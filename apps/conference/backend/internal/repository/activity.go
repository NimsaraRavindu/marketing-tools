// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wso2-coin-backend/internal/models"
)

// ActivityRepo reads the conference's non-session happenings -- networking
// events, receptions, parties -- from the shared sessions table.
//
// This used to read an `activities` table owned by this repo (migration 011),
// created on the finding that no upstream equivalent existed. That is no longer
// true: upstream migration 021 added 'activity' to the sessions.kind vocabulary
// precisely for these entries, and the organizer's admin UI has a shipped form
// that authors them. So this now reads the shared schema, and the owned table is
// gone -- it had no write path anywhere and was never applied to any database,
// which meant this endpoint could only ever have returned nothing.
//
// The trade: upstream models no per-activity location. There are no
// address/floor-plan columns on sessions under any kind, and 020's room-mapping
// trigger deliberately resolves room to NULL for every non-'session' kind, so an
// activity's Location is always absent. The client optional-chains it, so a card
// renders without its location line rather than breaking. That is a real
// reduction against what the owned table modelled -- and a strict improvement
// over the nothing it actually held.
type ActivityRepo struct {
	pool        *pgxpool.Pool
	slotMinutes int
	loc         *time.Location
}

// NewActivityRepo constructs an ActivityRepo backed by the given pool.
// slotMinutes and loc are used exactly as SessionRepo uses them, to turn a
// day + slot_index into wall-clock instants; a nil loc defaults to UTC.
func NewActivityRepo(pool *pgxpool.Pool, slotMinutes int, loc *time.Location) *ActivityRepo {
	if loc == nil {
		loc = time.UTC
	}
	return &ActivityRepo{pool: pool, slotMinutes: slotMinutes, loc: loc}
}

// List returns the current conference's activities, ordered by name then start
// time.
//
// Scoped to the current conference (latest start_date), the same rule every other
// read here uses -- the General page shows what is happening at this event, and
// last year's party is not that.
//
// Unscheduled activities are excluded. Start and end time are the whole point of
// an activity's rendering, and a row with no day or slot has neither; including
// it would put a card with a blank time on the page.
//
// Ordered by name first because the client groups by name and its
// reduce-into-a-map preserves receive order: without this, one sitting of a
// recurring activity could list above an earlier one.
//
// kind is compared as text rather than against the bare literal. Upstream 021
// added 'activity' to the kind vocabulary, and if that column is a Postgres enum
// then naming a label the type does not yet carry is not an empty result but an
// "invalid input value for enum" error -- this endpoint would 500 against every
// database still below 021, which is exactly the failure schemaCaps exists to
// avoid elsewhere. The cast costs an index scan on a table holding a handful of
// rows per conference, and buys the same degrade-to-empty behaviour the rest of
// this package has against an unknown upstream revision.
func (r *ActivityRepo) List(ctx context.Context) ([]models.Activity, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT s.id, s.title, s.description,
		        d.date, d.start_minute, s.slot_index, s.duration_slots,
		        cc.timezone
		 FROM sessions s
		 JOIN conference_days d ON d.id = s.day_id
		 JOIN conference_config cc ON cc.id = s.config_id
		 WHERE s.kind::text = 'activity'
		   AND s.config_id = (SELECT id FROM conference_config ORDER BY start_date DESC, id DESC LIMIT 1)
		   AND s.slot_index IS NOT NULL
		 ORDER BY s.title, d.date, s.slot_index`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var activities []models.Activity
	for rows.Next() {
		var (
			a             models.Activity
			date          time.Time
			startMinute   int
			slotIndex     int
			durationSlots int
			cfgTZ         string
		)

		if err := rows.Scan(
			&a.ID, &a.Name, &a.Description,
			&date, &startMinute, &slotIndex, &durationSlots,
			&cfgTZ,
		); err != nil {
			return nil, err
		}

		// conference_config.timezone is the source of truth for anchoring; the
		// env-configured fallback only covers an empty value.
		loc := r.loc
		if cfgTZ != "" {
			loc = locationFor(cfgTZ)
		}

		a.StartTime, a.EndTime = computeSessionWindow(date, startMinute, slotIndex, durationSlots, r.slotMinutes, loc)
		activities = append(activities, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return activities, nil
}
