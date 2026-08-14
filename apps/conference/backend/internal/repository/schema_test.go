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

// Unlike the rest of this package these tests carry no `integration` tag: the
// point of schemaCaps is which SQL it emits for a given capability state, and
// that is decidable without a database. A resolved cache never reaches the
// pool, so the branch tests pass a nil one -- if the cache were consulted
// incorrectly the nil would panic rather than silently pass.

package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSchemaCaps_TopicSQL_ResolvesTopicNameWhenCapabilityPresent(t *testing.T) {
	caps := &schemaCaps{resolved: true, hasTopics: true}

	selectExpr, joinClause := caps.topicSQL(context.Background(), nil)

	if selectExpr != "tt.name" {
		t.Errorf("selectExpr = %q, want %q", selectExpr, "tt.name")
	}
	if !strings.Contains(joinClause, "LEFT JOIN track_topics") {
		t.Errorf("joinClause = %q, want a LEFT JOIN onto track_topics", joinClause)
	}
	// A LEFT (not INNER) join is the whole reason a session with a NULL
	// topic_id still lists, so assert the join type specifically.
	if strings.Contains(joinClause, "INNER JOIN") {
		t.Errorf("joinClause = %q, must not INNER JOIN -- it would drop sessions with a NULL topic_id", joinClause)
	}
}

func TestSchemaCaps_TopicSQL_DegradesToNullWhenCapabilityAbsent(t *testing.T) {
	caps := &schemaCaps{resolved: true, hasTopics: false}

	selectExpr, joinClause := caps.topicSQL(context.Background(), nil)

	// NULL::text, not a bare NULL: it has to scan into a *string the same way
	// the resolved form does, which is what makes the degraded shape a drop-in.
	if selectExpr != "NULL::text" {
		t.Errorf("selectExpr = %q, want %q", selectExpr, "NULL::text")
	}
	if joinClause != "" {
		t.Errorf("joinClause = %q, want empty -- track_topics may not exist at this revision", joinClause)
	}
}

// A failed probe must not be cached. Memoizing it would turn a transient blip
// into a permanent, silent loss of `category` for the life of the process.
func TestSchemaCaps_FailedProbeIsNotCachedAndDegradesThatRequest(t *testing.T) {
	// Port 1 is reserved and never listening, so the probe fails fast with a
	// connection error rather than an answered "column absent".
	pool, err := pgxpool.New(context.Background(), "postgres://user@127.0.0.1:1/nodb")
	if err != nil {
		t.Fatalf("building pool: %v", err)
	}
	defer pool.Close()

	caps := &schemaCaps{}

	selectExpr, joinClause := caps.topicSQL(context.Background(), pool)
	if selectExpr != "NULL::text" || joinClause != "" {
		t.Errorf("topicSQL = (%q, %q), want the degraded form when the probe fails", selectExpr, joinClause)
	}

	caps.mu.Lock()
	resolved := caps.resolved
	caps.mu.Unlock()
	if resolved {
		t.Error("capability was marked resolved after a failed probe; the next request must retry instead of inheriting the failure")
	}
}

// A cancelled caller must not decide the capability for the whole process: the
// probe runs detached, so an already-cancelled request context still resolves.
func TestSchemaCaps_ProbeIgnoresCallerCancellation(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://user@127.0.0.1:1/nodb")
	if err != nil {
		t.Fatalf("building pool: %v", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	caps := &schemaCaps{}
	// Reaching the pool at all proves the cancelled context did not short
	// circuit the probe; the connection then refuses, so this degrades.
	if selectExpr, _ := caps.topicSQL(ctx, pool); selectExpr != "NULL::text" {
		t.Errorf("selectExpr = %q, want the degraded form", selectExpr)
	}

	caps.mu.Lock()
	defer caps.mu.Unlock()
	if caps.resolved {
		t.Error("a cancelled caller must not resolve the capability")
	}
}
