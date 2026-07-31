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

// Deliberately not behind the `integration` build tag, unlike the rest of this
// package's tests: the attendee search cursor is pure encode/decode with no
// database involved, so gating it would mean it never ran outside an
// integration environment.

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

const cursorTestUUID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

func TestAttendeeCursor_RoundTrips(t *testing.T) {
	want := time.Date(2026, 7, 31, 10, 15, 30, 123456789, time.UTC)

	gotTime, gotID, err := decodeAttendeeCursor(encodeAttendeeCursor(want, cursorTestUUID))
	if err != nil {
		t.Fatalf("decodeAttendeeCursor returned error: %v", err)
	}
	if !gotTime.Equal(want) {
		t.Errorf("time = %v, want %v", gotTime, want)
	}
	if gotID != cursorTestUUID {
		t.Errorf("id = %q, want %q", gotID, cursorTestUUID)
	}
}

func TestAttendeeCursor_NormalizesToUTC(t *testing.T) {
	// Colombo is +05:30, so a non-UTC instant round-tripping to the same moment
	// is what keeps paging independent of the connection's session time zone.
	colombo := time.FixedZone("+0530", int((5*time.Hour + 30*time.Minute).Seconds()))
	instant := time.Date(2026, 7, 31, 15, 45, 30, 0, colombo)

	gotTime, _, err := decodeAttendeeCursor(encodeAttendeeCursor(instant, cursorTestUUID))
	if err != nil {
		t.Fatalf("decodeAttendeeCursor returned error: %v", err)
	}
	if !gotTime.Equal(instant) {
		t.Errorf("time = %v, want the same instant as %v", gotTime, instant)
	}
	if gotTime.Location() != time.UTC {
		t.Errorf("location = %v, want UTC", gotTime.Location())
	}
}

func TestAttendeeCursor_MalformedInputIsErrInvalidCursor(t *testing.T) {
	// A cursor is opaque client-supplied input, so every malformed shape has to
	// come back as ErrInvalidCursor for the handler to answer 400 instead of 500.
	cases := map[string]string{
		"not base64":            "!!!not-base64!!!",
		"empty":                 "",
		"no separator":          base64.RawURLEncoding.EncodeToString([]byte("2026-07-31T10:00:00Z")),
		"empty id":              base64.RawURLEncoding.EncodeToString([]byte("2026-07-31T10:00:00Z|")),
		"unparseable time":      base64.RawURLEncoding.EncodeToString([]byte("not-a-time|" + cursorTestUUID)),
		"empty time":            base64.RawURLEncoding.EncodeToString([]byte("|" + cursorTestUUID)),
		"non-uuid id":           base64.RawURLEncoding.EncodeToString([]byte("2026-07-31T10:00:00Z|junk")),
		"id missing dashes":     base64.RawURLEncoding.EncodeToString([]byte("2026-07-31T10:00:00Z|3f2504e04f8911d39a0c0305e82c3301")),
		"id with trailing junk": base64.RawURLEncoding.EncodeToString([]byte("2026-07-31T10:00:00Z|" + cursorTestUUID + "x")),
		"sql injection in id":   base64.RawURLEncoding.EncodeToString([]byte("2026-07-31T10:00:00Z|' OR 1=1 --")),
	}

	for name, cursor := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeAttendeeCursor(cursor); !errors.Is(err, ErrInvalidCursor) {
				t.Errorf("decodeAttendeeCursor(%q) error = %v, want ErrInvalidCursor", cursor, err)
			}
		})
	}
}
