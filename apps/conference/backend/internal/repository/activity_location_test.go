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

// Untagged, like attendee_cursor_test.go and schema_test.go: what an activity's
// location does with a given pair of venue columns is decidable without a
// database, and it is the part of ActivityRepo.List a database below upstream
// 018 exercises anyway -- there the columns arrive as NULL because the SELECT
// substituted a literal for them, not because a row was empty.

package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func strptr(s string) *string { return &s }

func TestVenueLocation_NameAndAddressProduceAFullLocation(t *testing.T) {
	loc := venueLocation(strptr("BMICH"), strptr("Bauddhaloka Mawatha, Colombo 07"))

	if loc == nil {
		t.Fatal("venueLocation = nil, want a location built from the venue columns")
	}
	if loc.Name != "BMICH" {
		t.Errorf("Name = %q, want %q", loc.Name, "BMICH")
	}
	if loc.Address != "Bauddhaloka Mawatha, Colombo 07" {
		t.Errorf("Address = %q, want the venue address", loc.Address)
	}
	// Nothing upstream models a floor plan at any granularity, so this must
	// stay unset rather than acquire an invented value.
	if loc.FloorPlanURL != "" {
		t.Errorf("FloorPlanURL = %q, want empty -- upstream models no floor plan", loc.FloorPlanURL)
	}
}

// A named venue with no address is the ordinary partially-filled state, and the
// address is omitted rather than sent as "" (openapi.yaml's absence convention).
func TestVenueLocation_NameWithoutAddressOmitsAddress(t *testing.T) {
	for _, tt := range []struct {
		name    string
		address *string
	}{
		{"address column NULL", nil},
		{"address column empty", strptr("")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			loc := venueLocation(strptr("BMICH"), tt.address)
			if loc == nil {
				t.Fatal("venueLocation = nil, want a location -- the venue is named")
			}
			if loc.Address != "" {
				t.Errorf("Address = %q, want empty so the key is omitted", loc.Address)
			}

			body, err := json.Marshal(loc)
			if err != nil {
				t.Fatalf("marshalling location: %v", err)
			}
			if strings.Contains(string(body), "address") {
				t.Errorf("serialized location = %s, want no address key", body)
			}
		})
	}
}

// Name is not omitempty, so an unnamed venue must produce no location at all --
// an object serializing as {"name": ""} would announce a place with no name to a
// client that already handles the key being absent. The venue columns are
// nullable and unpopulated by default upstream, so this is the live state until
// the content team fills them in.
func TestVenueLocation_WithoutNameThereIsNoLocation(t *testing.T) {
	for _, tt := range []struct {
		name           string
		venue, address *string
	}{
		{"both columns NULL (or absent below upstream 018)", nil, nil},
		{"name NULL, address recorded", nil, strptr("Bauddhaloka Mawatha, Colombo 07")},
		{"name empty, address recorded", strptr(""), strptr("Bauddhaloka Mawatha, Colombo 07")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if loc := venueLocation(tt.venue, tt.address); loc != nil {
				t.Errorf("venueLocation = %+v, want nil -- an unnamed venue is no location", loc)
			}
		})
	}
}

func TestSchemaCaps_VenueExprs_ReadTheColumnsWhenPresent(t *testing.T) {
	nameExpr, addressExpr := venueExprs(true, true)

	if nameExpr != "cc.venue_name" {
		t.Errorf("nameExpr = %q, want %q", nameExpr, "cc.venue_name")
	}
	if addressExpr != "cc.venue_address" {
		t.Errorf("addressExpr = %q, want %q", addressExpr, "cc.venue_address")
	}
}

// Below upstream 018 the columns are not there, and naming one is a hard error
// on the whole endpoint rather than an empty result. NULL::text, not a bare
// NULL, so it scans into the same *string the resolved form does -- which makes
// the degraded shape a drop-in and lands it on venueLocation's no-name path.
func TestSchemaCaps_VenueExprs_DegradeToNullWhenColumnsAbsent(t *testing.T) {
	for _, tt := range []struct {
		label         string
		name, address bool
		wantName      string
		wantAddress   string
	}{
		{"neither column", false, false, "NULL::text", "NULL::text"},
		{"name only, ALTERs applied separately", true, false, "cc.venue_name", "NULL::text"},
		{"address only, ALTERs applied separately", false, true, "NULL::text", "cc.venue_address"},
	} {
		t.Run(tt.label, func(t *testing.T) {
			nameExpr, addressExpr := venueExprs(tt.name, tt.address)
			if nameExpr != tt.wantName {
				t.Errorf("nameExpr = %q, want %q", nameExpr, tt.wantName)
			}
			if addressExpr != tt.wantAddress {
				t.Errorf("addressExpr = %q, want %q", addressExpr, tt.wantAddress)
			}
		})
	}
}

// A resolved capability is answered from the cache, never re-probed, which is
// what lets List call this once per request without a round trip. The nil pool
// makes that concrete: a probe here would panic.
func TestSchemaCaps_VenueSQLUsesTheCachedCapability(t *testing.T) {
	for _, tt := range []struct {
		label         string
		name, address bool
	}{
		{"both present", true, true},
		{"neither present", false, false},
	} {
		t.Run(tt.label, func(t *testing.T) {
			caps := &schemaCaps{venueResolved: true, hasVenueName: tt.name, hasVenueAddress: tt.address}

			gotName, gotAddress := caps.venueSQL(context.Background(), nil)
			wantName, wantAddress := venueExprs(tt.name, tt.address)
			if gotName != wantName || gotAddress != wantAddress {
				t.Errorf("venueSQL = (%q, %q), want (%q, %q)", gotName, gotAddress, wantName, wantAddress)
			}
		})
	}
}

// A failed probe must not be cached: memoizing it would cost every later request
// its location for the life of the process, not just this one.
func TestSchemaCaps_VenueProbeFailureIsNotCached(t *testing.T) {
	// Port 1 is reserved and never listening, so the probe fails fast with a
	// connection error rather than an answered "column absent".
	pool, err := pgxpool.New(context.Background(), "postgres://user@127.0.0.1:1/nodb")
	if err != nil {
		t.Fatalf("building pool: %v", err)
	}
	defer pool.Close()

	caps := &schemaCaps{}

	nameExpr, addressExpr := caps.venueSQL(context.Background(), pool)
	if nameExpr != "NULL::text" || addressExpr != "NULL::text" {
		t.Errorf("venueSQL = (%q, %q), want the degraded form when the probe fails", nameExpr, addressExpr)
	}

	caps.mu.Lock()
	resolved := caps.venueResolved
	caps.mu.Unlock()
	if resolved {
		t.Error("venue capability was marked resolved after a failed probe; the next request must retry instead of inheriting the failure")
	}
}

// The venue capability is a separate upstream migration from the colour and
// topic ones, so resolving it must not claim to have resolved those -- a
// database can carry any combination of 018, 023 and 027.
func TestSchemaCaps_VenueCapabilityIsIndependentOfTheOthers(t *testing.T) {
	caps := &schemaCaps{venueResolved: true, hasVenueName: true, hasVenueAddress: true}

	caps.mu.Lock()
	defer caps.mu.Unlock()
	if caps.resolved {
		t.Error("resolving the venue capability must not resolve the topic one")
	}
	if caps.colorResolved {
		t.Error("resolving the venue capability must not resolve the colour one")
	}
}
