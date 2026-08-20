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

package models

import "time"

// Session represents a single conference agenda item, as returned by
// GET /sessions/:id. The old Ballerina schema stored startTime/endTime as
// strings and had youtubeLink/slidesLink/pdfLink/locationId/venueId/agendaId;
// the new marketingops.sessions table computes time from a day+slot instead,
// has no venue/agenda concept, and models links as two generic labeled
// slots. See .claude/PLAN.md for the full field-by-field mapping.
type Session struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Kind is one of session/keynote/break/activity. 'activity' was added by
	// upstream migration 021 for the full-width, room-less entries the agenda
	// already carried as fake breaks (Networking Event, Speaker Reception).
	// Title and Description are sanitized rich-text HTML, not plain text.
	// The content team writes both in a Quill editor and the event platform
	// sanitizes them on write against an allowlist matching that toolbar
	// (p, br, strong, em, u, s, ul, ol, li, blockquote, a[href], and span
	// carrying a ql-* class or a font-weight style); the public marketing
	// agenda renders the markup. Every other text field the writer stores --
	// a speaker's bio, a footnote, a track-section label -- is stripped to
	// plain text there, so these two and Activity.Description are the whole
	// set.
	//
	// This backend passes them through untouched rather than stripping or
	// re-sanitizing: the reader cannot tell markup the content team meant from
	// markup it did not, and stripping here would silently drop formatting the
	// marketing page depends on. The consequence is that a client renders these
	// as HTML and inherits the writer's allowlist as its own trust boundary.
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Category is the session's topic label -- the chip the agenda renders next
	// to a session (AgendaSession.tsx reads `session.category`).
	//
	// It used to come from sessions.category, a hand-maintained 6-value enum that
	// was NULL on every row and which upstream migration 024 dropped outright.
	// Its replacement is the resolved topic (track_topics.name via
	// sessions.topic_id, upstream 023), which is a real human label like
	// "API Management". The JSON key stays `category` so the client contract is
	// unchanged. Omitted when the session resolves to no topic -- which is always
	// the case against a database that predates upstream 023 (see
	// repository/schema.go).
	Category  string     `json:"category,omitempty"`
	StartTime *time.Time `json:"startTime,omitempty"`
	EndTime   *time.Time `json:"endTime,omitempty"`
	DayID     string     `json:"dayId,omitempty"`
	TrackID   string     `json:"trackId,omitempty"`
	// ColorToken names which colour the session is -- "red", "green", "main",
	// … -- without saying what that colour looks like, so the client owns the
	// values and can define a different one per light/dark appearance. It is
	// the only colour field the API publishes: the trackColor/roomColor hexes
	// it replaced were each one fixed value a theme had no licence to
	// reinterpret.
	//
	// The vocabulary is the microapp's own ROOM_COLOR_MAP keys, so the token
	// indexes that map directly and replaces the roomName string-sniffing it is
	// reached by today (FE.md 3.5). Resolved read-time as
	// COALESCE(rooms.color_token, tracks.color_token, 'main') -- the room owns
	// the colour, the track is the fallback (upstream migration 027). See
	// repository.ColorTokens for the closed set.
	//
	// Always present: a session with no token on either side gets
	// repository.ColorTokenDefault ("main", the app's existing grey), as does
	// every session against a database that predates upstream 027, so a client
	// never has to handle the field being absent.
	ColorToken string `json:"colorToken"`
	// TrackGroup is the label of the track_sections row the session sits in --
	// the heading the agenda groups a run of sessions under ("Case Studies",
	// "Integration", "Keynote Sessions"). Sections come in two upstream kinds,
	// track-scoped and day-scoped keynote ones, but the join is on
	// sessions.section_id so both resolve the same way. Omitted when the session
	// belongs to no section.
	TrackGroup    string `json:"trackGroup,omitempty"`
	SlotIndex     *int   `json:"slotIndex,omitempty"`
	DurationSlots int    `json:"durationSlots"`
	RoomID        string `json:"roomId,omitempty"`
	// RoomName is resolved from rooms.name so the client renders the room label
	// without a second lookup. Omitted when the session has no room.
	RoomName     string `json:"roomName,omitempty"`
	ArticleURL   string `json:"articleUrl,omitempty"`
	ArticleLabel string `json:"articleLabel,omitempty"`
	VideoURL     string `json:"videoUrl,omitempty"`
	VideoLabel   string `json:"videoLabel,omitempty"`
	// Speakers are embedded via a server-side join so the client renders a
	// session without a second fetch or a client-side session<->speaker join
	// (FE.md 3.2). IsModerator is filled from the owned presentation overlay in
	// Phase C; it is false until then.
	//
	// Only the single-session reads (GET /sessions/:id, GET /sessions/current)
	// populate this. The agenda endpoints leave it nil, and omitempty drops the
	// key entirely there: an agenda card renders no speaker, so shipping one
	// speaker array per session multiplied the day payload for nothing.
	Speakers []SessionSpeaker `json:"speakers,omitempty"`
}

// SessionSpeaker is a speaker embedded on a Session: the minimal shape a
// session card renders. IDs are strings everywhere (FE.md 3.2 number-vs-string
// bug). Name is decrypted server-side.
type SessionSpeaker struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Title is the speaker's job title from speakers.title, decrypted
	// server-side like Name. Named after the column rather than mirroring
	// Speaker.Description / SpeakerSession -- those publish this same column as
	// "description" only because the old Ballerina schema had a separate
	// description column and this one replaced it. Omitted when unset.
	Title string `json:"title,omitempty"`
	// Company is speakers.company, decrypted server-side. Nullable and almost
	// entirely unpopulated upstream (2 of 70 visible speakers as of 2026-08-03)
	// -- the company is usually only present as a suffix on Title. Omitted when
	// unset, so a client that wants a guaranteed affiliation still has to fall
	// back to Title.
	Company     string `json:"company,omitempty"`
	PhotoURL    string `json:"photoUrl,omitempty"`
	IsModerator bool   `json:"isModerator"`
}
