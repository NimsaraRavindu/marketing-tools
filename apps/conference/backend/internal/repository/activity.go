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

// ActivityRepo provides read access to the owned activities table
// (migration 011). loc is the venue timezone the stored instants are
// re-anchored to on the way out, so the API emits the venue offset rather
// than whatever zone the driver hands back (see config.Config.VenueLocation).
type ActivityRepo struct {
	pool *pgxpool.Pool
	loc  *time.Location
}

// NewActivityRepo constructs an ActivityRepo backed by the given pool.
// loc is the venue timezone start/end times are presented in; a nil loc
// defaults to UTC.
func NewActivityRepo(pool *pgxpool.Pool, loc *time.Location) *ActivityRepo {
	if loc == nil {
		loc = time.UTC
	}
	return &ActivityRepo{pool: pool, loc: loc}
}

// List returns every activity, ordered by name then start time.
//
// The old service returned rows in whatever order MySQL produced and left all
// grouping to the client. The client still groups by name, but ordering here
// means each group's occurrences arrive chronologically instead of relying on
// insertion order -- which the client's reduce-into-a-map would otherwise
// preserve verbatim, listing a Tuesday sitting above a Monday one.
func (r *ActivityRepo) List(ctx context.Context) ([]models.Activity, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, description, start_time, end_time,
		        location_name, location_address, location_floor_plan_url
		 FROM activities
		 ORDER BY name, start_time`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var activities []models.Activity
	for rows.Next() {
		var a models.Activity
		var locName, locAddress, locFloorPlanURL string

		if err := rows.Scan(
			&a.ID, &a.Name, &a.Description, &a.StartTime, &a.EndTime,
			&locName, &locAddress, &locFloorPlanURL,
		); err != nil {
			return nil, err
		}

		// start_time/end_time are TIMESTAMPTZ, so the instant is already
		// correct; .In only fixes which offset it serializes with, keeping
		// the "ISO-8601 with a real venue offset, never a naive Z" contract
		// the rest of the API holds (see openapi.yaml conventions).
		a.StartTime = a.StartTime.In(r.loc)
		a.EndTime = a.EndTime.In(r.loc)

		// All three location columns default to '', so a row with nothing
		// recorded yields no location object at all rather than one full of
		// empty strings.
		if locName != "" || locAddress != "" || locFloorPlanURL != "" {
			a.Location = &models.ActivityLocation{
				Name:         locName,
				Address:      locAddress,
				FloorPlanURL: locFloorPlanURL,
			}
		}

		activities = append(activities, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return activities, nil
}
