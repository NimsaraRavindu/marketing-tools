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

package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wso2-coin-backend/internal/clients/aiagent"
	"wso2-coin-backend/internal/middleware"
	"wso2-coin-backend/internal/models"
)

var aiAdminRoles = []string{"event-admin"}

// aiAdminUser is in the allow-list; aiNonAdminUser (reuse testUser) is not.
var aiAdminUser = &middleware.UserInfo{
	Email:    "admin@example.com",
	UserID:   "admin-1",
	Groups:   []string{"wso2-everyone", "event-admin"},
	RawToken: "admin-token",
}

// newAdminTestRouter mounts the nine admin routes with a middleware that injects
// user (when non-nil), mirroring newAIAgentTestRouter.
func newAdminTestRouter(h *AIAgentHandler, user *middleware.UserInfo) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if user != nil {
			ctx := middleware.WithUserInfo(c.Request.Context(), user)
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	})
	r.POST("/admin/o2bar/engineers", h.CreateEngineer)
	r.GET("/admin/o2bar/engineers", h.ListEngineers)
	r.DELETE("/admin/o2bar/engineers/:email", h.DeleteEngineer)
	r.GET("/admin/o2bar/engineers/:email/exists", h.EngineerExists)
	r.POST("/admin/ai-profiles", h.AdminCreateProfile)
	r.GET("/admin/ai-profiles/:email", h.AdminGetProfile)
	r.PATCH("/admin/ai-profiles/:email", h.AdminUpdateProfile)
	r.DELETE("/admin/ai-profiles/:email", h.AdminDeleteProfile)
	r.GET("/admin/ai-profiles/:email/exists", h.ProfileExists)
	return r
}

func adminHandler(client AIAgentClient) *AIAgentHandler {
	return NewAIAgentHandler(client, &fakeAttendeeRepo{}, allAIFeaturesOn, nil, aiAdminRoles)
}

func validEngineerBody() models.EngineerCreateRequest {
	return models.EngineerCreateRequest{
		Engineer: models.EngineerProfileInput{
			Email:                    "eng@wso2.com",
			Name:                     "Eng Ineer",
			Title:                    "Solutions Architect",
			Domains:                  "API management",
			FamiliarProducts:         "WSO2 API Manager",
			SpecializeAreas:          "gateway, security",
			YearsOfWorkingExperience: intPtr(8),
			ExampleQuestions:         "How do I secure an API?",
			AvailableTimeSlots:       "2026-09-22 09:00-10:00",
		},
	}
}

// upstreamStatusErr builds an *aiagent.StatusError as the client would return
// for a non-2xx upstream response.
func upstreamStatusErr(code int, body string) error {
	return &aiagent.StatusError{StatusCode: code, URL: "https://ai.example.com/x", Body: body}
}

// --- RBAC: every route rejects a caller who is not authenticated or not in the
// allow-list. Table covers all nine so a new route cannot be added ungated.

func TestAdmin_RBAC_AllRoutes(t *testing.T) {
	routes := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/admin/o2bar/engineers", validEngineerBody()},
		{http.MethodGet, "/admin/o2bar/engineers", nil},
		{http.MethodDelete, "/admin/o2bar/engineers/eng@wso2.com", nil},
		{http.MethodGet, "/admin/o2bar/engineers/eng@wso2.com/exists", nil},
		{http.MethodPost, "/admin/ai-profiles", models.AdminProfileCreateRequest{User: models.PersonalizeAgentUserProfile{Email: "a@b.com"}}},
		{http.MethodGet, "/admin/ai-profiles/a@b.com", nil},
		{http.MethodPatch, "/admin/ai-profiles/a@b.com", models.AdminProfileUpdateRequest{LinkedInInfo: "x"}},
		{http.MethodDelete, "/admin/ai-profiles/a@b.com", nil},
		{http.MethodGet, "/admin/ai-profiles/a@b.com/exists", nil},
	}

	for _, rt := range routes {
		// Unauthenticated -> 401.
		h := adminHandler(&fakeAIAgentClient{})
		w := doRequest(newAdminTestRouter(h, nil), rt.method, rt.path, rt.body)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s unauthenticated: status = %d, want 401", rt.method, rt.path, w.Code)
		}
		// Authenticated but not in the allow-list -> 403.
		h = adminHandler(&fakeAIAgentClient{})
		w = doRequest(newAdminTestRouter(h, testUser), rt.method, rt.path, rt.body)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s non-admin: status = %d, want 403", rt.method, rt.path, w.Code)
		}
	}
}

// --- Happy paths.

func TestAdmin_CreateEngineer_201(t *testing.T) {
	client := &fakeAIAgentClient{engineerResp: &models.EngineerResponse{ID: "1", Email: "eng@wso2.com", Name: "Eng Ineer", Created: true, Message: "Engineer profile added to the vector store."}}
	h := adminHandler(client)
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodPost, "/admin/o2bar/engineers", validEngineerBody())

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", w.Code, w.Body.String())
	}
	if client.engineerSeen.Engineer.Email != "eng@wso2.com" {
		t.Fatalf("engineer email forwarded = %q", client.engineerSeen.Engineer.Email)
	}
	if client.jwtSeen != aiAdminUser.RawToken {
		t.Fatalf("forwarded jwt = %q, want the caller's raw token", client.jwtSeen)
	}
	var got models.EngineerResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Created {
		t.Fatalf("created = false, want true")
	}
}

func TestAdmin_CreateEngineer_BindError_400(t *testing.T) {
	h := adminHandler(&fakeAIAgentClient{})
	// Missing every required field.
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodPost, "/admin/o2bar/engineers",
		models.EngineerCreateRequest{Engineer: models.EngineerProfileInput{Name: "no email"}})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
}

// yearsOfWorkingExperience is required by openapi.yaml, so an omitted one is a
// 400 rather than an engineer silently registered with no experience -- and a
// submitted 0 still has to get through, which is the reason for the pointer.
func TestAdmin_CreateEngineer_YearsOfExperience(t *testing.T) {
	tests := []struct {
		name  string
		years *int
		want  int
	}{
		{name: "omitted", years: nil, want: http.StatusBadRequest},
		{name: "zero is a real answer", years: intPtr(0), want: http.StatusCreated},
		{name: "past the upper bound", years: intPtr(61), want: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := validEngineerBody()
			body.Engineer.YearsOfWorkingExperience = tc.years
			h := adminHandler(&fakeAIAgentClient{engineerResp: &models.EngineerResponse{ID: "e1", Created: true}})
			w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodPost, "/admin/o2bar/engineers", body)

			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d, body: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestAdmin_ListEngineers_EmptyIsArrayNotNull(t *testing.T) {
	h := adminHandler(&fakeAIAgentClient{engineers: nil})
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodGet, "/admin/o2bar/engineers", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); body != "[]" {
		t.Fatalf("body = %q, want %q (never null)", body, "[]")
	}
}

func TestAdmin_ListEngineers_200(t *testing.T) {
	client := &fakeAIAgentClient{engineers: []models.EngineerSummary{{Email: "eng@wso2.com", Name: "Eng"}}}
	h := adminHandler(client)
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodGet, "/admin/o2bar/engineers", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got []models.EngineerSummary
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Email != "eng@wso2.com" {
		t.Fatalf("got = %+v", got)
	}
}

func TestAdmin_DeleteEngineer_204(t *testing.T) {
	client := &fakeAIAgentClient{}
	h := adminHandler(client)
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodDelete, "/admin/o2bar/engineers/eng@wso2.com", nil)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body: %s", w.Code, w.Body.String())
	}
	if client.emailSeen != "eng@wso2.com" {
		t.Fatalf("email forwarded = %q", client.emailSeen)
	}
}

// Now that the identifier lives in the path, an omitted email is not a bad
// request but an unroutable one: DELETE on the collection is a route that does
// not exist, and gin answers before any handler runs. The 400 the handler still
// carries is reachable only through a blank segment, which is what the second
// case pins.
func TestAdmin_DeleteEngineer_NoEmailSegment_NotRouted(t *testing.T) {
	h := adminHandler(&fakeAIAgentClient{})
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodDelete, "/admin/o2bar/engineers", nil)

	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 404 or 405", w.Code)
	}
}

func TestAdmin_DeleteEngineer_BlankEmailSegment_400(t *testing.T) {
	client := &fakeAIAgentClient{}
	h := adminHandler(client)
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodDelete, "/admin/o2bar/engineers/%20", nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	if client.emailSeen != "" {
		t.Fatalf("blank segment reached upstream as %q", client.emailSeen)
	}
}

// An address needs no escaping in a path segment, but operator tooling escapes
// "@" anyway; both spellings must reach the client as the same address.
func TestAdmin_DeleteEngineer_EmailSpellings(t *testing.T) {
	for _, path := range []string{"/admin/o2bar/engineers/eng@wso2.com", "/admin/o2bar/engineers/eng%40wso2.com"} {
		client := &fakeAIAgentClient{}
		h := adminHandler(client)
		w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodDelete, path, nil)

		if w.Code != http.StatusNoContent {
			t.Fatalf("%s: status = %d, want 204, body: %s", path, w.Code, w.Body.String())
		}
		if client.emailSeen != "eng@wso2.com" {
			t.Fatalf("%s: email forwarded = %q, want eng@wso2.com", path, client.emailSeen)
		}
	}
}

func TestAdmin_DeleteEngineer_Upstream404_404(t *testing.T) {
	client := &fakeAIAgentClient{deleteEngineerErr: upstreamStatusErr(http.StatusNotFound, `{"detail":"Engineer not found for the given email."}`)}
	h := adminHandler(client)
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodDelete, "/admin/o2bar/engineers/nope@wso2.com", nil)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", w.Code, w.Body.String())
	}
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["message"] == "" {
		t.Fatalf("expected a message, got %q", w.Body.String())
	}
}

func TestAdmin_EngineerExists_200(t *testing.T) {
	client := &fakeAIAgentClient{engineerExists: &models.ExistsResponse{Exists: true}}
	h := adminHandler(client)
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodGet, "/admin/o2bar/engineers/eng@wso2.com/exists", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got models.ExistsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !got.Exists {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
}

func TestAdmin_CreateProfile_201(t *testing.T) {
	client := &fakeAIAgentClient{adminProfileResp: &models.ProfileResponse{Email: "a@b.com", Created: true, Message: "ok"}}
	h := adminHandler(client)
	body := models.AdminProfileCreateRequest{User: models.PersonalizeAgentUserProfile{Email: "a@b.com", Name: "A B"}}
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodPost, "/admin/ai-profiles", body)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", w.Code, w.Body.String())
	}
	if client.adminProfileSeen.User.Email != "a@b.com" {
		t.Fatalf("forwarded email = %q", client.adminProfileSeen.User.Email)
	}
}

func TestAdmin_CreateProfile_MissingEmail_400(t *testing.T) {
	h := adminHandler(&fakeAIAgentClient{})
	body := models.AdminProfileCreateRequest{User: models.PersonalizeAgentUserProfile{Name: "no email"}}
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodPost, "/admin/ai-profiles", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
}

func TestAdmin_GetProfile_200(t *testing.T) {
	client := &fakeAIAgentClient{adminGetProfile: map[string]any{"email": "a@b.com", "name": "A B"}}
	h := adminHandler(client)
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodGet, "/admin/ai-profiles/a@b.com", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got["email"] != "a@b.com" {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
}

func TestAdmin_GetProfile_Upstream404_404(t *testing.T) {
	client := &fakeAIAgentClient{adminGetProfileErr: upstreamStatusErr(http.StatusNotFound, `{"detail":"not found"}`)}
	h := adminHandler(client)
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodGet, "/admin/ai-profiles/nope@b.com", nil)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", w.Code, w.Body.String())
	}
}

func TestAdmin_GetProfile_NoEmailSegment_NotRouted(t *testing.T) {
	h := adminHandler(&fakeAIAgentClient{})
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodGet, "/admin/ai-profiles", nil)

	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 404 or 405", w.Code)
	}
}

func TestAdmin_GetProfile_BlankEmailSegment_400(t *testing.T) {
	client := &fakeAIAgentClient{}
	h := adminHandler(client)
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodGet, "/admin/ai-profiles/%20", nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	if client.emailSeen != "" {
		t.Fatalf("blank segment reached upstream as %q", client.emailSeen)
	}
}

func TestAdmin_UpdateProfile_200(t *testing.T) {
	client := &fakeAIAgentClient{adminUpdateProfile: map[string]any{"email": "a@b.com"}}
	h := adminHandler(client)
	body := models.AdminProfileUpdateRequest{LinkedInInfo: "fresh text"}
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodPatch, "/admin/ai-profiles/a@b.com", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if client.adminUpdateSeen.LinkedInInfo != "fresh text" {
		t.Fatalf("forwarded linkedInInfo = %q", client.adminUpdateSeen.LinkedInInfo)
	}
}

func TestAdmin_UpdateProfile_BindError_400(t *testing.T) {
	h := adminHandler(&fakeAIAgentClient{})
	// Empty linkedInInfo violates the required binding.
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodPatch, "/admin/ai-profiles/a@b.com",
		models.AdminProfileUpdateRequest{})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAdmin_DeleteProfile_204(t *testing.T) {
	h := adminHandler(&fakeAIAgentClient{})
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodDelete, "/admin/ai-profiles/a@b.com", nil)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body: %s", w.Code, w.Body.String())
	}
}

func TestAdmin_ProfileExists_200(t *testing.T) {
	client := &fakeAIAgentClient{profileExists: &models.ExistsResponse{Exists: false}}
	h := adminHandler(client)
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodGet, "/admin/ai-profiles/a@b.com/exists", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// --- Error mapping: a gateway 401/403 (never con-ai, never the admin's input)
// becomes a generic 500, so the gateway's token taxonomy is never relayed and a
// 401 never reaches a frontend auth interceptor on an admin call.

func TestAdmin_Upstream401_Becomes500(t *testing.T) {
	client := &fakeAIAgentClient{engineerErr: upstreamStatusErr(http.StatusUnauthorized, `{"code":"900901"}`)}
	h := adminHandler(client)
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodPost, "/admin/o2bar/engineers", validEngineerBody())

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); jsonHas(t, body, "code", "900901") {
		t.Fatalf("gateway token code leaked to client: %s", body)
	}
}

func TestAdmin_Upstream403_Becomes500(t *testing.T) {
	client := &fakeAIAgentClient{adminProfileErr: upstreamStatusErr(http.StatusForbidden, `{"code":"900908"}`)}
	h := adminHandler(client)
	body := models.AdminProfileCreateRequest{User: models.PersonalizeAgentUserProfile{Email: "a@b.com"}}
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodPost, "/admin/ai-profiles", body)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body: %s", w.Code, w.Body.String())
	}
}

// TestAdmin_Upstream400_Relayed proves con-ai's own client-fixable 400 (e.g. a
// bad time-slot format) is relayed at 400 with the upstream message, not
// flattened to a 500.
func TestAdmin_Upstream400_Relayed(t *testing.T) {
	client := &fakeAIAgentClient{engineerErr: upstreamStatusErr(http.StatusBadRequest, `{"detail":"Each time slot needs a date"}`)}
	h := adminHandler(client)
	w := doRequest(newAdminTestRouter(h, aiAdminUser), http.MethodPost, "/admin/o2bar/engineers", validEngineerBody())

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["message"] != "Each time slot needs a date" {
		t.Fatalf("message = %q, want the relayed upstream detail", got["message"])
	}
}

func TestExtractUpstreamMessage(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`{"detail":"bad slot"}`, "bad slot"},
		{`{"message":"nope"}`, "nope"},
		{`{"detail":[{"loc":["x"]}]}`, ""}, // FastAPI validation list -> ignored
		{`not json`, ""},
		{``, ""},
	}
	for _, tc := range cases {
		if got := extractUpstreamMessage(tc.in); got != tc.want {
			t.Errorf("extractUpstreamMessage(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// jsonHas reports whether body is a JSON object whose key equals want.
func jsonHas(t *testing.T, body, key, want string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return false
	}
	v, ok := m[key].(string)
	return ok && v == want
}

// --- Audit trail. con-ai authenticates nobody and keeps no record of its own,
// so these Info lines are the only place a successful mutation is written down.

// auditRecords returns every captured log record that carries an "action"
// attribute, decoded whole. logMessages (aiagent_test.go) only exposes "msg",
// which is not enough here: the point of the audit line is the attributes
// beside the message.
func auditRecords(t *testing.T, buf *lockedBuffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		if _, ok := record["action"]; ok {
			records = append(records, record)
		}
	}
	return records
}

// Each mutating route must name the caller, the action and the target once the
// upstream call has actually succeeded.
func TestAdmin_MutatingRoutes_EmitAuditLine(t *testing.T) {
	cases := []struct {
		name         string
		method, path string
		body         any
		client       *fakeAIAgentClient
		wantAction   string
		wantTarget   string
	}{
		{
			name: "create engineer", method: http.MethodPost, path: "/admin/o2bar/engineers",
			body:   validEngineerBody(),
			client: &fakeAIAgentClient{engineerResp: &models.EngineerResponse{ID: "1", Email: "eng@wso2.com"}},
			// The target comes from the body here, not the query string: this is
			// the one mutation that carries its subject in the payload.
			wantAction: "create-o2bar-engineer", wantTarget: "eng@wso2.com",
		},
		{
			name: "delete engineer", method: http.MethodDelete, path: "/admin/o2bar/engineers/eng@wso2.com",
			client:     &fakeAIAgentClient{},
			wantAction: "delete-o2bar-engineer", wantTarget: "eng@wso2.com",
		},
		{
			name: "create profile", method: http.MethodPost, path: "/admin/ai-profiles",
			body:       models.AdminProfileCreateRequest{User: models.PersonalizeAgentUserProfile{Email: "a@b.com", Name: "A B"}},
			client:     &fakeAIAgentClient{adminProfileResp: &models.ProfileResponse{Email: "a@b.com"}},
			wantAction: "create-ai-profile", wantTarget: "a@b.com",
		},
		{
			name: "update profile", method: http.MethodPatch, path: "/admin/ai-profiles/a@b.com",
			body:       models.AdminProfileUpdateRequest{LinkedInInfo: "fresh text"},
			client:     &fakeAIAgentClient{adminUpdateProfile: map[string]any{"email": "a@b.com"}},
			wantAction: "update-ai-profile", wantTarget: "a@b.com",
		},
		{
			name: "delete profile", method: http.MethodDelete, path: "/admin/ai-profiles/a@b.com",
			client:     &fakeAIAgentClient{},
			wantAction: "delete-ai-profile", wantTarget: "a@b.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureAILogs(t)
			h := adminHandler(tc.client)
			w := doRequest(newAdminTestRouter(h, aiAdminUser), tc.method, tc.path, tc.body)
			if w.Code >= 300 {
				t.Fatalf("status = %d, want 2xx, body: %s", w.Code, w.Body.String())
			}

			records := auditRecords(t, logs)
			if len(records) != 1 {
				t.Fatalf("got %d audit records, want exactly 1: %s", len(records), logs.String())
			}
			got := records[0]
			if got["action"] != tc.wantAction {
				t.Errorf("action = %v, want %q", got["action"], tc.wantAction)
			}
			if got["targetEmail"] != tc.wantTarget {
				t.Errorf("targetEmail = %v, want %q", got["targetEmail"], tc.wantTarget)
			}
			// Same attribute name requireAIAdmin's denial uses, so the allowed
			// and refused calls of one operator sit on one query.
			if got["user"] != aiAdminUser.UserID {
				t.Errorf("user = %v, want %q", got["user"], aiAdminUser.UserID)
			}
			// The deployed log viewer renders only the message, so the action
			// has to survive there too.
			msg, _ := got["msg"].(string)
			if !strings.Contains(msg, tc.wantAction) {
				t.Errorf("msg = %q, should name the action", msg)
			}
		})
	}
}

// Nothing is audited when nothing changed: a refused caller, a failed upstream
// call and a plain read must all leave the trail empty, or the record of "who
// removed this engineer" drowns in entries where no one removed anything.
func TestAdmin_NoAuditLineWithoutAMutation(t *testing.T) {
	cases := []struct {
		name         string
		method, path string
		body         any
		user         *middleware.UserInfo
		client       *fakeAIAgentClient
	}{
		{
			name: "refused caller", method: http.MethodDelete, path: "/admin/o2bar/engineers/eng@wso2.com",
			user: testUser, client: &fakeAIAgentClient{},
		},
		{
			name: "upstream rejected the delete", method: http.MethodDelete, path: "/admin/ai-profiles/a@b.com",
			user:   aiAdminUser,
			client: &fakeAIAgentClient{adminDeleteProfileErr: upstreamStatusErr(http.StatusNotFound, `{"detail":"not found"}`)},
		},
		{
			name: "read-only exists check", method: http.MethodGet, path: "/admin/ai-profiles/a@b.com/exists",
			user: aiAdminUser, client: &fakeAIAgentClient{profileExists: &models.ExistsResponse{Exists: true}},
		},
		{
			name: "read-only list", method: http.MethodGet, path: "/admin/o2bar/engineers",
			user: aiAdminUser, client: &fakeAIAgentClient{engineers: []models.EngineerSummary{{Email: "eng@wso2.com"}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureAILogs(t)
			h := adminHandler(tc.client)
			doRequest(newAdminTestRouter(h, tc.user), tc.method, tc.path, tc.body)

			if records := auditRecords(t, logs); len(records) != 0 {
				t.Fatalf("got %d audit records, want none: %s", len(records), logs.String())
			}
		})
	}
}

func intPtr(v int) *int { return &v }
