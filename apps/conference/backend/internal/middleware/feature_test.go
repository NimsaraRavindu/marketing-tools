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

package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wso2-coin-backend/internal/features"
)

// stubGate answers from a fixed table keyed by "<METHOD> <route>". It lets
// nobody past a closed gate; bypassGate wraps it for the cases that do.
type stubGate map[string]features.State

func (s stubGate) Gate(_ context.Context, method, routePattern string) (features.State, bool) {
	st, ok := s[method+" "+routePattern]
	return st, ok
}

func (s stubGate) BypassesGates(context.Context, string) bool { return false }

// stubMembers answers from a fixed list of registered addresses, case-folded
// the way features.Membership folds them. fail stands for a lookup the
// database could not answer, which features.Membership resolves as true.
type stubMembers struct {
	registered []string
	// all stands in for "every caller has a row", which is what most cases
	// in this file want so they can be about something else.
	all  bool
	fail bool
}

func (m stubMembers) IsAttendee(_ context.Context, email string) bool {
	if m.fail || m.all {
		return true
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	for _, r := range m.registered {
		if strings.ToLower(r) == email {
			return true
		}
	}
	return false
}

// bypassGate is a stubGate whose allowlist holds exactly the addresses in
// allow, compared the way the real resolver compares them: case-folded, and
// never matching the empty string.
type bypassGate struct {
	stubGate
	allow []string
}

func (b bypassGate) BypassesGates(_ context.Context, email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	for _, a := range b.allow {
		if strings.ToLower(a) == email {
			return true
		}
	}
	return false
}

// getAs issues a GET whose request context carries an authenticated caller,
// the way Auth leaves it for FeatureGate on the same group. An empty email
// stands for a token that verified but carried no email claim.
func getAs(r *gin.Engine, path, email string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(WithUserInfo(req.Context(), &UserInfo{Email: email}))
	r.ServeHTTP(w, req)
	return w
}

// newFeatureTestRouter builds the router with everybody registered, so that a
// case about the flags or the allowlist is not also a case about the attendees
// table. TestFeatureGate_RegistrationRequired covers the other half.
func newFeatureTestRouter(t *testing.T, gate FeatureGateResolver) *gin.Engine {
	return newFeatureTestRouterWith(t, gate, stubMembers{all: true})
}

func newFeatureTestRouterWith(t *testing.T, gate FeatureGateResolver, members AttendeeMembership) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(FeatureGate(gate, members))
	handler := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	r.GET("/speakers", handler)
	r.GET("/speakers/:id", handler)
	r.GET("/events/current", handler)
	return r
}

func get(r *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestFeatureGate_DisabledFeatureIs503(t *testing.T) {
	r := newFeatureTestRouter(t, stubGate{
		"GET /speakers": {
			Feature: features.Speakers,
			Enabled: false,
			Title:   "Speakers coming soon",
			Message: "The speaker line-up is still being confirmed.",
		},
	})

	w := get(r, "/speakers")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	// The body must be usable by a client that has no flags of its own.
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["feature"] != "speakers" {
		t.Errorf("feature = %q", body["feature"])
	}
	if body["title"] != "Speakers coming soon" {
		t.Errorf("title = %q", body["title"])
	}
	// `message`, not `error`: the handler-side convention in this codebase.
	if body["message"] != "The speaker line-up is still being confirmed." {
		t.Errorf("message = %q", body["message"])
	}
}

func TestFeatureGate_EnabledFeaturePassesThrough(t *testing.T) {
	r := newFeatureTestRouter(t, stubGate{
		"GET /speakers": {Feature: features.Speakers, Enabled: true},
	})

	if w := get(r, "/speakers"); w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestFeatureGate_UngatedRoutePassesThrough(t *testing.T) {
	// An empty mapping is the "nothing configured" case, and it must leave
	// the API exactly as it behaved before this middleware existed.
	r := newFeatureTestRouter(t, stubGate{})

	if w := get(r, "/events/current"); w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestFeatureGate_MatchesOnTheRoutePatternNotTheConcretePath(t *testing.T) {
	// The mapping is written against "/speakers/:id"; a request for a real
	// id must still match it.
	r := newFeatureTestRouter(t, stubGate{
		"GET /speakers/:id": {Feature: features.SpeakerDetails, Enabled: false, Message: "later"},
	})

	if w := get(r, "/speakers/abc-123"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	// The list route is a different pattern and must be unaffected.
	if w := get(r, "/speakers"); w.Code != http.StatusOK {
		t.Errorf("status = %d, want the sibling route untouched", w.Code)
	}
}

func TestFeatureGate_UnmatchedPathIsLeftToGin(t *testing.T) {
	// FullPath() is empty for a path no route matched. Gating it would turn
	// gin's 404 into a 503 and hide genuine wiring mistakes.
	r := newFeatureTestRouter(t, stubGate{
		"GET ": {Feature: "whatever", Enabled: false},
	})

	if w := get(r, "/nope"); w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// A 503 from the gate must never pick up a cache validator, or a client would
// keep serving "coming soon" after the feature is switched back on.
func TestFeatureGate_RefusalIsNotGivenAnETag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ETag("private, max-age=60, must-revalidate"))
	r.Use(FeatureGate(stubGate{
		"GET /speakers": {Feature: features.Speakers, Enabled: false, Message: "later"},
	}, stubMembers{all: true}))
	r.GET("/speakers", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	w := get(r, "/speakers")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", w.Code)
	}
	if etag := w.Header().Get("ETag"); etag != "" {
		t.Errorf("ETag = %q, want none on a refusal", etag)
	}
}

// The point of the whole change: the flag stays off -- so every microapp keeps
// hiding the screen -- and the listed caller still reaches the endpoint.
func TestFeatureGate_AllowlistedCallerIsServedADisabledFeature(t *testing.T) {
	r := newFeatureTestRouter(t, bypassGate{
		stubGate: stubGate{
			"GET /speakers": {Feature: features.Speakers, Enabled: false, Message: "later"},
		},
		allow: []string{"tester@wso2.com"},
	})

	w := getAs(r, "/speakers", "tester@wso2.com")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d for an allowlisted caller", w.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshalling body: %v", err)
	}
	if body["ok"] != true {
		t.Errorf("body = %v, want the handler's own response", body)
	}
}

// The bypass is per caller, so it must not relax the gate for anyone else.
func TestFeatureGate_UnlistedCallerStillGets503(t *testing.T) {
	r := newFeatureTestRouter(t, bypassGate{
		stubGate: stubGate{
			"GET /speakers": {Feature: features.Speakers, Enabled: false, Message: "later"},
		},
		allow: []string{"tester@wso2.com"},
	})

	if w := getAs(r, "/speakers", "attendee@example.com"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d for a caller not on the list", w.Code, http.StatusServiceUnavailable)
	}
}

// A request with no UserInfo in its context -- FeatureGate registered without
// Auth in front of it -- must deny rather than consult the allowlist with an
// empty address.
func TestFeatureGate_UnidentifiedCallerIsNotBypassed(t *testing.T) {
	r := newFeatureTestRouter(t, bypassGate{
		stubGate: stubGate{
			"GET /speakers": {Feature: features.Speakers, Enabled: false, Message: "later"},
		},
		// An allowlist that would match an empty email if it were consulted.
		allow: []string{""},
	})

	if w := get(r, "/speakers"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d with no authenticated caller", w.Code, http.StatusServiceUnavailable)
	}
}

// An enabled feature is not affected by the allowlist either way, and an
// allowlisted caller on an ungated route is an ordinary request.
func TestFeatureGate_AllowlistDoesNotChangeAnOpenRoute(t *testing.T) {
	r := newFeatureTestRouter(t, bypassGate{
		stubGate: stubGate{
			"GET /speakers": {Feature: features.Speakers, Enabled: true},
		},
		allow: []string{"tester@wso2.com"},
	})

	if w := getAs(r, "/speakers", "tester@wso2.com"); w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w := getAs(r, "/events/current", "tester@wso2.com"); w.Code != http.StatusOK {
		t.Errorf("ungated route status = %d, want %d", w.Code, http.StatusOK)
	}
}

// An enabled feature is refused to a caller the attendees table does not hold
// -- who gets exactly what they would get if the feature were switched off,
// because from where they stand it is.
func TestFeatureGate_RegistrationRequired(t *testing.T) {
	enabled := stubGate{
		"GET /speakers": {
			Feature: features.Speakers,
			Enabled: true,
			Title:   "Speakers coming soon",
			Message: "The speaker line-up is still being confirmed.",
		},
	}
	disabled := stubGate{
		"GET /speakers": {Feature: features.Speakers, Enabled: false, Title: "t", Message: "m"},
	}

	tests := []struct {
		name  string
		gate  FeatureGateResolver
		email string
		want  int
	}{
		{name: "registered attendee", gate: enabled, email: "attendee@wso2.com", want: http.StatusOK},
		{name: "unregistered caller", gate: enabled, email: "stranger@example.com", want: http.StatusServiceUnavailable},
		{name: "case-folded match", gate: enabled, email: "Attendee@WSO2.com", want: http.StatusOK},
		{name: "no email claim", gate: enabled, email: "", want: http.StatusServiceUnavailable},

		// The allowlist beats it: a tester who is not also a registered
		// attendee is the ordinary case, not an edge one.
		{
			name:  "allowlisted but unregistered",
			gate:  bypassGate{stubGate: enabled, allow: []string{"tester@wso2.com"}},
			email: "tester@wso2.com",
			want:  http.StatusOK,
		},
		// Registration narrows an enabled flag; it never turns one on.
		{name: "registered but feature off", gate: disabled, email: "attendee@wso2.com", want: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFeatureTestRouterWith(t, tt.gate, stubMembers{registered: []string{"attendee@wso2.com"}})

			w := getAs(r, "/speakers", tt.email)

			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d, body: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

// A route nobody gated is untouched. /events/current and the attendee routes
// are how the shell learns who is holding the phone; gating them would turn a
// hidden screen into a broken app for anybody the sync has not reached yet.
func TestFeatureGate_RegistrationDoesNotTouchUngatedRoutes(t *testing.T) {
	r := newFeatureTestRouterWith(t,
		stubGate{"GET /speakers": {Feature: features.Speakers, Enabled: true}},
		stubMembers{})

	w := getAs(r, "/events/current", "stranger@example.com")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// A membership lookup the database could not answer must not black out the
// app. features.Membership resolves a failure as true and this is the gate
// honouring that.
func TestFeatureGate_RegistrationLookupFailureServes(t *testing.T) {
	r := newFeatureTestRouterWith(t,
		stubGate{"GET /speakers": {Feature: features.Speakers, Enabled: true}},
		stubMembers{fail: true})

	w := getAs(r, "/speakers", "stranger@example.com")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
