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

	"github.com/jackc/pgx/v5/pgxpool"

	"wso2-coin-backend/internal/models"
)

// FavoritesRepo provides read/write access to the favorites table (see
// .claude/PLAN.md Phase F). No PII here, so unlike SpeakerRepo/
// AttendeeProfileRepo it needs no piiKey.
type FavoritesRepo struct {
	pool *pgxpool.Pool
}

// NewFavoritesRepo constructs a FavoritesRepo backed by the given pool.
func NewFavoritesRepo(pool *pgxpool.Pool) *FavoritesRepo {
	return &FavoritesRepo{pool: pool}
}

// List returns userUUID's favorites as references (session id + when added),
// oldest first. Always a non-nil slice.
func (r *FavoritesRepo) List(ctx context.Context, userUUID string) ([]models.Favorite, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT session_id, created_at FROM favorites
		 WHERE user_uuid = $1
		 ORDER BY created_at, session_id`,
		userUUID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]models.Favorite, 0)
	for rows.Next() {
		var f models.Favorite
		if err := rows.Scan(&f.SessionID, &f.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// Add records a favorite. Idempotent: re-adding an existing (user, session)
// pair conflicts on the primary key and is a no-op, not an error.
func (r *FavoritesRepo) Add(ctx context.Context, userUUID, sessionID string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO favorites (user_uuid, session_id) VALUES ($1, $2)
		 ON CONFLICT (user_uuid, session_id) DO NOTHING`,
		userUUID, sessionID,
	)
	return err
}

// Remove deletes a favorite. Idempotent: removing one that doesn't exist
// affects zero rows and is not an error.
func (r *FavoritesRepo) Remove(ctx context.Context, userUUID, sessionID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM favorites WHERE user_uuid = $1 AND session_id = $2`,
		userUUID, sessionID,
	)
	return err
}
