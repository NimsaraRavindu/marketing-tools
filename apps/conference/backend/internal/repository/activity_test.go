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

//go:build integration

package repository

import (
	"context"
	"testing"
	"time"
)

// insertActivity seeds one activity row and removes it when the test ends.
func insertActivity(t *testing.T, name, description string, start time.Time, locName, locAddress, locFloorPlan string) string {
	t.Helper()
	id := newUUID()
	_, err := testDB.Exec(context.Background(),
		`INSERT INTO activities (id, name, description, start_time, end_time,
		    location_name, location_address, location_floor_plan_url)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, name, description, start, start.Add(time.Hour), locName, locAddress, locFloorPlan,
	)
	if err != nil {
		t.Fatalf("failed to insert activity: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM activities WHERE id = $1", id)
	})
	return id
}

func TestActivityRepo_ListReturnsNestedLocation(t *testing.T) {
	ctx := context.Background()
	repo := NewActivityRepo(testDB, time.UTC)

	name := "Registration " + newUUID()
	start := time.Now().UTC().Truncate(time.Second)
	id := insertActivity(t, name, "Pick up your badge", start,
		"Main Foyer", "123 Conference Way", "https://example.com/floor.png")

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	var found bool
	for _, a := range all {
		if a.ID != id {
			continue
		}
		found = true
		if a.Location == nil {
			t.Fatal("Location is nil, want the inlined columns nested")
		}
		if a.Location.Name != "Main Foyer" || a.Location.Address != "123 Conference Way" {
			t.Errorf("Location = %+v, want Main Foyer / 123 Conference Way", a.Location)
		}
		if a.Location.FloorPlanURL != "https://example.com/floor.png" {
			t.Errorf("FloorPlanURL = %q, want the seeded URL", a.Location.FloorPlanURL)
		}
		if !a.StartTime.Equal(start) {
			t.Errorf("StartTime = %v, want %v", a.StartTime, start)
		}
	}
	if !found {
		t.Fatalf("activity %s not returned by List", id)
	}
}

// All three location columns default to the empty string, so a row with none recorded must
// come back with no location object rather than one full of empty strings.
func TestActivityRepo_ListOmitsEmptyLocation(t *testing.T) {
	ctx := context.Background()
	repo := NewActivityRepo(testDB, time.UTC)

	id := insertActivity(t, "Hallway track "+newUUID(), "", time.Now().UTC(), "", "", "")

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	for _, a := range all {
		if a.ID == id && a.Location != nil {
			t.Fatalf("Location = %+v, want nil for a row with no location", a.Location)
		}
	}
}

// Occurrences of one activity must arrive chronologically, since the client
// groups by name and preserves the order it receives.
func TestActivityRepo_ListOrdersOccurrencesByStartTime(t *testing.T) {
	ctx := context.Background()
	repo := NewActivityRepo(testDB, time.UTC)

	name := "Lunch " + newUUID()
	day2 := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	day1 := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)

	// Inserted out of order on purpose.
	insertActivity(t, name, "", day2, "Hall B", "", "")
	insertActivity(t, name, "", day1, "Hall A", "", "")

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	var seen []time.Time
	for _, a := range all {
		if a.Name == name {
			seen = append(seen, a.StartTime)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("got %d occurrences of %q, want 2", len(seen), name)
	}
	if seen[0].After(seen[1]) {
		t.Errorf("occurrences out of order: %v then %v", seen[0], seen[1])
	}
}

// The CHECK constraint is what stops an activity that ends before it starts.
func TestActivityRepo_RejectsInvertedTimeRange(t *testing.T) {
	start := time.Now().UTC()
	_, err := testDB.Exec(context.Background(),
		`INSERT INTO activities (name, start_time, end_time) VALUES ($1, $2, $3)`,
		"Impossible "+newUUID(), start, start.Add(-time.Hour),
	)
	if err == nil {
		t.Fatal("expected the activities_time_order CHECK to reject end_time < start_time")
	}
}
