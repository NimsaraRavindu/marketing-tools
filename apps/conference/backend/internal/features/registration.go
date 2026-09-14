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
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Grant is one evaluation of the access rule, with every input named at the
// call site. There are two call sites -- middleware.FeatureGate, which decides
// whether a route answers, and handlers.AppConfigHandler, which decides what
// the microapp is told about the screen in front of it -- and a rule spelled
// out twice is a rule that drifts.
type Grant struct {
	// Enabled is the is_<feature>_enabled row, as the resolver parsed it.
	Enabled bool
	// Registered reports a row in attendees for this caller's address.
	// The call sites skip the lookup when it cannot change the answer -- a
	// switched-off feature, or a caller the allowlist already settles --
	// so a false here may simply mean "not asked".
	Registered bool
	// Bypasses reports membership of GateBypassEmailsKey.
	Bypasses bool
}

// Allowed applies the one access rule this service has:
//
//	bypass OR ( enabled AND registered )
//
// Which is to say: a feature is served to a registered attendee when its flag
// is on, to an allowlisted caller always, and to nobody else.
//
// The bypass comes first and is unconditional, which is the whole point of it:
// it is how a named caller reaches a feature nobody else is meant to see yet,
// and a tester who is not also a registered attendee is the ordinary case, not
// an edge one.
//
// Registration is a narrowing on top of an enabled flag, never a widening. A
// switched-off feature stays switched off for a registered attendee -- if
// registration could turn a feature on, the flag would no longer be the thing
// that ships it.
//
// Why registration is part of the rule at all: AUTH_AUDIENCES names Asgardeo
// applications, not a guest list, so "holds a token this service will verify"
// and "is coming to the conference" are different populations. The attendees
// table is the only thing that knows the difference, and it is populated by
// the marketing team's sync rather than by anything in this service.
func (g Grant) Allowed() bool {
	if g.Bypasses {
		return true
	}
	return g.Enabled && g.Registered
}

// AttendeeLookup is the slice of repository.AttendeeProfileRepo that Membership
// needs.
type AttendeeLookup interface {
	// Exists reports whether the attendees table holds this address,
	// matched case-insensitively.
	Exists(ctx context.Context, email string) (bool, error)
}

// Membership answers "does this address have a row in attendees", with a TTL
// cache in front of the table.
//
// It exists because the answer is needed on every gated request, and an
// uncached lookup would put a query on the hot path of every route in the API
// -- for a fact that changes once per attendee, ever, when the marketing
// team's sync writes their row.
//
// Two TTLs, because the two answers age differently:
//
//   - A positive is durable. Attendees are deleted rarely and never as an
//     access-control action, so holding one for minutes costs nothing.
//   - A negative is not. It is the answer that locks somebody out, and the
//     event that changes it -- the sync adding their row -- lands without
//     warning and is watched by somebody refreshing the app. So it expires
//     fast, and the cost of that is one query per unregistered caller per
//     NegativeTTL, which is bounded by how many strangers hold a valid token.
type Membership struct {
	lookup      AttendeeLookup
	positiveTTL time.Duration
	negativeTTL time.Duration
	maxEntries  int
	now         func() time.Time

	mu      sync.Mutex
	entries map[string]membershipEntry
}

type membershipEntry struct {
	member  bool
	expires time.Time
}

// Membership cache tuning. The negative TTL is what somebody the sync has just
// added waits before the app works; the positive one is how long an attendee
// the sync has removed keeps their access.
const (
	MembershipPositiveTTL = 5 * time.Minute
	MembershipNegativeTTL = 10 * time.Second

	// membershipMaxEntries bounds the cache. One entry is an address and
	// two words, so this is a small map by any measure; the cap is here so
	// that a caller looping over invented addresses cannot grow it without
	// limit, not because a real attendee list would approach it.
	membershipMaxEntries = 50_000
)

// NewMembership builds a Membership over lookup with the default TTLs.
func NewMembership(lookup AttendeeLookup) *Membership {
	return &Membership{
		lookup:      lookup,
		positiveTTL: MembershipPositiveTTL,
		negativeTTL: MembershipNegativeTTL,
		maxEntries:  membershipMaxEntries,
		now:         time.Now,
		entries:     make(map[string]membershipEntry),
	}
}

// IsAttendee reports whether email has a row in attendees.
//
// Addresses are case-folded before both the cache lookup and the query, for
// the reason parseBypassEmails gives: no IdP in front of this service treats an
// address as case-sensitive even though the RFC lets it, and the token's claim
// and the stored row are written by different systems.
//
// An empty address is never a member. That is the token with no `email` claim,
// and an unidentified caller cannot be matched against a guest list.
//
// A failed lookup answers true, and the choice is deliberate. This check only
// ever narrows access that authentication has already granted, so treating a
// database blip as "not registered" would take every feature away from every
// attendee at once, turning a degraded database into a blank app. Treating it
// as "registered" leaves the feature flags deciding on their own, which is
// where they were before this rule existed. The failure is logged at Warn
// rather than swallowed, because a lookup that is failing quietly is a check
// that has silently stopped being enforced.
func (m *Membership) IsAttendee(ctx context.Context, email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}

	if member, ok := m.cached(email); ok {
		return member
	}

	member, err := m.lookup.Exists(ctx, email)
	if err != nil {
		slog.WarnContext(ctx, "attendee membership lookup failed, allowing the request",
			"error", err)
		return true
	}

	m.store(email, member)
	return member
}

func (m *Membership) cached(email string) (bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[email]
	if !ok || !m.now().Before(entry.expires) {
		return false, false
	}
	return entry.member, true
}

// store records an answer, first making room if the map has hit its cap.
//
// Eviction is deliberately crude: drop what has expired, and if that frees
// nothing, drop everything. A cache this is emptying has already been fed
// tens of thousands of distinct addresses, which is an attack rather than a
// workload, and the cost of being wrong is one query per request until it
// refills -- not an incorrect answer.
func (m *Membership) store(email string, member bool) {
	ttl := m.negativeTTL
	if member {
		ttl = m.positiveTTL
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.entries) >= m.maxEntries {
		now := m.now()
		for k, v := range m.entries {
			if !now.Before(v.expires) {
				delete(m.entries, k)
			}
		}
		if len(m.entries) >= m.maxEntries {
			m.entries = make(map[string]membershipEntry)
		}
	}

	m.entries[email] = membershipEntry{member: member, expires: m.now().Add(ttl)}
}
