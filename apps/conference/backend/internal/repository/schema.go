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
	"log/slog"
	"sync"
	"time"

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
// The second capability is the same story one migration later: upstream 027
// adds rooms.color_token/tracks.color_token, which every session, speaker and
// agenda read now selects. Naming them against a database that predates 027
// would 500 those reads outright, so an unprobed or absent column degrades to
// the literal ColorTokenDefault -- the same value the COALESCE would have
// produced for a row with no token anywhere.
//
// A failed probe degrades *that request only* to the safe form and stays
// unresolved, so the next request retries. Memoizing a failure would turn a
// one-off blip into a permanent, silent loss of the field for the lifetime of
// the process -- the capability is cached once it is actually known, not once it
// has been asked about.
//
// Only an answered probe is cached, so a migration applied by hand while the
// server is running is still not picked up until something re-probes; once the
// capability resolves true it never needs to change, and the reverse (an
// upstream rollback) is not a state this degrades into silently, because the
// query would then fail loudly rather than return wrong data.
type schemaCaps struct {
	mu        sync.Mutex
	resolved  bool
	hasTopics bool

	// Colour-token capability, resolved independently of the topic one: the
	// two arrive in different upstream migrations, so a database can have
	// either, both or neither.
	colorResolved      bool
	hasRoomColorToken  bool
	hasTrackColorToken bool
}

// schemaProbeTimeout bounds a single capability probe. It is deliberately short:
// the probe is an EXISTS against information_schema, and a caller waiting on it
// is a request that could instead be served in the degraded shape.
const schemaProbeTimeout = 3 * time.Second

// columnExists reports whether table.column exists in the connection's current
// schema (set from DB_SCHEMA via the DSN's search_path). A probe that could not
// be answered is an error, distinct from an answered "no" -- the caller has to
// tell them apart to know whether the result is worth caching.
func columnExists(ctx context.Context, pool *pgxpool.Pool, table, column string) (bool, error) {
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
		return false, err
	}
	return exists, nil
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
	if c.hasTopicID(ctx, pool) {
		return "tt.name", "LEFT JOIN track_topics tt ON tt.id = s.topic_id"
	}
	return "NULL::text", ""
}

// hasTopicID reports whether sessions.topic_id exists, probing at most once
// successfully and retrying on every request until it gets an answer.
//
// The probe runs detached from the caller's context. A request-scoped context is
// the wrong lifetime for a process-wide fact: the first request to arrive would
// otherwise get to decide the capability for every later one, and a client that
// disconnects mid-probe would cancel it -- indistinguishable, at this layer,
// from the column genuinely being absent.
//
// The lock is not held across the query. Concurrent callers on a cold cache may
// each probe, which costs a duplicate EXISTS against information_schema and
// nothing else; holding it would instead serialise every request behind one
// round-trip, which is the worse trade when the database is slow.
func (c *schemaCaps) hasTopicID(ctx context.Context, pool *pgxpool.Pool) bool {
	c.mu.Lock()
	if c.resolved {
		defer c.mu.Unlock()
		return c.hasTopics
	}
	c.mu.Unlock()

	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), schemaProbeTimeout)
	defer cancel()

	exists, err := columnExists(probeCtx, pool, "sessions", "topic_id")
	if err != nil {
		// Serve this request in the degraded shape but leave the capability
		// unresolved, so a transient failure costs one request's category
		// field rather than every request until the process restarts.
		slog.Warn("schema capability probe failed, serving degraded",
			"table", "sessions", "column", "topic_id", "error", err)
		return false
	}

	c.mu.Lock()
	c.resolved, c.hasTopics = true, exists
	c.mu.Unlock()

	slog.Info("schema capability resolved", "table", "sessions", "column", "topic_id", "present", exists)
	return exists
}

// colorTokenSQL returns the SELECT expression for a session's colour token --
// the only colour field the API publishes.
//
// Upstream 027 puts the token on rooms and tracks and fixes the precedence:
// COALESCE(rooms.color_token, tracks.color_token, 'main'). The colour belongs
// to the room, the stable thing an attendee navigates by; the track is the
// fallback for tracks that have no room. The expression assumes the caller's
// query already aliases rooms as r and tracks as t, which all four colour-
// reading queries do.
//
// Each column is folded in only if it is actually there, so a database at any
// point of the 027 rollout -- neither column, or one of them if the ALTERs are
// applied separately -- gets a valid query rather than a 500. With neither, the
// expression is the literal default: every session comes back "main", which is
// exactly what a client renders for a row that has no token upstream either.
func (c *schemaCaps) colorTokenSQL(ctx context.Context, pool *pgxpool.Pool) string {
	return colorTokenExpr(c.colorTokenColumns(ctx, pool))
}

// colorTokenExpr builds the expression for a given pair of capabilities. Split
// out from the probe so the precedence it encodes -- room, then track, then the
// default -- is testable without a database.
func colorTokenExpr(room, track bool) string {
	switch {
	case room && track:
		return fmt.Sprintf("COALESCE(r.color_token, t.color_token, '%s')", ColorTokenDefault)
	case room:
		return fmt.Sprintf("COALESCE(r.color_token, '%s')", ColorTokenDefault)
	case track:
		return fmt.Sprintf("COALESCE(t.color_token, '%s')", ColorTokenDefault)
	default:
		return fmt.Sprintf("'%s'::text", ColorTokenDefault)
	}
}

// colorTokenColumns reports whether rooms.color_token and tracks.color_token
// exist, probing at most once successfully and retrying on every request until
// it gets an answer.
//
// Same lifetime rules as hasTopicID, for the same reasons: the probe runs
// detached from the caller's context so one request cannot decide a
// process-wide fact by disconnecting mid-probe, the lock is not held across the
// query, and only an answered probe is cached so a transient failure costs one
// request's colour rather than every request until the process restarts.
//
// Both columns are probed under one resolved flag because 027 adds them
// together; two round trips on a cold cache is the price of not inventing a
// second caching scheme for a fact that always moves as a pair.
func (c *schemaCaps) colorTokenColumns(ctx context.Context, pool *pgxpool.Pool) (room, track bool) {
	c.mu.Lock()
	if c.colorResolved {
		defer c.mu.Unlock()
		return c.hasRoomColorToken, c.hasTrackColorToken
	}
	c.mu.Unlock()

	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), schemaProbeTimeout)
	defer cancel()

	room, err := columnExists(probeCtx, pool, "rooms", "color_token")
	if err == nil {
		track, err = columnExists(probeCtx, pool, "tracks", "color_token")
	}
	if err != nil {
		// Serve this request with the default token and leave the capability
		// unresolved, so the next request re-probes.
		slog.Warn("schema capability probe failed, serving degraded",
			"table", "rooms/tracks", "column", "color_token", "error", err)
		return false, false
	}

	c.mu.Lock()
	c.colorResolved, c.hasRoomColorToken, c.hasTrackColorToken = true, room, track
	c.mu.Unlock()

	slog.Info("schema capability resolved", "table", "rooms/tracks", "column", "color_token",
		"rooms", room, "tracks", track)
	return room, track
}
