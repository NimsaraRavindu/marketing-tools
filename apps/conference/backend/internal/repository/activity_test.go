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

	"wso2-coin-backend/internal/models"
)

// activityFixture is a conference_config + conference_days pair with a far-future
// start_date, so it wins the "current conference = latest start_date" rule that
// ActivityRepo.List scopes on and the assertions below don't depend on whatever
// real data the shared database holds.
//
// Two days, because one activity opening on both is the case the whole
// con_activities/con_activity_hours split exists for and the one this endpoint
// has to flatten back into a sitting apiece.
type activityFixture struct {
	configID string
	dayIDs   []string
	dates    []string
}

// requireActivityTables skips rather than fails when upstream 029 has not been
// applied to the test database. Migrations are applied by hand across the two
// repos with no ordering guarantee, so a database sitting below 029 is a state
// this suite has to tolerate -- the endpoint's behaviour there is the
// degrade-to-empty path, which is asserted without a database in
// activity_window_test.go.
func requireActivityTables(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, table := range []string{"con_activities", "con_activity_hours"} {
		exists, err := tableExists(ctx, testDB, table)
		if err != nil {
			t.Fatalf("probing for %s: %v", table, err)
		}
		if !exists {
			t.Skipf("%s is absent; test database is below upstream 029", table)
		}
	}
}

// newActivityFixture leaves venue_name/venue_address unset, the state every
// conference_config row starts in and the one most of these tests want: an
// activity whose conference names no venue has no location to publish.
func newActivityFixture(t *testing.T, ctx context.Context) *activityFixture {
	t.Helper()
	return newActivityFixtureWithVenue(t, ctx, nil, nil)
}

// newActivityFixtureWithVenue is the same fixture with the upstream-018 venue
// columns populated. A nil argument inserts SQL NULL rather than an empty
// string, so the no-venue cases exercise the same NULL the degraded (below-018)
// SELECT substitutes for the column.
func newActivityFixtureWithVenue(t *testing.T, ctx context.Context, venueName, venueAddress *string) *activityFixture {
	t.Helper()
	requireActivityTables(t, ctx)

	dates := []string{"2099-11-04", "2099-11-05"}

	var configID string
	if err := testDB.QueryRow(ctx,
		`INSERT INTO conference_config (name, start_date, timezone, venue_name, venue_address)
		 VALUES ($1, $2, 'UTC', $3, $4) RETURNING id`,
		"TDD Activity Conference", dates[0], venueName, venueAddress,
	).Scan(&configID); err != nil {
		t.Fatalf("failed to insert conference_config: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM conference_config WHERE id = $1", configID)
	})

	// start_minute 540 (09:00) is the day's agenda origin. It is deliberately
	// non-zero and deliberately not what any assertion below expects to see
	// added to an activity's own minutes: an activity's window is wall-clock
	// from midnight, so a query that reused the session grid arithmetic would
	// shift every expectation by nine hours and be caught here.
	fixture := &activityFixture{configID: configID, dates: dates}
	for i, date := range dates {
		var dayID string
		if err := testDB.QueryRow(ctx,
			// end_minute is given explicitly rather than left to its column
			// default. Upstream 008 renamed start_hour/end_hour to
			// start_minute/end_minute and multiplied the stored data by 60, but
			// left the DEFAULTs at the old hour values (8 and 17), so an insert
			// that omits end_minute produces a day running 00:08 to 00:17 and
			// trips upstream 014's start_minute < end_minute check.
			"INSERT INTO conference_days (config_id, day_index, date, start_minute, end_minute) VALUES ($1, $2, $3, 540, 1080) RETURNING id",
			configID, i, date,
		).Scan(&dayID); err != nil {
			t.Fatalf("failed to insert conference_day: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testDB.Exec(context.Background(), "DELETE FROM conference_days WHERE id = $1", dayID)
		})
		fixture.dayIDs = append(fixture.dayIDs, dayID)
	}

	return fixture
}

// insertActivity seeds one con_activities row at the column's default position
// and removes it afterwards. The row on its own publishes nothing -- an activity
// with no opening hours has no sitting to return -- so every test pairs it with
// at least one insertHours.
//
// Leaving position to its DEFAULT 0 is deliberate for every test that does not
// care about it: that is the state of every row the content team entered before
// upstream 029 gave them a control for it, so these tests assert the ordering
// that data still gets.
func (f *activityFixture) insertActivity(t *testing.T, ctx context.Context, name, description string) string {
	t.Helper()
	var id string
	err := testDB.QueryRow(ctx,
		`INSERT INTO con_activities (config_id, name, description) VALUES ($1, $2, $3) RETURNING id`,
		f.configID, name, description,
	).Scan(&id)
	if err != nil {
		t.Fatalf("failed to insert con_activities row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM con_activities WHERE id = $1", id)
	})
	return id
}

// insertActivityAt is insertActivity with an explicit con_activities.position,
// for the tests that assert the content team's arrangement is what leads the
// response.
func (f *activityFixture) insertActivityAt(t *testing.T, ctx context.Context, name, description string, position int) string {
	t.Helper()
	var id string
	err := testDB.QueryRow(ctx,
		`INSERT INTO con_activities (config_id, name, description, position)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		f.configID, name, description, position,
	).Scan(&id)
	if err != nil {
		t.Fatalf("failed to insert con_activities row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM con_activities WHERE id = $1", id)
	})
	return id
}

// insertHours seeds one opening window on the fixture's day at dayIndex.
// startMinute/endMinute are wall-clock minutes from midnight. The returned id is
// what the endpoint publishes as Activity.ID.
func (f *activityFixture) insertHours(t *testing.T, ctx context.Context, activityID string, dayIndex, startMinute, endMinute int) string {
	t.Helper()
	var id string
	err := testDB.QueryRow(ctx,
		`INSERT INTO con_activity_hours (activity_id, day_id, start_minute, end_minute)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		activityID, f.dayIDs[dayIndex], startMinute, endMinute,
	).Scan(&id)
	if err != nil {
		t.Fatalf("failed to insert con_activity_hours row: %v", err)
	}
	// con_activities' ON DELETE CASCADE would take this too, but the fixture's
	// cleanups run in reverse order and a test may delete hours independently.
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM con_activity_hours WHERE id = $1", id)
	})
	return id
}

// The times come straight off the day's date plus the two wall-clock offsets:
// con_activity_hours.start_minute is minutes from midnight, so the day's own
// start_minute (540 in the fixture) must not be added and there is no slot unit
// to multiply by. 540 -> 09:00, not 18:00.
func TestActivityRepo_ListComputesTimesFromWallClockMinutes(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, time.UTC)

	name := "O2 Bar " + newUUID()
	activityID := fixture.insertActivity(t, ctx, name, "Open bar in the foyer")
	hoursID := fixture.insertHours(t, ctx, activityID, 0, 540, 840)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	got := findActivity(t, all, hoursID)
	if got.Name != name {
		t.Errorf("Name = %q, want %q", got.Name, name)
	}
	if got.Description != "Open bar in the foyer" {
		t.Errorf("Description = %q, want %q", got.Description, "Open bar in the foyer")
	}
	wantStart := time.Date(2099, 11, 4, 9, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2099, 11, 4, 14, 0, 0, 0, time.UTC)
	if !got.StartTime.Equal(wantStart) {
		t.Errorf("StartTime = %s, want %s -- the day's start_minute must not be added again", got.StartTime, wantStart)
	}
	if !got.EndTime.Equal(wantEnd) {
		t.Errorf("EndTime = %s, want %s", got.EndTime, wantEnd)
	}
	// This fixture's conference names no venue, and Name is not omitempty, so
	// the location must be omitted rather than sent as an object of empty
	// strings.
	if got.Location != nil {
		t.Errorf("Location = %+v, want nil", got.Location)
	}
}

// One entry per con_activity_hours row, never one per activity: the O2 Bar open
// on two days is two entries sharing a name, which is the flat array the client
// reduces into a single card with several times.
func TestActivityRepo_ListReturnsOneEntryPerOpeningWindow(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, time.UTC)

	name := "O2 Bar " + newUUID()
	activityID := fixture.insertActivity(t, ctx, name, "Open bar in the foyer")
	day1 := fixture.insertHours(t, ctx, activityID, 0, 540, 840)
	day2 := fixture.insertHours(t, ctx, activityID, 1, 660, 1080)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	var ids []string
	for _, a := range all {
		if a.Name == name {
			ids = append(ids, a.ID)
		}
		// The activity's own id must never surface: it repeats once per
		// sitting, so a client keying on it would collapse the two windows.
		if a.ID == activityID {
			t.Errorf("con_activities id %s was published as Activity.ID; it must be the hours row's id", activityID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("got %d sittings of %q, want 2 -- one per opening window", len(ids), name)
	}
	if ids[0] != day1 || ids[1] != day2 {
		t.Errorf("sitting ids = %v, want the two con_activity_hours ids [%s %s]", ids, day1, day2)
	}

	// Both sittings carry the same name and description; only the window
	// differs, which is exactly what lets the client group them.
	first := findActivity(t, all, day1)
	second := findActivity(t, all, day2)
	if first.Description != second.Description {
		t.Errorf("sittings disagree on description: %q vs %q", first.Description, second.Description)
	}
	wantSecondStart := time.Date(2099, 11, 5, 11, 0, 0, 0, time.UTC)
	if !second.StartTime.Equal(wantSecondStart) {
		t.Errorf("second sitting StartTime = %s, want %s", second.StartTime, wantSecondStart)
	}
}

// Two windows on one day is a legal shape upstream -- an amenity that shuts for
// lunch -- and each is its own entry, since forbidding it would have pushed the
// content team into inventing a second activity.
func TestActivityRepo_ListReturnsBothWindowsOfASplitDay(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, time.UTC)

	name := "Registration Desk " + newUUID()
	activityID := fixture.insertActivity(t, ctx, name, "")
	morning := fixture.insertHours(t, ctx, activityID, 0, 480, 720)
	afternoon := fixture.insertHours(t, ctx, activityID, 0, 780, 1020)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	var ids []string
	for _, a := range all {
		if a.Name == name {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) != 2 || ids[0] != morning || ids[1] != afternoon {
		t.Errorf("sitting ids = %v, want [%s %s] -- both windows of the same day, in order", ids, morning, afternoon)
	}
}

// An activity with no opening hours contributes nothing. It is a legitimate row
// upstream (the content team creates the amenity before scheduling it), and it
// is the successor to the old "unscheduled activity" case: a card with no time
// is worse than no card.
func TestActivityRepo_ListSkipsActivitiesWithNoHours(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, time.UTC)

	name := "Unscheduled Booth " + newUUID()
	fixture.insertActivity(t, ctx, name, "")

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	for _, a := range all {
		if a.Name == name {
			t.Fatalf("activity %q with no opening hours was returned as %+v", name, a)
		}
	}
}

// Name decides between activities that share a position, which is every
// activity in a database whose rows predate upstream 029's position control:
// the column defaults to 0, so they all tie and the name is what is left. Within
// an activity, day then start_minute orders its sittings.
//
// An activity-level key has to lead either way, because the client groups by
// name and its reduce-into-a-map preserves receive order. Inserted in the
// reverse of the expected order so a missing ORDER BY cannot pass by insertion
// luck.
func TestActivityRepo_ListOrdersByNameThenDayThenStart(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, time.UTC)

	suffix := newUUID()
	// A shared suffix keeps these three names adjacent in the global ordering
	// while still sorting Bar < Booth < Desk between themselves.
	barName := "ZZ Bar " + suffix
	boothName := "ZZ Booth " + suffix
	deskName := "ZZ Desk " + suffix

	desk := fixture.insertActivity(t, ctx, deskName, "")
	deskDay0 := fixture.insertHours(t, ctx, desk, 0, 480, 600)

	booth := fixture.insertActivity(t, ctx, boothName, "")
	boothDay1 := fixture.insertHours(t, ctx, booth, 1, 600, 720)

	bar := fixture.insertActivity(t, ctx, barName, "")
	barDay1 := fixture.insertHours(t, ctx, bar, 1, 600, 720)
	barDay0Late := fixture.insertHours(t, ctx, bar, 0, 900, 1000)
	barDay0Early := fixture.insertHours(t, ctx, bar, 0, 540, 600)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	var order []string
	for _, a := range all {
		switch a.Name {
		case barName, boothName, deskName:
			order = append(order, a.ID)
		}
	}

	want := []string{barDay0Early, barDay0Late, barDay1, boothDay1, deskDay0}
	if len(order) != len(want) {
		t.Fatalf("got %d entries, want %d", len(order), len(want))
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// position is the content team's arrangement of the General page, and it beats
// the name. Upstream 029 added the column, indexed (config_id, position), and
// the admin Activities page edits it; if this endpoint sorted by name alone the
// team could reorder the cards and see nothing change in the microapp.
//
// The names here sort in the exact opposite direction to the positions, so the
// test cannot pass on a name-ordered query.
func TestActivityRepo_ListOrdersByPositionBeforeName(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, time.UTC)

	suffix := newUUID()
	firstName := "ZZ C-Bar " + suffix
	secondName := "ZZ B-Booth " + suffix
	thirdName := "ZZ A-Desk " + suffix

	first := fixture.insertActivityAt(t, ctx, firstName, "", 0)
	firstSitting := fixture.insertHours(t, ctx, first, 0, 540, 600)

	second := fixture.insertActivityAt(t, ctx, secondName, "", 1)
	secondSitting := fixture.insertHours(t, ctx, second, 0, 540, 600)

	third := fixture.insertActivityAt(t, ctx, thirdName, "", 2)
	thirdSitting := fixture.insertHours(t, ctx, third, 0, 540, 600)

	assertActivityOrder(t, repo, ctx,
		map[string]bool{firstName: true, secondName: true, thirdName: true},
		[]string{firstSitting, secondSitting, thirdSitting},
	)
}

// Every sitting of one amenity stays contiguous when positions are what order
// the list. The client reduces the flat array into a map keyed by name and its
// group-by preserves receive order, so two amenities whose sittings interleaved
// would render as split cards. position is per-activity, which is what makes
// this hold -- the sittings of one activity all carry the same one.
func TestActivityRepo_ListKeepsAnActivitysSittingsContiguousUnderPosition(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, time.UTC)

	suffix := newUUID()
	barName := "ZZ Bar " + suffix
	deskName := "ZZ Desk " + suffix

	bar := fixture.insertActivityAt(t, ctx, barName, "", 0)
	barDay0 := fixture.insertHours(t, ctx, bar, 0, 540, 600)
	barDay1 := fixture.insertHours(t, ctx, bar, 1, 600, 720)

	desk := fixture.insertActivityAt(t, ctx, deskName, "", 1)
	// Straddles the bar's two sittings in time. Ordering on the window rather
	// than on the activity would sort this between them.
	deskDay0 := fixture.insertHours(t, ctx, desk, 0, 570, 630)

	assertActivityOrder(t, repo, ctx,
		map[string]bool{barName: true, deskName: true},
		[]string{barDay0, barDay1, deskDay0},
	)
}

// Names are compared case-insensitively, matching upstream's own lower(name)
// ordering. Without it Postgres' default collation puts an amenity the content
// team typed in lower case in a different part of the list from its neighbours
// -- "o2 Bar" landing after "Zone" rather than next to "O2 Lounge".
func TestActivityRepo_ListOrdersNamesCaseInsensitively(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, time.UTC)

	suffix := newUUID()
	lowerName := "ZZ b-bar " + suffix
	upperName := "ZZ C-Desk " + suffix

	lower := fixture.insertActivity(t, ctx, lowerName, "")
	lowerSitting := fixture.insertHours(t, ctx, lower, 0, 540, 600)

	upper := fixture.insertActivity(t, ctx, upperName, "")
	upperSitting := fixture.insertHours(t, ctx, upper, 0, 540, 600)

	assertActivityOrder(t, repo, ctx,
		map[string]bool{lowerName: true, upperName: true},
		[]string{lowerSitting, upperSitting},
	)
}

// Two windows that open at the same minute on the same day are ordered by the
// window's id. con_activity_hours has no unique key forbidding the pair -- an
// amenity with two doors opening together is as legal as one that shuts for
// lunch -- so without a final tiebreak their relative order is whatever the
// query plan returns, and the list could reshuffle between two identical
// requests. The client keys React nodes off these ids.
func TestActivityRepo_ListBreaksTiesByWindowID(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, time.UTC)

	name := "ZZ Twin Windows " + newUUID()
	activity := fixture.insertActivity(t, ctx, name, "")
	one := fixture.insertHours(t, ctx, activity, 0, 540, 600)
	two := fixture.insertHours(t, ctx, activity, 0, 540, 660)

	// Ordered by id, not by insertion: the ids are random UUIDs, so which of the
	// two leads is decided here rather than assumed.
	want := []string{one, two}
	if two < one {
		want = []string{two, one}
	}

	assertActivityOrder(t, repo, ctx, map[string]bool{name: true}, want)
}

// assertActivityOrder lists the activities, keeps only the sittings whose names
// the test seeded, and compares their ids to want in order.
//
// Filtering by name is what lets these tests run against the shared database:
// the fixture's conference wins the "latest start_date" scope, but the assertion
// still has to ignore anything else that conference happens to hold.
func assertActivityOrder(t *testing.T, repo *ActivityRepo, ctx context.Context, names map[string]bool, want []string) {
	t.Helper()

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	var got []string
	for _, a := range all {
		if names[a.Name] {
			got = append(got, a.ID)
		}
	}

	if len(got) != len(want) {
		t.Fatalf("got %d sittings %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// Activities belonging to an older conference must not leak into the current
// event's list. con_activities is config-scoped directly, so this is one
// predicate on the parent rather than a reach through the day.
func TestActivityRepo_ListScopesToCurrentConference(t *testing.T) {
	ctx := context.Background()
	current := newActivityFixture(t, ctx)
	repo := NewActivityRepo(testDB, time.UTC)

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
		// end_minute explicit for the same reason as the fixture above: the
		// column default is a leftover hour value and trips upstream 014.
		"INSERT INTO conference_days (config_id, day_index, date, start_minute, end_minute) VALUES ($1, 0, '2000-01-01', 540, 1080) RETURNING id",
		oldConfigID,
	).Scan(&oldDayID); err != nil {
		t.Fatalf("failed to insert old conference_day: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM conference_days WHERE id = $1", oldDayID)
	})

	var oldActivityID, oldHoursID string
	if err := testDB.QueryRow(ctx,
		"INSERT INTO con_activities (config_id, name) VALUES ($1, 'Ancient Bar') RETURNING id",
		oldConfigID,
	).Scan(&oldActivityID); err != nil {
		t.Fatalf("failed to insert old con_activities row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM con_activities WHERE id = $1", oldActivityID)
	})
	if err := testDB.QueryRow(ctx,
		"INSERT INTO con_activity_hours (activity_id, day_id, start_minute, end_minute) VALUES ($1, $2, 540, 840) RETURNING id",
		oldActivityID, oldDayID,
	).Scan(&oldHoursID); err != nil {
		t.Fatalf("failed to insert old con_activity_hours row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM con_activity_hours WHERE id = $1", oldHoursID)
	})

	keptActivity := current.insertActivity(t, ctx, "Current Bar "+newUUID(), "")
	kept := current.insertHours(t, ctx, keptActivity, 0, 540, 840)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	var sawKept bool
	for _, a := range all {
		if a.ID == oldHoursID {
			t.Errorf("activity sitting from an older conference (%s) was returned", oldHoursID)
		}
		if a.ID == kept {
			sawKept = true
		}
	}
	if !sawKept {
		t.Errorf("current conference activity sitting %s was not returned", kept)
	}
}

// conference_config.timezone is the source of truth for anchoring, so the
// instants carry the venue's real offset rather than a fake Z. The repo's own
// fallback location is set to something else entirely, so a query that ignored
// the column would land four and a half hours out.
func TestActivityRepo_ListAnchorsInTheConferenceTimezone(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)
	if _, err := testDB.Exec(ctx,
		"UPDATE conference_config SET timezone = 'Asia/Colombo' WHERE id = $1", fixture.configID,
	); err != nil {
		t.Fatalf("failed to set the conference timezone: %v", err)
	}

	repo := NewActivityRepo(testDB, time.UTC)

	activityID := fixture.insertActivity(t, ctx, "Colombo Bar "+newUUID(), "")
	hoursID := fixture.insertHours(t, ctx, activityID, 0, 540, 840)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	got := findActivity(t, all, hoursID)
	colombo, err := time.LoadLocation("Asia/Colombo")
	if err != nil {
		t.Fatalf("loading Asia/Colombo: %v", err)
	}
	wantStart := time.Date(2099, 11, 4, 9, 0, 0, 0, colombo)
	if !got.StartTime.Equal(wantStart) {
		t.Errorf("StartTime = %s, want %s (09:00 +05:30, not 09:00Z)", got.StartTime, wantStart)
	}
}

// Every activity carries the conference's own venue: con_activities models no
// location, and conference_config.venue_name/venue_address (upstream 018) is the
// granularity that exists.
func TestActivityRepo_ListPublishesTheConferenceVenue(t *testing.T) {
	ctx := context.Background()
	name, address := "BMICH", "Bauddhaloka Mawatha, Colombo 07"
	fixture := newActivityFixtureWithVenue(t, ctx, &name, &address)
	repo := NewActivityRepo(testDB, time.UTC)

	activityID := fixture.insertActivity(t, ctx, "Welcome Desk "+newUUID(), "")
	hoursID := fixture.insertHours(t, ctx, activityID, 0, 540, 840)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	got := findActivity(t, all, hoursID)
	if got.Location == nil {
		t.Fatalf("Location = nil, want the conference venue")
	}
	if got.Location.Name != name {
		t.Errorf("Location.Name = %q, want %q", got.Location.Name, name)
	}
	if got.Location.Address != address {
		t.Errorf("Location.Address = %q, want %q", got.Location.Address, address)
	}
	// Nothing upstream models a floor plan, so this stays unset and the key
	// stays out of the payload.
	if got.Location.FloorPlanURL != "" {
		t.Errorf("Location.FloorPlanURL = %q, want empty", got.Location.FloorPlanURL)
	}
}

// A venue named but not addressed is a normal half-filled row, and the address
// is omitted rather than emitted as "" (openapi.yaml's absence convention).
func TestActivityRepo_ListOmitsAddressWhenTheVenueHasNone(t *testing.T) {
	ctx := context.Background()
	name := "BMICH"
	fixture := newActivityFixtureWithVenue(t, ctx, &name, nil)
	repo := NewActivityRepo(testDB, time.UTC)

	activityID := fixture.insertActivity(t, ctx, "Coffee Cart "+newUUID(), "")
	hoursID := fixture.insertHours(t, ctx, activityID, 0, 600, 660)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	got := findActivity(t, all, hoursID)
	if got.Location == nil {
		t.Fatalf("Location = nil, want a location -- the venue is named")
	}
	if got.Location.Address != "" {
		t.Errorf("Location.Address = %q, want empty so the key is omitted", got.Location.Address)
	}
}

// An address with no name publishes nothing: Name is not omitempty, so the
// object would serialize as {"name": ""}, and an address alone has nothing to
// label it in the UI anyway.
func TestActivityRepo_ListOmitsLocationWhenTheVenueIsUnnamed(t *testing.T) {
	ctx := context.Background()
	address := "Bauddhaloka Mawatha, Colombo 07"
	fixture := newActivityFixtureWithVenue(t, ctx, nil, &address)
	repo := NewActivityRepo(testDB, time.UTC)

	activityID := fixture.insertActivity(t, ctx, "Unnamed Venue Booth "+newUUID(), "")
	hoursID := fixture.insertHours(t, ctx, activityID, 0, 600, 660)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	if got := findActivity(t, all, hoursID); got.Location != nil {
		t.Errorf("Location = %+v, want nil -- the venue has no name", got.Location)
	}
}

// Against a database below upstream 018 the venue columns are not there at all,
// and naming one would 500 the whole endpoint rather than cost it a nested
// object. Forcing the capability to absent -- with the venue actually populated,
// so a query that ignored the capability would visibly return it -- proves the
// degraded SELECT is valid SQL and lands on the no-location path.
func TestActivityRepo_ListDegradesToNoLocationBelowUpstream018(t *testing.T) {
	ctx := context.Background()
	name, address := "BMICH", "Bauddhaloka Mawatha, Colombo 07"
	fixture := newActivityFixtureWithVenue(t, ctx, &name, &address)

	repo := NewActivityRepo(testDB, time.UTC)
	repo.caps.venueResolved = true
	repo.caps.hasVenueName = false
	repo.caps.hasVenueAddress = false

	activityID := fixture.insertActivity(t, ctx, "Pre-018 Bar "+newUUID(), "")
	hoursID := fixture.insertHours(t, ctx, activityID, 0, 660, 900)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error against the degraded shape: %v", err)
	}

	if got := findActivity(t, all, hoursID); got.Location != nil {
		t.Errorf("Location = %+v, want nil -- the columns are treated as absent", got.Location)
	}
}

// Below upstream 029 the tables do not exist and the read is skipped entirely.
// Forcing the capability to absent against a database that *does* have the
// tables and rows in them proves the skip is what empties the result, not an
// empty database -- and that it is an empty list rather than the 500 a query
// naming a missing table would produce.
func TestActivityRepo_ListReturnsNothingBelowUpstream029(t *testing.T) {
	ctx := context.Background()
	fixture := newActivityFixture(t, ctx)

	repo := NewActivityRepo(testDB, time.UTC)
	repo.caps.activityTablesResolved = true
	repo.caps.hasActivityTables = false

	activityID := fixture.insertActivity(t, ctx, "Invisible Bar "+newUUID(), "")
	hoursID := fixture.insertHours(t, ctx, activityID, 0, 540, 840)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error with the tables treated as absent: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("List returned %d activities, want none -- the read must be skipped entirely", len(all))
	}

	// And the same repo serves the rows once the capability says the tables are
	// there, so the emptiness above is the capability's doing.
	repo.caps.hasActivityTables = true
	all, err = repo.List(ctx)
	if err != nil {
		t.Fatalf("List returned error with the tables present: %v", err)
	}
	findActivity(t, all, hoursID)
}

// findActivity fails the test if the activity is missing, so each assertion
// above is about the row's contents rather than about it having been returned.
func findActivity(t *testing.T, all []models.Activity, id string) models.Activity {
	t.Helper()
	for _, a := range all {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("activity sitting %s not returned by List", id)
	return models.Activity{}
}
