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

// Untagged, like activity_location_test.go and schema_test.go: an activity's
// window is arithmetic over a date and two minute offsets, and the below-029
// path is decided before any query is built, so both are settled without a
// database. The degrade case in particular *cannot* be written as an integration
// test of the real absence -- the test database either has upstream 029's tables
// or does not -- so the untagged form is the only one that pins it.

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The offsets are wall-clock minutes from midnight, so they land on the clock
// directly: no day origin added, no slot unit multiplied in.
func TestActivityWindow_MinutesAreWallClockFromMidnight(t *testing.T) {
	date := time.Date(2099, 11, 4, 0, 0, 0, 0, time.UTC)

	start, end := activityWindow(date, 540, 840, time.UTC)

	wantStart := time.Date(2099, 11, 4, 9, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2099, 11, 4, 14, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Errorf("start = %s, want %s", start, wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Errorf("end = %s, want %s", end, wantEnd)
	}
}

// The distinction that matters: fed the same numbers, computeSessionWindow means
// something else entirely, because a session's placement is a slot index over a
// day's grid origin. Asserting they disagree is what stops a later reader from
// "simplifying" one into the other.
func TestActivityWindow_IsNotTheSessionSlotArithmetic(t *testing.T) {
	date := time.Date(2099, 11, 4, 0, 0, 0, 0, time.UTC)

	start, _ := activityWindow(date, 540, 840, time.UTC)
	// The same 540 read as a session would: the day's start_minute plus
	// 540 slots of 5 minutes.
	sessionStart, _ := computeSessionWindow(date, 540, 540, 60, 5, time.UTC)

	if start.Equal(sessionStart) {
		t.Fatalf("activityWindow and computeSessionWindow agreed on %s; the units are not the same and the arithmetic must not be shared", start)
	}
	if got, want := start.Hour(), 9; got != want {
		t.Errorf("start hour = %d, want %d -- minutes from midnight, not a slot index", got, want)
	}
}

// Anchoring in the conference's location is what makes the serialized instant
// carry a real offset (+05:30) instead of a fake Z. The same wall-clock minutes
// are a different absolute instant per zone.
func TestActivityWindow_AnchorsInTheGivenLocation(t *testing.T) {
	colombo, err := time.LoadLocation("Asia/Colombo")
	if err != nil {
		t.Fatalf("loading Asia/Colombo: %v", err)
	}
	date := time.Date(2099, 11, 4, 0, 0, 0, 0, time.UTC)

	start, _ := activityWindow(date, 540, 840, colombo)

	want := time.Date(2099, 11, 4, 9, 0, 0, 0, colombo)
	if !start.Equal(want) {
		t.Errorf("start = %s, want %s", start, want)
	}
	if utcStart, _ := activityWindow(date, 540, 840, time.UTC); start.Equal(utcStart) {
		t.Error("the Colombo and UTC windows resolved to the same instant; the location was ignored")
	}
}

// A nil location is UTC, matching computeSessionWindow, so a repo constructed
// without a fallback still produces valid instants rather than panicking.
func TestActivityWindow_NilLocationDefaultsToUTC(t *testing.T) {
	date := time.Date(2099, 11, 4, 0, 0, 0, 0, time.UTC)

	start, end := activityWindow(date, 0, 1440, nil)

	if !start.Equal(time.Date(2099, 11, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("start = %s, want midnight UTC", start)
	}
	// end_minute is CHECKed <= 1440 upstream, and 1440 means the following
	// midnight rather than wrapping to the same day's.
	if !end.Equal(time.Date(2099, 11, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("end = %s, want the following midnight", end)
	}
}

// Below upstream 029 the tables are absent and the whole read is skipped, so
// List must not build a query at all. The nil pool makes that concrete: any
// query would panic rather than silently pass.
func TestActivityRepo_ListSkipsTheQueryWhenTheTablesAreAbsent(t *testing.T) {
	repo := NewActivityRepo(nil, time.UTC)
	repo.caps.activityTablesResolved = true
	repo.caps.hasActivityTables = false

	all, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("List returned error: %v, want the degrade-to-empty path", err)
	}
	if len(all) != 0 {
		t.Errorf("List returned %d activities, want none", len(all))
	}
}

// A resolved capability is answered from the cache, never re-probed, which is
// what lets List call it once per request without a round trip.
func TestSchemaCaps_ActivityTablesUsesTheCachedCapability(t *testing.T) {
	for _, tt := range []struct {
		label   string
		present bool
	}{
		{"tables present", true},
		{"tables absent", false},
	} {
		t.Run(tt.label, func(t *testing.T) {
			caps := &schemaCaps{activityTablesResolved: true, hasActivityTables: tt.present}

			if got := caps.activityTables(context.Background(), nil); got != tt.present {
				t.Errorf("activityTables = %v, want %v", got, tt.present)
			}
		})
	}
}

// A failed probe must not be cached: memoizing it would leave the endpoint
// permanently empty for the life of the process, which is indistinguishable from
// a venue with no amenities and therefore silent.
func TestSchemaCaps_ActivityTablesProbeFailureIsNotCached(t *testing.T) {
	// Port 1 is reserved and never listening, so the probe fails fast with a
	// connection error rather than an answered "table absent".
	pool, err := pgxpool.New(context.Background(), "postgres://user@127.0.0.1:1/nodb")
	if err != nil {
		t.Fatalf("building pool: %v", err)
	}
	defer pool.Close()

	caps := &schemaCaps{}

	if caps.activityTables(context.Background(), pool) {
		t.Error("activityTables = true after a failed probe, want the degraded false")
	}

	caps.mu.Lock()
	resolved := caps.activityTablesResolved
	caps.mu.Unlock()
	if resolved {
		t.Error("activity capability was marked resolved after a failed probe; the next request must retry instead of inheriting the failure")
	}
}

// A cancelled caller must not decide the capability for the whole process: the
// probe runs detached, so an already-cancelled request context still reaches the
// pool (which then refuses, degrading this request only).
func TestSchemaCaps_ActivityTablesProbeIgnoresCallerCancellation(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://user@127.0.0.1:1/nodb")
	if err != nil {
		t.Fatalf("building pool: %v", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	caps := &schemaCaps{}
	if caps.activityTables(ctx, pool) {
		t.Error("activityTables = true, want the degraded false")
	}

	caps.mu.Lock()
	defer caps.mu.Unlock()
	if caps.activityTablesResolved {
		t.Error("a cancelled caller must not resolve the capability")
	}
}

// Upstream 029 is a different migration from 018, 023 and 027, so resolving the
// table capability must not claim to have resolved the column ones -- a database
// can carry any combination of the four.
func TestSchemaCaps_ActivityCapabilityIsIndependentOfTheOthers(t *testing.T) {
	caps := &schemaCaps{activityTablesResolved: true, hasActivityTables: true}

	caps.mu.Lock()
	defer caps.mu.Unlock()
	if caps.resolved {
		t.Error("resolving the activity-tables capability must not resolve the topic one")
	}
	if caps.colorResolved {
		t.Error("resolving the activity-tables capability must not resolve the colour one")
	}
	if caps.venueResolved {
		t.Error("resolving the activity-tables capability must not resolve the venue one")
	}
}
