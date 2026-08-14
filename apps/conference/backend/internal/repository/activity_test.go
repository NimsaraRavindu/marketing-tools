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

// activityFixture is a conference_config + conference_days pair with a far-future
// start_date, so it wins the "current conference = latest start_date" rule that
// ActivityRepo.List scopes on and the assertions below don't depend on whatever
// real data the shared database holds.
type activityFixture struct {
	configID string
	dayID    string
	date     string
}

func newActivityFixture(t *testing.T, ctx context.Context) *activityFixture {
	t.Helper()

	const date = "2099-11-04"

	var configID string
	if err := testDB.QueryRow(ctx,
		"INSERT INTO conference_config (name, start_date, timezone) VALUES ($1, $2, 'UTC') RETURNING id",
		"TDD Activity Conference", date,
	).Scan(&configID); err != nil {
		t.Fatalf("failed to insert conference_config: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM conference_config WHERE id = $1", configID)
	})

	var dayID string
	if err := testDB.QueryRow(ctx,
		"INSERT INTO conference_days (config_id, day_index, date, start_minute) VALUES ($1, 0, $2, 540) RETURNING id",
		configID, date,
	).Scan(&dayID); err != nil {
		t.Fatalf("failed to insert conference_day: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM conference_days WHERE id = $1", dayID)
	})

	return &activityFixture{configID: configID, dayID: dayID, date: date}
}

// insertActivity seeds one kind='activity' session and removes it afterwards.
// A nil slotIndex leaves the activity unscheduled.
func (f *activityFixture) insertActivity(t *testing.T, ctx context.Context, title, description string, slotIndex *int, durationSlots int) string {
	t.Helper()
	var id string
	err := testDB.QueryRow(ctx,
		`INSERT INTO sessions (config_id, kind, title, description, day_id, slot_index, duration_slots)
		 VALUES ($1, 'activity', $2, $3, $4, $5, $6) RETURNING id`,
		f.configID, title, description, f.dayID, slotIndex, durationSlots,
	).Scan(&id)
	if err != nil {
		t.Fatalf("failed to insert activity session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM sessions WHERE id = $1", id)
	})
	return id
}

func slot(i int) *int { return &i }

func TestActivityRepo_ListComputesTimesFromDayAndSlot(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, 5, time.UTC)

	title := "Networking Event " + newUUID()
	// start_minute 540 (09:00) + slot 12 * 5min = 10:00; 24 slots = 2h -> 12:00.
	id := fixture.insertActivity(t, ctx, title, "Drinks in the foyer", slot(12), 24)

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
		if a.Name != title {
			t.Errorf("Name = %q, want %q", a.Name, title)
		}
		if a.Description != "Drinks in the foyer" {
			t.Errorf("Description = %q, want %q", a.Description, "Drinks in the foyer")
		}
		wantStart := time.Date(2099, 11, 4, 10, 0, 0, 0, time.UTC)
		wantEnd := time.Date(2099, 11, 4, 12, 0, 0, 0, time.UTC)
		if !a.StartTime.Equal(wantStart) {
			t.Errorf("StartTime = %s, want %s", a.StartTime, wantStart)
		}
		if !a.EndTime.Equal(wantEnd) {
			t.Errorf("EndTime = %s, want %s", a.EndTime, wantEnd)
		}
		// Upstream models no per-activity location, so this must be omitted
		// rather than an object of empty strings.
		if a.Location != nil {
			t.Errorf("Location = %+v, want nil", a.Location)
		}
	}
	if !found {
		t.Fatalf("activity %s not returned by List", id)
	}
}

// A break or a normal session must never show up as an activity: the General
// page is not the agenda.
func TestActivityRepo_ListExcludesOtherKinds(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, 5, time.UTC)

	var breakID string
	if err := testDB.QueryRow(ctx,
		`INSERT INTO sessions (config_id, kind, title, day_id, slot_index, duration_slots)
		 VALUES ($1, 'break', 'Lunch', $2, 30, 12) RETURNING id`,
		fixture.configID, fixture.dayID,
	).Scan(&breakID); err != nil {
		t.Fatalf("failed to insert break: %v", err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(context.Background(), "DELETE FROM sessions WHERE id = $1", breakID) })

	sessionID := fixture.insertActivity(t, ctx, "Real Activity "+newUUID(), "", slot(6), 6)
	if _, err := testDB.Exec(ctx, "UPDATE sessions SET kind = 'session' WHERE id = $1", sessionID); err != nil {
		t.Fatalf("failed to flip kind: %v", err)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	for _, a := range all {
		if a.ID == breakID {
			t.Errorf("break %s returned as an activity", breakID)
		}
		if a.ID == sessionID {
			t.Errorf("session %s returned as an activity", sessionID)
		}
	}
}

// An activity with no slot has no start or end time, so it cannot be rendered
// and must not be returned.
func TestActivityRepo_ListExcludesUnscheduled(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, 5, time.UTC)

	id := fixture.insertActivity(t, ctx, "Unscheduled "+newUUID(), "", nil, 1)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	for _, a := range all {
		if a.ID == id {
			t.Fatalf("unscheduled activity %s was returned", id)
		}
	}
}

// Two sittings of the same activity must arrive in chronological order: the
// client groups by name and preserves receive order, so an unordered result
// would list a later sitting above an earlier one.
func TestActivityRepo_ListOrdersSittingsChronologically(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, 5, time.UTC)

	name := "Registration " + newUUID()
	late := fixture.insertActivity(t, ctx, name, "", slot(60), 6)
	early := fixture.insertActivity(t, ctx, name, "", slot(12), 6)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	var order []string
	for _, a := range all {
		if a.Name == name {
			order = append(order, a.ID)
		}
	}
	if len(order) != 2 {
		t.Fatalf("got %d sittings of %q, want 2", len(order), name)
	}
	if order[0] != early || order[1] != late {
		t.Errorf("sitting order = %v, want [%s %s]", order, early, late)
	}
}

// Activities belonging to an older conference must not leak into the current
// event's list.
func TestActivityRepo_ListScopesToCurrentConference(t *testing.T) {
	ctx := context.Background()
	current := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, 5, time.UTC)

	var oldConfigID, oldDayID string
	if err := testDB.QueryRow(ctx,
		"INSERT INTO conference_config (name, start_date, timezone) VALUES ($1, '2000-01-01', 'UTC') RETURNING id",
		"TDD Old Conference",
	).Scan(&oldConfigID); err != nil {
		t.Fatalf("failed to insert old conference_config: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM conference_config WHERE id = $1", oldConfigID)
	})
	if err := testDB.QueryRow(ctx,
		"INSERT INTO conference_days (config_id, day_index, date, start_minute) VALUES ($1, 0, '2000-01-01', 540) RETURNING id",
		oldConfigID,
	).Scan(&oldDayID); err != nil {
		t.Fatalf("failed to insert old conference_day: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM conference_days WHERE id = $1", oldDayID)
	})

	var oldActivityID string
	if err := testDB.QueryRow(ctx,
		`INSERT INTO sessions (config_id, kind, title, day_id, slot_index, duration_slots)
		 VALUES ($1, 'activity', 'Ancient Party', $2, 12, 6) RETURNING id`,
		oldConfigID, oldDayID,
	).Scan(&oldActivityID); err != nil {
		t.Fatalf("failed to insert old activity: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM sessions WHERE id = $1", oldActivityID)
	})

	kept := current.insertActivity(t, ctx, "Current Party "+newUUID(), "", slot(12), 6)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	var sawKept bool
	for _, a := range all {
		if a.ID == oldActivityID {
			t.Errorf("activity from an older conference (%s) was returned", oldActivityID)
		}
		if a.ID == kept {
			sawKept = true
		}
	}
	if !sawKept {
		t.Errorf("current conference activity %s was not returned", kept)
	}
}
