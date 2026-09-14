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

package features

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// The rule, in full. Every other test in this package and the two call sites
// in middleware and handlers lean on this being right.
func TestGrantAllowed(t *testing.T) {
	tests := []struct {
		name  string
		grant Grant
		want  bool
	}{
		{name: "off, nobody", grant: Grant{}, want: false},
		{name: "off, registered", grant: Grant{Registered: true}, want: false},

		// An enabled feature is for registered attendees, not for everyone
		// holding a token this service will verify.
		{name: "on, registered", grant: Grant{Enabled: true, Registered: true}, want: true},
		{name: "on, unregistered", grant: Grant{Enabled: true}, want: false},

		// The bypass is unconditional, which is the whole point of it.
		{name: "off, allowlisted", grant: Grant{Bypasses: true}, want: true},
		{name: "off, allowlisted and unregistered", grant: Grant{Bypasses: true}, want: true},
		{name: "on, allowlisted and unregistered", grant: Grant{Enabled: true, Bypasses: true}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.grant.Allowed(); got != tt.want {
				t.Errorf("Allowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

// stubLookup counts calls so the cache can be shown to be a cache.
type stubLookup struct {
	mu      sync.Mutex
	members map[string]bool
	err     error
	calls   int
}

func (s *stubLookup) Exists(_ context.Context, email string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return false, s.err
	}
	return s.members[strings.ToLower(email)], nil
}

func (s *stubLookup) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestMembership_IsAttendee(t *testing.T) {
	lookup := &stubLookup{members: map[string]bool{"attendee@wso2.com": true}}
	m := NewMembership(lookup)
	ctx := context.Background()

	if !m.IsAttendee(ctx, "attendee@wso2.com") {
		t.Error("a registered attendee must be a member")
	}
	// Case-folded on both sides, for the reason parseBypassEmails gives: the
	// claim and the stored row are written by different systems.
	if !m.IsAttendee(ctx, "  Attendee@WSO2.com  ") {
		t.Error("matching must be case- and space-insensitive")
	}
	if m.IsAttendee(ctx, "stranger@example.com") {
		t.Error("an address with no row must not be a member")
	}
	// An unidentified caller cannot be matched against a guest list, and the
	// table must not be asked about the empty string.
	before := lookup.count()
	if m.IsAttendee(ctx, "") {
		t.Error("an empty address must never be a member")
	}
	if lookup.count() != before {
		t.Error("an empty address must not reach the table")
	}
}

// The point of the type: one query per address per TTL, not one per request.
func TestMembership_CachesBothAnswers(t *testing.T) {
	lookup := &stubLookup{members: map[string]bool{"attendee@wso2.com": true}}
	m := NewMembership(lookup)
	ctx := context.Background()

	for range 5 {
		m.IsAttendee(ctx, "attendee@wso2.com")
		m.IsAttendee(ctx, "stranger@example.com")
	}

	if got := lookup.count(); got != 2 {
		t.Errorf("lookups = %d, want 2 (one per address)", got)
	}
}

// A negative expires fast, because the event that changes it -- the caller
// finishing registration -- happens seconds before they expect the app to
// work. A positive is held far longer.
func TestMembership_NegativesExpireBeforePositives(t *testing.T) {
	lookup := &stubLookup{members: map[string]bool{"attendee@wso2.com": true}}
	m := NewMembership(lookup)

	now := time.Now()
	m.now = func() time.Time { return now }
	ctx := context.Background()

	m.IsAttendee(ctx, "attendee@wso2.com")
	m.IsAttendee(ctx, "stranger@example.com")
	if got := lookup.count(); got != 2 {
		t.Fatalf("lookups = %d, want 2", got)
	}

	now = now.Add(MembershipNegativeTTL + time.Second)
	m.IsAttendee(ctx, "attendee@wso2.com")
	m.IsAttendee(ctx, "stranger@example.com")
	if got := lookup.count(); got != 3 {
		t.Errorf("lookups = %d, want 3 -- the negative should have expired and the positive should not", got)
	}

	now = now.Add(MembershipPositiveTTL)
	m.IsAttendee(ctx, "attendee@wso2.com")
	if got := lookup.count(); got != 4 {
		t.Errorf("lookups = %d, want 4 -- the positive should have expired", got)
	}
}

// Somebody the sync has just added must not stay locked out for long.
func TestMembership_NoticesANewAttendee(t *testing.T) {
	lookup := &stubLookup{members: map[string]bool{}}
	m := NewMembership(lookup)

	now := time.Now()
	m.now = func() time.Time { return now }
	ctx := context.Background()

	if m.IsAttendee(ctx, "newcomer@wso2.com") {
		t.Fatal("the sync has not written this row yet")
	}

	lookup.mu.Lock()
	lookup.members["newcomer@wso2.com"] = true
	lookup.mu.Unlock()

	now = now.Add(MembershipNegativeTTL + time.Second)
	if !m.IsAttendee(ctx, "newcomer@wso2.com") {
		t.Error("a row the sync has added must be picked up within the negative TTL")
	}
}

// A database blip must leave the feature flags deciding on their own, not
// black out every screen.
func TestMembership_LookupFailureGrants(t *testing.T) {
	lookup := &stubLookup{err: errors.New("connection refused")}
	m := NewMembership(lookup)

	if !m.IsAttendee(context.Background(), "anyone@example.com") {
		t.Error("a failed lookup must grant, not refuse")
	}
	// ...and must not be cached, or one blip would grant for the whole TTL.
	m.IsAttendee(context.Background(), "anyone@example.com")
	if got := lookup.count(); got != 2 {
		t.Errorf("lookups = %d, want 2 -- a failure must not be cached", got)
	}
}
