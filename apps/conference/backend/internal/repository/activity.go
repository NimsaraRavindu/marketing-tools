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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wso2-coin-backend/internal/models"
)

// ActivityRepo reads the venue's amenities -- the O2 Bar, an FDE booth, a
// registration desk -- from the upstream-owned con_activities and
// con_activity_hours tables.
//
// It has moved twice. It first read an `activities` table owned by this repo
// (migration 011), created on the finding that no upstream equivalent existed;
// that table had no write path anywhere and was never applied, so the endpoint
// could only ever return nothing. Upstream 021 then added 'activity' to the
// sessions.kind vocabulary and this read sessions.kind='activity' instead.
//
// Upstream 029 splits that kind in two, and this follows it. One kind was
// carrying two unrelated ideas: 021 meant Networking Event, Speaker Reception,
// Conference Party -- timed things that interrupt the whole event, render as a
// full-width bar in the public agenda template, and belong in the printed
// export. A bar that is open from 09:00 to 14:00 alongside the sessions is the
// opposite on every count, and must never appear in that export. One kind
// cannot answer "does the export publish this" both ways, so the amenities move
// to their own tables and 'activity' goes back to meaning only the first thing.
// The General page wants the amenities, which is why this endpoint follows the
// second idea rather than staying on the kind.
//
// The two tables split the recurring thing from its openings:
// con_activities is event-scoped (one O2 Bar, UNIQUE (config_id, name)) and
// con_activity_hours holds one open window per day, with a day the activity does
// not run simply having no row. This still returns one models.Activity per
// *sitting*, keyed by the hours row's id, because the client groups sittings by
// name itself and the response shape predates the split.
//
// con_activity_hours.start_minute/end_minute are wall-clock minutes from
// midnight, the unit conference_days already uses -- not the sessions slot grid.
// Activities are not placed against tracks and have no slot origin to inherit,
// so computeSessionWindow and SESSION_SLOT_MINUTES do not apply here; the
// instants come straight off the day's date plus the two offsets, anchored the
// same way every other read anchors: conference_config.timezone first, the
// configured fallback only when it is empty.
//
// Below upstream 029 the tables are absent, and naming a missing *table* is a
// hard error on the whole endpoint rather than an empty result -- so the
// capability is probed (see schemaCaps.activityTables) and an unprobed or absent
// pair returns no activities at all. That is the same degrade-to-empty this
// package does everywhere else, and it is what makes this deployable before the
// migration is applied by hand. Note that it degrades to *empty*, not back to
// sessions.kind='activity': after 029 that kind means only the timed bars, which
// belong in the agenda and not on the General page, so falling back to it would
// publish the wrong rows rather than fewer of the right ones.
//
// Location is unaffected by any of this. con_activities models no location of
// its own -- there are no address or floor-plan columns on it -- so it stays the
// conference's own venue, from the venue_name/venue_address that upstream 018
// put on conference_config, the table this query already joins for cc.timezone.
// Every activity in a conference therefore reports the same location, which is
// the truth about a single-venue event. Both columns are nullable and
// unpopulated until the content team fills them in, so the absent path is the
// live one today, not a theoretical branch: with no venue name there is no
// Location at all rather than one naming nowhere. See venueLocation for why that
// distinction is not cosmetic. FloorPlanURL is never set -- nothing upstream
// models a floor plan.
type ActivityRepo struct {
	pool *pgxpool.Pool
	loc  *time.Location
	caps schemaCaps
}

// NewActivityRepo constructs an ActivityRepo backed by the given pool.
//
// loc is the venue-timezone fallback, used exactly as SessionRepo uses it and
// only when conference_config.timezone is empty; a nil loc defaults to UTC.
// There is deliberately no slotMinutes: an activity's hours are wall-clock
// minutes from midnight (upstream 029), not slot indices, so the session grid
// unit has nothing to say about them.
func NewActivityRepo(pool *pgxpool.Pool, loc *time.Location) *ActivityRepo {
	if loc == nil {
		loc = time.UTC
	}
	return &ActivityRepo{pool: pool, loc: loc}
}

// List returns the current conference's activity sittings, in the order the
// content team arranged the amenities, chronologically within each one.
//
// One row per con_activity_hours row, not per con_activities row. An O2 Bar open
// on two days is two entries here sharing a name, which is the shape the client
// already consumes: it reduces the flat array into a map keyed by name and
// renders one card carrying several times. Keeping the split invisible to the
// microapp is deliberate -- 029 reorganised the *storage* of these entries, and
// there is no reason for a schema tidy-up upstream to cost the client a release.
//
// Activity.ID is therefore the hours row's id, not the activity's: it has to be
// unique per entry (React keys, and the client dedupes by it), and the activity
// id would repeat once per sitting.
//
// Scoped to the current conference (latest start_date), the same rule every
// other read here uses -- the General page shows what is open at this event, and
// last year's bar is not that. con_activities is config-scoped directly, so the
// scope is one predicate on the parent rather than a reach through the day.
//
// Ordered by con_activities.position first, then lower(name) -- the same order
// upstream's own list uses (ActivitiesRepo.ListForConfig). position is the
// content team's arrangement of the General page: 029 gave the column an index
// on (config_id, position) and the admin Activities page a control for it, so
// ignoring it here would let them reorder the cards and watch nothing happen.
// The column defaults to 0, so rows entered before that control existed all tie
// and fall through to the name, which is the ordering this endpoint had before;
// lower() makes that fall-through case-insensitive, so "o2 Bar" no longer sorts
// below every capitalised name instead of next to them.
//
// Whatever leads has to be an *activity*-level key, not a sitting-level one. The
// client reduces this flat array into a map keyed by name and its group-by
// preserves receive order, so a leading key that differed between two sittings
// of one amenity would interleave them with another's and split the card.
// position and name are both per-activity; every sitting of an amenity carries
// the same pair and they stay contiguous.
//
// Within an amenity, day_index then start_minute lists the sittings in the order
// an attendee meets them, and h.id breaks the tie between two windows that begin
// at the same minute -- con_activity_hours has no unique key forbidding that, and
// without a final tiebreak their relative order is whatever the plan happens to
// return. day_index rather than d.date for the day, matching both EventRepo here
// and upstream's own hours ordering: it is the conference's stated reading order
// rather than an inference from the calendar.
//
// There is no "unscheduled" case left to exclude. Under the old sessions-backed
// query an activity could exist with a NULL day or slot and had to be filtered
// out, since a card with a blank time is worse than no card; a con_activity_hours
// row is a window by construction (both minute columns NOT NULL, CHECK end >
// start), and an activity with no windows contributes no rows to begin with.
//
// Returns nothing at all, with no error, when the tables are absent -- see the
// type comment and schemaCaps.activityTables.
func (r *ActivityRepo) List(ctx context.Context) ([]models.Activity, error) {
	if !r.caps.activityTables(ctx, r.pool) {
		return nil, nil
	}

	venueNameExpr, venueAddressExpr := r.caps.venueSQL(ctx, r.pool)

	rows, err := r.pool.Query(ctx,
		fmt.Sprintf(
			`SELECT h.id, a.name, a.description,
			        d.date, h.start_minute, h.end_minute,
			        cc.timezone, %s, %s
			 FROM con_activity_hours h
			 JOIN con_activities a ON a.id = h.activity_id
			 JOIN conference_days d ON d.id = h.day_id
			 JOIN conference_config cc ON cc.id = a.config_id
			 WHERE a.config_id = (SELECT id FROM conference_config ORDER BY start_date DESC, id DESC LIMIT 1)
			 ORDER BY a.position, lower(a.name), d.day_index, h.start_minute, h.id`,
			venueNameExpr, venueAddressExpr,
		),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var activities []models.Activity
	for rows.Next() {
		var (
			a            models.Activity
			date         time.Time
			startMinute  int
			endMinute    int
			cfgTZ        string
			venueName    *string
			venueAddress *string
		)

		if err := rows.Scan(
			&a.ID, &a.Name, &a.Description,
			&date, &startMinute, &endMinute,
			&cfgTZ, &venueName, &venueAddress,
		); err != nil {
			return nil, err
		}

		a.Location = venueLocation(venueName, venueAddress)

		// conference_config.timezone is the source of truth for anchoring; the
		// env-configured fallback only covers an empty value.
		loc := r.loc
		if cfgTZ != "" {
			loc = locationFor(cfgTZ)
		}

		a.StartTime, a.EndTime = activityWindow(date, startMinute, endMinute, loc)
		activities = append(activities, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return activities, nil
}

// activityWindow turns a day's date plus a pair of wall-clock minute offsets
// into instants anchored in the conference timezone.
//
// Deliberately not computeSessionWindow. That function converts a slot index
// through slotMinutes and adds the day's own start_minute as the grid origin,
// because a session is placed on the agenda grid. An activity is placed on the
// clock: con_activity_hours.start_minute is already minutes from midnight
// (upstream 029, same unit as conference_days.start_minute), so the day's origin
// must not be added again and there is no grid unit to multiply by. Feeding
// these values to computeSessionWindow would silently shift every activity by
// the day's start_minute and stretch it by SESSION_SLOT_MINUTES.
//
// Anchoring in loc rather than UTC is what makes the serialized instants carry
// the venue's real offset (e.g. +05:30), and it is also what makes them correct
// across a DST boundary: 09:00 local is a different absolute instant either side
// of one, and time.Date resolves that against the zone rather than assuming a
// fixed offset. A nil loc defaults to UTC, matching computeSessionWindow.
func activityWindow(date time.Time, startMinute, endMinute int, loc *time.Location) (start, end time.Time) {
	if loc == nil {
		loc = time.UTC
	}
	dayMidnight := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	start = dayMidnight.Add(time.Duration(startMinute) * time.Minute)
	end = dayMidnight.Add(time.Duration(endMinute) * time.Minute)
	return start, end
}

// venueLocation turns the conference's venue columns into the nested location
// object, or into nothing at all.
//
// The name is what decides. ActivityLocation.Name is the one field that is not
// omitempty -- the client reads `location?.name` and renders it as the label --
// so a location built from a NULL or blank venue_name would serialize as
// `"location": {"name": ""}`: a present object announcing a place with no name,
// which is worse for the client than the absent key it already handles, and a
// breach of the API-wide "optional scalars are omitted when empty, never empty
// strings" convention (openapi.yaml header). Both columns are nullable and
// default to unset upstream, so this is the state a database sits in until the
// content team fills the venue in, not an edge case.
//
// An address with no name is dropped with it rather than promoted: an address
// alone has nothing to label it in the UI. The reverse -- a name with no address
// -- is the ordinary partially-filled case, and omits Address rather than
// sending "".
func venueLocation(name, address *string) *models.ActivityLocation {
	if name == nil || *name == "" {
		return nil
	}
	loc := &models.ActivityLocation{Name: *name}
	if address != nil && *address != "" {
		loc.Address = *address
	}
	return loc
}
