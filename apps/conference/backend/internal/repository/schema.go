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
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// schemaCaps caches whether optional columns of the *shared* marketingops
// schema are present.
//
// This backend does not own that schema -- the agenda-organizer repo does, and
// its migrations are applied by hand with no migration-state table (see
// .claude/PLAN.endpoint-gap.md §5.4). So at any moment the live database may
// sit at any upstream revision, and a query naming a column that a not-yet-
// applied migration adds -- or one that an already-applied migration dropped --
// fails outright rather than degrading.
//
// The concrete case this exists for: upstream 023 added sessions.topic_id and
// the track_topics table, and 024 then *dropped* sessions.category. A query
// hardcoded either way 500s against half the possible database states. Probing
// once and shaping the SELECT accordingly means the agenda endpoints work
// against every upstream revision from 018 through 025.
//
// A failed probe leaves the capability false, which always selects the
// degraded-but-valid form, so a permissions or connectivity blip can never turn
// into a broken query.
type schemaCaps struct {
	topicOnce sync.Once
	hasTopics bool
}

// columnExists reports whether table.column exists in the connection's current
// schema (set from DB_SCHEMA via the DSN's search_path).
func columnExists(ctx context.Context, pool *pgxpool.Pool, table, column string) bool {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM information_schema.columns
		   WHERE table_schema = current_schema()
		     AND table_name = $1
		     AND column_name = $2
		 )`,
		table, column,
	).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

// topicSQL returns the SELECT expression and the extra JOIN clause to use for a
// session's topic label -- the field the API still exposes as `category`.
//
// When upstream 023 has been applied, the label comes from the resolved topic
// (track_topics.name, e.g. "API Management"). Otherwise it degrades to a
// literal NULL, which scans into a nil *string and serializes as an omitted
// key -- exactly what sessions.category produced before 024 dropped it, since
// that column was NULL on every row.
func (c *schemaCaps) topicSQL(ctx context.Context, pool *pgxpool.Pool) (selectExpr, joinClause string) {
	c.topicOnce.Do(func() {
		c.hasTopics = columnExists(ctx, pool, "sessions", "topic_id")
	})
	if c.hasTopics {
		return "tt.name", "LEFT JOIN track_topics tt ON tt.id = s.topic_id"
	}
	return "NULL::text", ""
}
