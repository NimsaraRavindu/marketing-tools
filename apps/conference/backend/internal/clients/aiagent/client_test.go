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

package aiagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wso2-coin-backend/internal/config"
	"wso2-coin-backend/internal/models"
)

func TestNewClient_SetsTimeout(t *testing.T) {
	c := NewClient(config.AIAgentConfig{
		ServiceURL:     "https://ai.example.com",
		RequestTimeout: 45 * time.Second,
	})
	if c.httpClient.Timeout != 45*time.Second {
		t.Errorf("httpClient.Timeout = %v, want 45s", c.httpClient.Timeout)
	}
}

func TestRetrieveMatches_Success(t *testing.T) {
	const jwt = "user-jwt-assertion"
	want := []models.RecommendedUser{{Email: "a@wso2.com", Name: "A", Tags: []string{"go"}}}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/networking/recommend" {
			t.Errorf("expected path /networking/recommend, got %q", r.URL.Path)
		}
		if got := r.Header.Get("x-jwt-assertion"); got != jwt {
			t.Errorf("x-jwt-assertion = %q, want %q", got, jwt)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "{}" {
			t.Errorf("expected body {}, got %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer server.Close()

	client := NewClientWithHTTPClient(config.AIAgentConfig{ServiceURL: server.URL}, server.Client())

	got, err := client.RetrieveMatches(context.Background(), jwt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Email != "a@wso2.com" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestRetrieveMatches_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()

	client := NewClientWithHTTPClient(config.AIAgentConfig{ServiceURL: server.URL}, server.Client())

	_, err := client.RetrieveMatches(context.Background(), "jwt")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestRetrieveO2BarRecommendations_NilQuestionSendsNoBody(t *testing.T) {
	want := []models.O2BarRecommendationResponse{{Email: "a@wso2.com", Name: "A"}}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/o2bar/recommend" {
			t.Errorf("expected path /o2bar/recommend, got %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) != 0 {
			t.Errorf("expected no request body when question is nil, got %q", body)
		}
		if r.ContentLength > 0 {
			t.Errorf("expected zero content length, got %d", r.ContentLength)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer server.Close()

	client := NewClientWithHTTPClient(config.AIAgentConfig{ServiceURL: server.URL}, server.Client())

	got, err := client.RetrieveO2BarRecommendations(context.Background(), "jwt", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Email != "a@wso2.com" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestRetrieveO2BarRecommendations_WithQuestionSendsBody(t *testing.T) {
	question := "when is the next slot?"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]string
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if got["question"] != question {
			t.Errorf("question = %q, want %q", got["question"], question)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]models.O2BarRecommendationResponse{})
	}))
	defer server.Close()

	client := NewClientWithHTTPClient(config.AIAgentConfig{ServiceURL: server.URL}, server.Client())

	if _, err := client.RetrieveO2BarRecommendations(context.Background(), "jwt", &question); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendProfileInfo_ReturnsRawResponse(t *testing.T) {
	const jwt = "user-jwt-assertion"
	profile := models.PersonalizeAgentUserProfile{Email: "a@wso2.com", Name: "A"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/profile/create" {
			t.Errorf("expected path /profile/create, got %q", r.URL.Path)
		}
		if got := r.Header.Get("x-jwt-assertion"); got != jwt {
			t.Errorf("x-jwt-assertion = %q, want %q", got, jwt)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["override"] != true {
			t.Errorf("override = %v, want true", body["override"])
		}
		user, ok := body["user"].(map[string]any)
		if !ok || user["email"] != "a@wso2.com" {
			t.Errorf("user = %v, want email a@wso2.com", body["user"])
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer server.Close()

	client := NewClientWithHTTPClient(config.AIAgentConfig{ServiceURL: server.URL}, server.Client())

	resp, err := client.SendProfileInfo(context.Background(), jwt, profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != `{"status":"accepted"}` {
		t.Errorf("body = %q, want raw passthrough", got)
	}
}

func TestSendProfileInfo_ClientCallFailureReturnsError(t *testing.T) {
	client := NewClientWithHTTPClient(config.AIAgentConfig{ServiceURL: "http://127.0.0.1:1"}, &http.Client{Timeout: time.Second})

	_, err := client.SendProfileInfo(context.Background(), "jwt", models.PersonalizeAgentUserProfile{})
	if err == nil {
		t.Fatal("expected error when the external service is unreachable")
	}
}

func TestRetrieveAgendaRecommendations_Success(t *testing.T) {
	want := []models.PickedForYouSession{{ID: "s-1", Title: "T"}}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agenda/create" {
			t.Errorf("expected path /agenda/create, got %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "{}" {
			t.Errorf("expected body {}, got %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer server.Close()

	client := NewClientWithHTTPClient(config.AIAgentConfig{ServiceURL: server.URL}, server.Client())

	got, err := client.RetrieveAgendaRecommendations(context.Background(), "jwt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "s-1" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestRetrieveChatResponse_Success(t *testing.T) {
	req := models.ChatRequest{
		History:  []models.ChatHistory{{Question: "q1", Answer: "a1"}},
		Question: "q2",
	}
	want := models.ChatResponse{Response: "hello", Suggestions: []string{"s1"}}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/assistant/chat" {
			t.Errorf("expected path /assistant/chat, got %q", r.URL.Path)
		}
		var got models.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if got.Question != req.Question || len(got.History) != 1 || got.History[0].Question != "q1" {
			t.Errorf("unexpected forwarded request: %+v", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer server.Close()

	client := NewClientWithHTTPClient(config.AIAgentConfig{ServiceURL: server.URL}, server.Client())

	got, err := client.RetrieveChatResponse(context.Background(), "jwt", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Response != "hello" || len(got.Suggestions) != 1 {
		t.Errorf("unexpected result: %+v", got)
	}
}

// TestNewClient_SendsGatewayTokenAndAssertion pins the two-credential contract:
// the caller's JWT keeps travelling in x-jwt-assertion while an OAuth2
// client-credentials token is added in Authorization for the Choreo gateway
// fronting the AI service. Dropping either is a live outage -- without the
// token the gateway answers 401 {"code":"900901"} and every AI route surfaces a
// bare 500; without the assertion the AI service cannot tell who is asking.
func TestNewClient_SendsGatewayTokenAndAssertion(t *testing.T) {
	const jwt = "user-jwt-assertion"

	var gotGrantType, gotTokenAuth string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parsing token request form: %v", err)
		}
		gotGrantType = r.PostFormValue("grant_type")
		gotTokenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"gateway-token","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokenSrv.Close()

	var gotAuth, gotAssertion string
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAssertion = r.Header.Get("x-jwt-assertion")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"response":"hi","suggestions":[]}`)
	}))
	defer aiSrv.Close()

	c := NewClient(config.AIAgentConfig{
		ServiceURL: aiSrv.URL,
		OAuth: config.OAuthClientConfig{
			TokenURL:     tokenSrv.URL,
			ClientID:     "ai-client",
			ClientSecret: "ai-secret",
		},
		RequestTimeout: 10 * time.Second,
	})

	if _, err := c.RetrieveChatResponse(context.Background(), jwt, models.ChatRequest{Question: "q"}); err != nil {
		t.Fatalf("RetrieveChatResponse: %v", err)
	}
	if gotAuth != "Bearer gateway-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer gateway-token")
	}
	if gotAssertion != jwt {
		t.Errorf("x-jwt-assertion = %q, want %q", gotAssertion, jwt)
	}
	if gotGrantType != "client_credentials" {
		t.Errorf("grant_type = %q, want client_credentials", gotGrantType)
	}
	// AuthStyleInHeader: credentials as HTTP Basic on the token request, which
	// is what Asgardeo expects and what the reference gateway sends by hand.
	wantBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("ai-client:ai-secret"))
	if gotTokenAuth != wantBasic {
		t.Errorf("token request Authorization = %q, want %q", gotTokenAuth, wantBasic)
	}
}

// TestNewClient_NoTokenWhenTokenURLEmpty covers an AI service reached directly
// with no gateway in front of it: no token is fetched and no Authorization
// header is sent, so an empty AI_TOKEN_URL stays a working local configuration.
func TestNewClient_NoTokenWhenTokenURLEmpty(t *testing.T) {
	var gotAuth string
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"response":"hi","suggestions":[]}`)
	}))
	defer aiSrv.Close()

	c := NewClient(config.AIAgentConfig{ServiceURL: aiSrv.URL, RequestTimeout: 10 * time.Second})

	if _, err := c.RetrieveChatResponse(context.Background(), "jwt", models.ChatRequest{Question: "q"}); err != nil {
		t.Fatalf("RetrieveChatResponse: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want no header", gotAuth)
	}
}

// A non-2xx from the AI service (or a gateway in front of it) must carry its
// status out of this package, so handlers can name it in the log message
// instead of burying it in an attribute the deployed log viewer hides.
func TestStatusCodeFrom_CarriesUpstreamStatus(t *testing.T) {
	const gatewayBody = `{"error_message":"Invalid Credentials","code":"900901"}`
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, gatewayBody)
	}))
	defer aiSrv.Close()

	c := NewClient(config.AIAgentConfig{ServiceURL: aiSrv.URL, RequestTimeout: 10 * time.Second})

	_, err := c.RetrieveChatResponse(context.Background(), "jwt", models.ChatRequest{Question: "q"})
	if err == nil {
		t.Fatal("expected an error for a 401 from the AI service")
	}
	status, ok := StatusCodeFrom(err)
	if !ok {
		t.Fatalf("StatusCodeFrom did not recognise %v as an upstream status failure", err)
	}
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", status, http.StatusUnauthorized)
	}
	if !strings.Contains(err.Error(), "900901") {
		t.Errorf("error = %q, want it to retain the upstream body", err.Error())
	}
	if _, isTokenFailure := TokenFetchStatusFrom(err); isTokenFailure {
		t.Error("a service 401 must not be reported as a token-fetch failure")
	}
}

// A rejected token request never reaches the AI service, so it must be
// distinguishable from that service being down -- bad credentials will not fix
// themselves, and reporting them as "temporarily unavailable" hides the cause.
func TestTokenFetchStatusFrom_DistinguishesRejectedCredentials(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid_client"}`)
	}))
	defer tokenSrv.Close()

	aiCalled := false
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aiCalled = true
	}))
	defer aiSrv.Close()

	c := NewClient(config.AIAgentConfig{
		ServiceURL: aiSrv.URL,
		OAuth: config.OAuthClientConfig{
			TokenURL:     tokenSrv.URL,
			ClientID:     "ai-client",
			ClientSecret: "wrong-secret",
		},
		RequestTimeout: 10 * time.Second,
	})

	_, err := c.RetrieveChatResponse(context.Background(), "jwt", models.ChatRequest{Question: "q"})
	if err == nil {
		t.Fatal("expected an error when the token endpoint rejects the credentials")
	}
	status, ok := TokenFetchStatusFrom(err)
	if !ok {
		t.Fatalf("TokenFetchStatusFrom did not recognise %v as a token-fetch failure", err)
	}
	if status != http.StatusUnauthorized {
		t.Errorf("token status = %d, want %d", status, http.StatusUnauthorized)
	}
	if _, isServiceStatus := StatusCodeFrom(err); isServiceStatus {
		t.Error("a token-fetch failure must not be reported as an AI service status")
	}
	if aiCalled {
		t.Error("the AI service must not be called when the token request fails")
	}
}

// TestNewClient_SetsTimeoutOnOAuthBranch pins the client-level deadline on the
// branch production actually takes. TestNewClient_SetsTimeout only exercises
// the TokenURL == "" branch, which never runs this code. oauth2.NewClient
// returns a client carrying the *token fetch* client's Timeout, so dropping the
// explicit assignment does not leave the AI call unbounded in an obvious way --
// it silently gives it the 15s token budget instead, failing every AI answer
// that legitimately takes longer, which is most of them.
func TestNewClient_SetsTimeoutOnOAuthBranch(t *testing.T) {
	c := NewClient(config.AIAgentConfig{
		ServiceURL: "https://ai.example.com",
		OAuth: config.OAuthClientConfig{
			TokenURL:     "https://api.asgardeo.io/t/wso2/oauth2/token",
			ClientID:     "ai-client",
			ClientSecret: "ai-secret",
		},
		RequestTimeout: 45 * time.Second,
	})
	if c.httpClient.Timeout != 45*time.Second {
		t.Errorf("httpClient.Timeout = %v, want 45s (the AI request budget, not the %v token-fetch budget)",
			c.httpClient.Timeout, tokenFetchTimeout)
	}
}

// A non-positive AI_REQUEST_TIMEOUT_SECONDS must not produce a client with no
// deadline at all: http.Client reads Timeout <= 0 as "wait forever", so a
// stalled AI service would pin the request past the server's write deadline and
// truncate the response mid-write rather than failing cleanly. Both
// construction branches are reachable with a misconfigured value, so both are
// checked.
func TestNewClient_NonPositiveTimeoutFallsBackToDefault(t *testing.T) {
	behindGateway := config.OAuthClientConfig{
		TokenURL:     "https://api.asgardeo.io/t/wso2/oauth2/token",
		ClientID:     "ai-client",
		ClientSecret: "ai-secret",
	}
	tests := []struct {
		name    string
		timeout time.Duration
		oauth   config.OAuthClientConfig
	}{
		{"zero, addressed directly", 0, config.OAuthClientConfig{}},
		{"negative, addressed directly", -1 * time.Second, config.OAuthClientConfig{}},
		{"zero, behind the gateway", 0, behindGateway},
		{"negative, behind the gateway", -1 * time.Second, behindGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient(config.AIAgentConfig{
				ServiceURL:     "https://ai.example.com",
				OAuth:          tt.oauth,
				RequestTimeout: tt.timeout,
			})
			if c.httpClient.Timeout != defaultRequestTimeout {
				t.Errorf("httpClient.Timeout = %v, want the %v backstop", c.httpClient.Timeout, defaultRequestTimeout)
			}
		})
	}
}

// --- Admin AI-management calls -------------------------------------------

// adminRoundTrip captures what one admin call put on the wire so a single table
// can assert method, path, the email query param, the x-jwt-assertion header and
// whether a body was sent (only POST/PATCH should send one).
type adminRoundTrip struct {
	method    string
	path      string
	rawQuery  string
	assertion string
	bodyLen   int
	hasCT     bool
}

func TestAdminCalls_WireContract(t *testing.T) {
	const jwt = "admin-jwt-assertion"

	tests := []struct {
		name       string
		respStatus int
		respBody   string
		call       func(c *Client) error
		wantMethod string
		wantPath   string
		wantEmail  string
		wantBody   bool
	}{
		{
			name:       "CreateEngineer",
			respStatus: http.StatusCreated,
			respBody:   `{"id":"e1","email":"eng@wso2.com","name":"Eng","created":true,"message":"added"}`,
			call: func(c *Client) error {
				got, err := c.CreateEngineer(context.Background(), jwt, models.EngineerCreateRequest{
					Engineer: models.EngineerProfileInput{Email: "eng@wso2.com", Name: "Eng"},
					Override: true,
				})
				if err != nil {
					return err
				}
				if got.ID != "e1" || !got.Created {
					t.Errorf("decoded = %+v, want id e1 created true", got)
				}
				return nil
			},
			wantMethod: http.MethodPost, wantPath: "/engineer/create", wantBody: true,
		},
		{
			name:       "ListEngineers",
			respStatus: http.StatusOK,
			respBody:   `[{"email":"eng@wso2.com","name":"Eng","title":"SE","linkedInProfileUrl":"","availableTimeSlots":[{"startTime":"2026-09-22T09:00:00+03:00","endTime":"2026-09-22T10:00:00+03:00"}]}]`,
			call: func(c *Client) error {
				got, err := c.ListEngineers(context.Background(), jwt)
				if err != nil {
					return err
				}
				if len(got) != 1 || got[0].Email != "eng@wso2.com" || len(got[0].AvailableTimeSlots) != 1 {
					t.Errorf("decoded = %+v, want one engineer with one slot", got)
				}
				return nil
			},
			wantMethod: http.MethodGet, wantPath: "/engineers", wantBody: false,
		},
		{
			name:       "DeleteEngineer",
			respStatus: http.StatusNoContent,
			call: func(c *Client) error {
				return c.DeleteEngineer(context.Background(), jwt, "eng@wso2.com")
			},
			wantMethod: http.MethodDelete, wantPath: "/engineers", wantEmail: "eng@wso2.com", wantBody: false,
		},
		{
			name:       "EngineerExists",
			respStatus: http.StatusOK,
			respBody:   `{"exists":true}`,
			call: func(c *Client) error {
				got, err := c.EngineerExists(context.Background(), jwt, "eng@wso2.com")
				if err != nil {
					return err
				}
				if !got.Exists {
					t.Errorf("exists = false, want true")
				}
				return nil
			},
			wantMethod: http.MethodGet, wantPath: "/engineer/exists", wantEmail: "eng@wso2.com", wantBody: false,
		},
		{
			name:       "AdminCreateProfile",
			respStatus: http.StatusCreated,
			respBody:   `{"id":"p1","email":"a@wso2.com","name":"A","company":"WSO2","title":"SE","created":true,"message":"ok"}`,
			call: func(c *Client) error {
				got, err := c.AdminCreateProfile(context.Background(), jwt, models.AdminProfileCreateRequest{
					User:     models.PersonalizeAgentUserProfile{Email: "a@wso2.com", Name: "A"},
					Override: true,
				})
				if err != nil {
					return err
				}
				if got.ID != "p1" || got.Company != "WSO2" {
					t.Errorf("decoded = %+v, want id p1 company WSO2", got)
				}
				return nil
			},
			wantMethod: http.MethodPost, wantPath: "/profile/create", wantBody: true,
		},
		{
			name:       "AdminGetProfile",
			respStatus: http.StatusOK,
			respBody:   `{"email":"a@wso2.com","document":"researched text"}`,
			call: func(c *Client) error {
				got, err := c.AdminGetProfile(context.Background(), jwt, "a@wso2.com")
				if err != nil {
					return err
				}
				if got["document"] != "researched text" {
					t.Errorf("decoded = %+v, want document field", got)
				}
				return nil
			},
			wantMethod: http.MethodGet, wantPath: "/profile", wantEmail: "a@wso2.com", wantBody: false,
		},
		{
			name:       "AdminUpdateProfile",
			respStatus: http.StatusOK,
			respBody:   `{"email":"a@wso2.com","message":"updated"}`,
			call: func(c *Client) error {
				got, err := c.AdminUpdateProfile(context.Background(), jwt, "a@wso2.com", models.AdminProfileUpdateRequest{LinkedInInfo: "fresh"})
				if err != nil {
					return err
				}
				if got["message"] != "updated" {
					t.Errorf("decoded = %+v, want updated message", got)
				}
				return nil
			},
			wantMethod: http.MethodPatch, wantPath: "/profile", wantEmail: "a@wso2.com", wantBody: true,
		},
		{
			name:       "AdminDeleteProfile",
			respStatus: http.StatusNoContent,
			call: func(c *Client) error {
				return c.AdminDeleteProfile(context.Background(), jwt, "a@wso2.com")
			},
			wantMethod: http.MethodDelete, wantPath: "/profile", wantEmail: "a@wso2.com", wantBody: false,
		},
		{
			name:       "ProfileExists",
			respStatus: http.StatusOK,
			respBody:   `{"exists":false}`,
			call: func(c *Client) error {
				got, err := c.ProfileExists(context.Background(), jwt, "a@wso2.com")
				if err != nil {
					return err
				}
				if got.Exists {
					t.Errorf("exists = true, want false")
				}
				return nil
			},
			wantMethod: http.MethodGet, wantPath: "/profile/exists", wantEmail: "a@wso2.com", wantBody: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got adminRoundTrip
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				got = adminRoundTrip{
					method:    r.Method,
					path:      r.URL.Path,
					rawQuery:  r.URL.Query().Get("email"),
					assertion: r.Header.Get("x-jwt-assertion"),
					bodyLen:   len(body),
					hasCT:     r.Header.Get("Content-Type") != "",
				}
				w.WriteHeader(tc.respStatus)
				if tc.respBody != "" {
					_, _ = w.Write([]byte(tc.respBody))
				}
			}))
			defer server.Close()

			client := NewClientWithHTTPClient(config.AIAgentConfig{ServiceURL: server.URL}, server.Client())
			if err := tc.call(client); err != nil {
				t.Fatalf("call returned error: %v", err)
			}

			if got.method != tc.wantMethod {
				t.Errorf("method = %q, want %q", got.method, tc.wantMethod)
			}
			if got.path != tc.wantPath {
				t.Errorf("path = %q, want %q", got.path, tc.wantPath)
			}
			if got.rawQuery != tc.wantEmail {
				t.Errorf("email query = %q, want %q", got.rawQuery, tc.wantEmail)
			}
			if got.assertion != jwt {
				t.Errorf("x-jwt-assertion = %q, want %q", got.assertion, jwt)
			}
			if tc.wantBody {
				if got.bodyLen == 0 {
					t.Errorf("expected a request body, got none")
				}
				if !got.hasCT {
					t.Errorf("expected Content-Type on a body request")
				}
			} else {
				if got.bodyLen != 0 {
					t.Errorf("expected no request body, got %d bytes", got.bodyLen)
				}
				if got.hasCT {
					t.Errorf("expected no Content-Type on a bodyless request")
				}
			}
		})
	}
}

// TestAdminCalls_Non2xxIsStatusError proves every admin call surfaces a non-2xx
// as a *StatusError carrying the code, which is what lets the handler tell a
// con-ai 400/404 (relay) from a gateway 401/403 (credential fault).
func TestAdminCalls_Non2xxIsStatusError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		call   func(c *Client) error
	}{
		{
			name:   "CreateEngineer 400",
			status: http.StatusBadRequest,
			body:   `{"detail":"Each time slot needs a date"}`,
			call: func(c *Client) error {
				_, err := c.CreateEngineer(context.Background(), "jwt", models.EngineerCreateRequest{})
				return err
			},
		},
		{
			name:   "DeleteEngineer 404",
			status: http.StatusNotFound,
			body:   `{"detail":"Engineer not found"}`,
			call: func(c *Client) error {
				return c.DeleteEngineer(context.Background(), "jwt", "nope@wso2.com")
			},
		},
		{
			name:   "AdminGetProfile 404",
			status: http.StatusNotFound,
			body:   `{"detail":"Profile not found"}`,
			call: func(c *Client) error {
				_, err := c.AdminGetProfile(context.Background(), "jwt", "nope@wso2.com")
				return err
			},
		},
		{
			name:   "ListEngineers gateway 401",
			status: http.StatusUnauthorized,
			body:   `{"code":"900901","error_message":"Invalid Credentials"}`,
			call: func(c *Client) error {
				_, err := c.ListEngineers(context.Background(), "jwt")
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client := NewClientWithHTTPClient(config.AIAgentConfig{ServiceURL: server.URL}, server.Client())
			err := tc.call(client)
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			code, ok := StatusCodeFrom(err)
			if !ok {
				t.Fatalf("error is not a *StatusError: %v", err)
			}
			if code != tc.status {
				t.Errorf("status = %d, want %d", code, tc.status)
			}
		})
	}
}
