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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"wso2-coin-backend/internal/crypto"
	"wso2-coin-backend/internal/models"
)

// speakerTestKey is a throwaway 32-byte AES-256 key used only by this test
// file to encrypt fixture rows; it has no relationship to any real
// PII_ENCRYPTION_KEY.
var speakerTestKey = mustDecodeTestKey("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")

func mustDecodeTestKey(b64 string) []byte {
	k, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		panic(err)
	}
	return k
}

func mustEncrypt(t *testing.T, plaintext string) string {
	t.Helper()
	ct, err := crypto.EncryptPII(plaintext, speakerTestKey)
	if err != nil {
		t.Fatalf("failed to encrypt fixture value %q: %v", plaintext, err)
	}
	return ct
}

// speakerFixture inserts an isolated speaker row (and, on demand, a
// conference_config/session/session_speakers chain) for a single test, and
// registers cleanup that deletes only those specific rows.
type speakerFixture struct {
	speakerID string
}

func newSpeakerFixture(t *testing.T, ctx context.Context, name, title, bio, photoURL string, visible bool) *speakerFixture {
	t.Helper()

	var speakerID string
	err := testDB.QueryRow(ctx,
		`INSERT INTO speakers (name, title, bio, photo_url, visible)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		mustEncrypt(t, name), mustEncrypt(t, title), mustEncrypt(t, bio), photoURL, visible,
	).Scan(&speakerID)
	if err != nil {
		t.Fatalf("failed to insert test speaker: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM speakers WHERE id = $1", speakerID)
	})

	return &speakerFixture{speakerID: speakerID}
}

// Room name and track colour the fixture session is placed in, asserted by
// TestSpeakerRepo_GetSpeakerSummary_ScopedByEventEmbedsResolvedSessions.
const (
	testSpeakerRoomName   = "TDD Speaker Test Room"
	testSpeakerTrackColor = "#123456"
	testSpeakerRoomColor  = "#abcdef"
)

// attachToSession creates a minimal conference_config + room + track +
// session and links this fixture's speaker to it via session_speakers,
// returning (sessionID, configID) for assertions. The session is left
// unscheduled (no day_id/slot_index) -- the track needs a day, but the
// session doesn't sit on one.
//
// The conference is dated far in the future so it wins the "current
// conference = latest start_date" rule GetSpeaker scopes its embedded
// sessions by. Each fixture config is dropped in cleanup, so only the running
// test's config holds that position.
func (f *speakerFixture) attachToSession(t *testing.T, ctx context.Context) (sessionID, configID string) {
	t.Helper()

	err := testDB.QueryRow(ctx,
		"INSERT INTO conference_config (name, start_date) VALUES ($1, $2) RETURNING id",
		"TDD Speaker Test Conference", "2099-08-01",
	).Scan(&configID)
	if err != nil {
		t.Fatalf("failed to insert test conference_config: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM conference_config WHERE id = $1", configID)
	})

	var roomID string
	err = testDB.QueryRow(ctx,
		"INSERT INTO rooms (config_id, name) VALUES ($1, $2) RETURNING id",
		configID, testSpeakerRoomName,
	).Scan(&roomID)
	if err != nil {
		t.Fatalf("failed to insert test room: %v", err)
	}

	insertRoomColor(t, ctx, roomID, testSpeakerRoomColor)

	var dayID string
	err = testDB.QueryRow(ctx,
		"INSERT INTO conference_days (config_id, day_index, date) VALUES ($1, 0, $2) RETURNING id",
		configID, "2026-08-01",
	).Scan(&dayID)
	if err != nil {
		t.Fatalf("failed to insert test conference_day: %v", err)
	}

	var trackID string
	err = testDB.QueryRow(ctx,
		"INSERT INTO tracks (day_id, room_id, color) VALUES ($1, $2, $3) RETURNING id",
		dayID, roomID, testSpeakerTrackColor,
	).Scan(&trackID)
	if err != nil {
		t.Fatalf("failed to insert test track: %v", err)
	}

	err = testDB.QueryRow(ctx,
		`INSERT INTO sessions (config_id, kind, title, room_id, track_id)
		 VALUES ($1, 'session', 'TDD Speaker Test Session', $2, $3) RETURNING id`,
		configID, roomID, trackID,
	).Scan(&sessionID)
	if err != nil {
		t.Fatalf("failed to insert test session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM sessions WHERE id = $1", sessionID)
	})

	_, err = testDB.Exec(ctx,
		"INSERT INTO session_speakers (session_id, speaker_id) VALUES ($1, $2)",
		sessionID, f.speakerID,
	)
	if err != nil {
		t.Fatalf("failed to insert test session_speakers row: %v", err)
	}

	return sessionID, configID
}

func TestSpeakerRepo_GetSpeaker_DecryptsFields(t *testing.T) {
	ctx := context.Background()
	repo := NewSpeakerRepo(testDB, speakerTestKey, 5, time.UTC)

	fixture := newSpeakerFixture(t, ctx, "John Doe", "Principal Engineer", "Works on integration.", "https://example.com/john.webp", true)

	speaker, err := repo.GetSpeaker(ctx, fixture.speakerID)
	if err != nil {
		t.Fatalf("GetSpeaker returned error: %v", err)
	}
	if speaker.Name != "John Doe" {
		t.Errorf("Name = %q, want %q", speaker.Name, "John Doe")
	}
	if speaker.Description != "Principal Engineer" {
		t.Errorf("Description = %q, want %q (mapped from title)", speaker.Description, "Principal Engineer")
	}
	if speaker.Bio != "Works on integration." {
		t.Errorf("Bio = %q, want %q", speaker.Bio, "Works on integration.")
	}
	if speaker.PhotoURL != "https://example.com/john.webp" {
		t.Errorf("PhotoURL = %q, want %q", speaker.PhotoURL, "https://example.com/john.webp")
	}
}

func TestSpeakerRepo_GetSpeaker_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := NewSpeakerRepo(testDB, speakerTestKey, 5, time.UTC)

	_, err := repo.GetSpeaker(ctx, newUUID())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSpeaker error = %v, want ErrNotFound", err)
	}
}

func TestSpeakerRepo_GetSpeaker_NotFoundWhenNotVisible(t *testing.T) {
	// visible is a public/private access boundary, not just a list-view
	// filter: GetSpeaker must not let a hidden speaker's id bypass the same
	// check GetSpeakerSummary enforces, since this route is unauthenticated.
	ctx := context.Background()
	repo := NewSpeakerRepo(testDB, speakerTestKey, 5, time.UTC)

	fixture := newSpeakerFixture(t, ctx, "Hidden Speaker", "", "", "", false)

	_, err := repo.GetSpeaker(ctx, fixture.speakerID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSpeaker error = %v, want ErrNotFound", err)
	}
}

func TestSpeakerRepo_GetSpeakerSummary_FiltersToVisibleOnly(t *testing.T) {
	ctx := context.Background()
	repo := NewSpeakerRepo(testDB, speakerTestKey, 5, time.UTC)

	visible := newSpeakerFixture(t, ctx, "Visible Speaker", "", "", "", true)
	hidden := newSpeakerFixture(t, ctx, "Hidden Speaker", "", "", "", false)

	summaries, err := repo.GetSpeakerSummary(ctx, models.SpeakerFilter{})
	if err != nil {
		t.Fatalf("GetSpeakerSummary returned error: %v", err)
	}

	ids := make(map[string]bool)
	for _, s := range summaries {
		ids[s.ID] = true
	}
	if !ids[visible.speakerID] {
		t.Errorf("expected visible speaker %s in summary", visible.speakerID)
	}
	if ids[hidden.speakerID] {
		t.Errorf("expected hidden (visible=false) speaker %s to be excluded from summary", hidden.speakerID)
	}
}

// Scoping by eventId returns only the speakers in that conference. Scoping
// also keeps this test off the shared DB's real (differently-encrypted)
// speaker rows.
func TestSpeakerRepo_GetSpeakerSummary_ScopedByEvent(t *testing.T) {
	ctx := context.Background()
	repo := NewSpeakerRepo(testDB, speakerTestKey, 5, time.UTC)

	fixture := newSpeakerFixture(t, ctx, "Speaker With Session", "", "", "", true)
	_, configID := fixture.attachToSession(t, ctx)

	summaries, err := repo.GetSpeakerSummary(ctx, models.SpeakerFilter{EventID: configID})
	if err != nil {
		t.Fatalf("GetSpeakerSummary returned error: %v", err)
	}

	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want exactly 1 (scoped to this event)", len(summaries))
	}
	if summaries[0].ID != fixture.speakerID {
		t.Fatalf("speaker id = %q, want %q", summaries[0].ID, fixture.speakerID)
	}
}

// The speaker's sessions belong to the detail endpoint, resolved to
// {id, title, room, colours} objects the client renders as-is.
func TestSpeakerRepo_GetSpeaker_EmbedsResolvedSessions(t *testing.T) {
	ctx := context.Background()
	repo := NewSpeakerRepo(testDB, speakerTestKey, 5, time.UTC)

	fixture := newSpeakerFixture(t, ctx, "Speaker With Session", "", "", "", true)
	sessionID, _ := fixture.attachToSession(t, ctx)

	speaker, err := repo.GetSpeaker(ctx, fixture.speakerID)
	if err != nil {
		t.Fatalf("GetSpeaker returned error: %v", err)
	}

	if len(speaker.Sessions) != 1 {
		t.Fatalf("expected 1 embedded session, got %d", len(speaker.Sessions))
	}
	sess := speaker.Sessions[0]
	if sess.ID != sessionID {
		t.Errorf("session id = %q, want %q", sess.ID, sessionID)
	}
	if sess.Title != "TDD Speaker Test Session" {
		t.Errorf("session title = %q, want %q", sess.Title, "TDD Speaker Test Session")
	}
	if sess.RoomName != testSpeakerRoomName {
		t.Errorf("session roomName = %q, want %q", sess.RoomName, testSpeakerRoomName)
	}
	if sess.TrackColor != testSpeakerTrackColor {
		t.Errorf("session trackColor = %q, want %q", sess.TrackColor, testSpeakerTrackColor)
	}
	if sess.RoomColor != testSpeakerRoomColor {
		t.Errorf("session roomColor = %q, want %q", sess.RoomColor, testSpeakerRoomColor)
	}
}

// The client used to get current-conference scoping for free, because it read
// a speaker's sessions off an event-scoped list. The detail endpoint has to
// enforce it itself, or a returning speaker's profile starts listing talks
// from conferences that already happened.
func TestSpeakerRepo_GetSpeaker_ExcludesSessionsFromOtherConferences(t *testing.T) {
	ctx := context.Background()
	repo := NewSpeakerRepo(testDB, speakerTestKey, 5, time.UTC)

	fixture := newSpeakerFixture(t, ctx, "Returning Speaker", "", "", "", true)
	currentSessionID, _ := fixture.attachToSession(t, ctx)

	var pastConfigID string
	if err := testDB.QueryRow(ctx,
		"INSERT INTO conference_config (name, start_date) VALUES ($1, $2) RETURNING id",
		"TDD Past Conference", "2019-01-01",
	).Scan(&pastConfigID); err != nil {
		t.Fatalf("failed to insert past conference_config: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM conference_config WHERE id = $1", pastConfigID)
	})

	var pastSessionID string
	if err := testDB.QueryRow(ctx,
		`INSERT INTO sessions (config_id, kind, title) VALUES ($1, 'session', 'Talk From 2019') RETURNING id`,
		pastConfigID,
	).Scan(&pastSessionID); err != nil {
		t.Fatalf("failed to insert past session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM sessions WHERE id = $1", pastSessionID)
	})

	if _, err := testDB.Exec(ctx,
		"INSERT INTO session_speakers (session_id, speaker_id) VALUES ($1, $2)",
		pastSessionID, fixture.speakerID,
	); err != nil {
		t.Fatalf("failed to link speaker to past session: %v", err)
	}

	speaker, err := repo.GetSpeaker(ctx, fixture.speakerID)
	if err != nil {
		t.Fatalf("GetSpeaker returned error: %v", err)
	}
	if len(speaker.Sessions) != 1 || speaker.Sessions[0].ID != currentSessionID {
		t.Fatalf("sessions = %+v, want only the current conference's session %s", speaker.Sessions, currentSessionID)
	}
}

// A session with no room and no track is a real state in this data (breaks,
// and the Blue Room keynotes that carry no track_id): the fields must come
// back empty and serialize away entirely, not as "".
func TestSpeakerRepo_GetSpeaker_OmitsRoomAndColourWhenSessionHasNeither(t *testing.T) {
	ctx := context.Background()
	repo := NewSpeakerRepo(testDB, speakerTestKey, 5, time.UTC)

	fixture := newSpeakerFixture(t, ctx, "Speaker Without Room", "", "", "", true)
	_, configID := fixture.attachToSession(t, ctx)

	if _, err := testDB.Exec(ctx,
		"UPDATE sessions SET room_id = NULL, track_id = NULL WHERE config_id = $1", configID,
	); err != nil {
		t.Fatalf("failed to clear room_id/track_id: %v", err)
	}

	speaker, err := repo.GetSpeaker(ctx, fixture.speakerID)
	if err != nil {
		t.Fatalf("GetSpeaker returned error: %v", err)
	}
	if len(speaker.Sessions) != 1 {
		t.Fatalf("want exactly 1 session, got %+v", speaker.Sessions)
	}

	sess := speaker.Sessions[0]
	if sess.RoomName != "" {
		t.Errorf("roomName = %q, want empty for a session with no room", sess.RoomName)
	}
	if sess.TrackColor != "" {
		t.Errorf("trackColor = %q, want empty for a session with no track", sess.TrackColor)
	}
	if sess.RoomColor != "" {
		t.Errorf("roomColor = %q, want empty for a session with no room", sess.RoomColor)
	}

	encoded, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshalling session: %v", err)
	}
	for _, key := range []string{"roomName", "trackColor", "roomColor"} {
		if bytes.Contains(encoded, []byte(key)) {
			t.Errorf("%s should be omitted from JSON when empty, got %s", key, encoded)
		}
	}
}

// The ?q= name search matches on the decrypted name (name is encrypted at
// rest, so it can't be an SQL ILIKE). Scoped by eventId to stay off real rows.
func TestSpeakerRepo_GetSpeakerSummary_QueryFiltersByDecryptedName(t *testing.T) {
	ctx := context.Background()
	repo := NewSpeakerRepo(testDB, speakerTestKey, 5, time.UTC)

	fixture := newSpeakerFixture(t, ctx, "Ada Lovelace", "", "", "", true)
	_, configID := fixture.attachToSession(t, ctx)

	// A matching query returns the speaker.
	got, err := repo.GetSpeakerSummary(ctx, models.SpeakerFilter{EventID: configID, Query: "lovelace"})
	if err != nil {
		t.Fatalf("GetSpeakerSummary returned error: %v", err)
	}
	if len(got) != 1 || got[0].ID != fixture.speakerID {
		t.Errorf("query 'lovelace' returned %+v, want the one matching speaker", got)
	}

	// A non-matching query excludes it.
	got, err = repo.GetSpeakerSummary(ctx, models.SpeakerFilter{EventID: configID, Query: "zzz-no-match"})
	if err != nil {
		t.Fatalf("GetSpeakerSummary returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("non-matching query returned %+v, want empty", got)
	}
}
