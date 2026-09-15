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

// Package aiagent provides an HTTP client for the external AI agent service.
// Matchmaking, personalize, picked-for-you and chat are one consolidated
// service serving all of those paths off a single root with no path prefix,
// so every call in this package shares one base URL.
//
// Every request carries two credentials, answering two different questions:
// x-jwt-assertion is the caller's own JWT forwarded verbatim, which is how the
// AI service knows which attendee is asking; Authorization is this service's
// own OAuth2 client-credentials token, which is for the Choreo gateway in front
// of the AI service. The AI service authenticates nobody -- the token never
// reaches it -- but its gateway rejects a tokenless call outright. See
// config.AIAgentConfig.
package aiagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"wso2-coin-backend/internal/config"
	"wso2-coin-backend/internal/models"
)

// maxErrBodyBytes caps how much of an error response body we read into an
// error message, so a huge/unexpected body doesn't blow up logs.
const maxErrBodyBytes = 2048

// maxDrainBytes caps how much of a response body is read purely to finish it.
// net/http returns a connection to the keep-alive pool only once its body has
// been read to EOF, and every path here stops early -- the error path at
// maxErrBodyBytes, the success path wherever the first JSON value ends -- so
// without this the connection is dropped instead of reused. That matters
// because these calls go through a managed gateway that meters connections.
//
// It is bounded rather than an open io.Copy so a runaway or hostile upstream
// cannot make this backend read for as long as it cares to send: 64 KiB is far
// beyond anything con-ai answers with, and a body past it simply is not worth
// reading, so we stop and let Close drop that one connection exactly as it did
// before.
const maxDrainBytes = 64 * 1024

// drainBody consumes what is left of body so its connection can be reused.
// Bounded on purpose -- see maxDrainBytes.
func drainBody(body io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrainBytes))
}

const (
	// tokenFetchTimeout bounds the OAuth2 token fetch, deliberately separate
	// from cfg.RequestTimeout. The two measure different things: an AI answer
	// legitimately takes a minute (AI_REQUEST_TIMEOUT_SECONDS defaults to 120),
	// while a token endpoint that has not answered in fifteen seconds is not
	// going to. Spending the AI budget on the token fetch is what turns one
	// unreachable token endpoint into minutes of blocked requests, because the
	// fetch is serialized -- see failCachingTokenSource.
	tokenFetchTimeout = 15 * time.Second

	// tokenFailureTTL is how long a failed token fetch is remembered. Long
	// enough that a burst of concurrent requests pays for exactly one fetch,
	// short enough that a token endpoint coming back is noticed on the next
	// request rather than after a cooldown the user can feel.
	tokenFailureTTL = 5 * time.Second

	// defaultRequestTimeout backstops a non-positive cfg.RequestTimeout.
	// http.Client reads Timeout <= 0 as "no timeout at all", so a misconfigured
	// AI_REQUEST_TIMEOUT_SECONDS would otherwise produce a client that hangs on
	// the AI service forever and holds the connection past the server's write
	// deadline. Config validation should catch that first; this makes the
	// client safe on its own regardless.
	defaultRequestTimeout = 120 * time.Second
)

// StatusError is returned when the AI service, or a gateway in front of it,
// answers with a non-2xx status. It carries the status out of this package so
// handlers can log it in the message itself rather than burying it in an
// attribute -- the deployed log viewer renders only the message, which is how a
// gateway 401 spent a day looking like an unexplained 500.
type StatusError struct {
	StatusCode int
	// Method is the HTTP verb of the failed request. The admin routes call the
	// AI service with GET/PATCH/DELETE as well as POST, so a hardcoded verb in
	// Error() would misname which operation failed. Empty falls back to POST,
	// which is what every construction site outside doAdminJSON sends.
	Method string
	// URL is the request URL with every query value redacted -- see
	// redactedURL. Never assign a raw req.URL.String() here: this value is
	// logged and rendered into Error(), and the admin routes carry an
	// attendee's email in the query string.
	URL  string
	Body string
}

func (e *StatusError) Error() string {
	method := e.Method
	if method == "" {
		method = http.MethodPost
	}
	return fmt.Sprintf("%s %s returned status %d: %s", method, e.URL, e.StatusCode, e.Body)
}

// redactedURL renders u for logs and error messages with every query value
// replaced by a placeholder, keeping the keys.
//
// The admin AI routes pass the attendee's or engineer's email as ?email=, and a
// StatusError's URL reaches both the log store and the error message. Any admin
// could otherwise force a 404 and persist an email in logs that RBAC no longer
// protects. Keeping the keys preserves the only debugging signal the query
// carried -- which parameter was sent -- without the value itself.
func redactedURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.RawQuery == "" {
		return u.String()
	}
	sanitized := *u
	q := u.Query()
	for k := range q {
		q.Set(k, "REDACTED")
	}
	sanitized.RawQuery = q.Encode()
	return sanitized.String()
}

// StatusCodeFrom reports the upstream HTTP status carried by err, and whether
// err was an upstream status failure at all.
func StatusCodeFrom(err error) (int, bool) {
	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode, true
	}
	return 0, false
}

// TokenFetchStatusFrom reports the status the OAuth2 token endpoint answered
// with, and whether err was a token-fetch failure at all.
//
// This failure never reaches the AI service: the oauth2 transport gives up
// before sending the request, so it presents as a transport error even though
// nothing about the AI service is wrong. Telling the two apart is what stops a
// rejected client secret from being reported as "AI service unavailable".
func TokenFetchStatusFrom(err error) (int, bool) {
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) {
		if retrieveErr.Response != nil {
			return retrieveErr.Response.StatusCode, true
		}
		// Unreachable in practice: oauth2 builds a RetrieveError only after a
		// successful round trip, so Response is always set. Kept so a future
		// oauth2 that constructs one differently still reports a token-fetch
		// failure rather than silently reading as an AI service error.
		return 0, true
	}
	return 0, false
}

// redactedRequestError wraps a transport failure from http.Client.Do with a
// request URL whose query values are redacted.
//
// Rebuilding the *url.Error is what makes that stick: Do returns one carrying
// the raw URL in its own message, so redacting only the format argument would
// leave the email visible in the wrapped error's text. The replacement keeps
// both the *url.Error type and the inner cause, which is what handlers match on
// to tell "couldn't reach the AI service" (a retriable 503) from a rejected
// OAuth2 token, so the classification is unchanged.
func redactedRequestError(req *http.Request, err error) error {
	safeURL := redactedURL(req.URL)
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = &url.Error{Op: urlErr.Op, URL: safeURL, Err: urlErr.Err}
	}
	return fmt.Errorf("request to %s failed: %w", safeURL, err)
}

// Client is an HTTP client for the external AI agent service.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient builds a production Client bounded by cfg.RequestTimeout.
//
// With cfg.OAuth.TokenURL set, every request carries an OAuth2
// client-credentials token for the Choreo gateway fronting the AI service. The
// token is fetched lazily on the first call, then cached and refreshed by the
// oauth2 transport. AuthStyleInHeader presents the client id and secret as HTTP
// Basic at the token endpoint, which is what Asgardeo expects and what the
// push-notification gateway does by hand. Pinning it is documentation rather
// than optimisation: left unset, the library's auto-detect already tries Basic
// first and only falls back to in-body credentials if that errors, so against
// Asgardeo there is no probe request to save. Saying it outright means the
// contract survives a future where the fallback would silently take over.
//
// An empty TokenURL means no gateway -- the AI service addressed directly, as
// when it runs on localhost -- and no Authorization header is sent. Either way
// x-jwt-assertion still travels on every request: the gateway token identifies
// this service, never the attendee.
func NewClient(cfg config.AIAgentConfig) *Client {
	timeout := requestTimeout(cfg)

	if cfg.OAuth.TokenURL == "" {
		return NewClientWithHTTPClient(cfg, &http.Client{Timeout: timeout})
	}

	oauthCfg := clientcredentials.Config{
		ClientID:     cfg.OAuth.ClientID,
		ClientSecret: cfg.OAuth.ClientSecret,
		TokenURL:     cfg.OAuth.TokenURL,
		Scopes:       cfg.OAuth.Scopes,
		AuthStyle:    oauth2.AuthStyleInHeader,
	}
	// The token fetch gets its own, much smaller budget via oauth2.HTTPClient
	// rather than reusing the AI request budget. oauth2.Transport fetches the
	// token before the AI request is sent, under a background context this
	// client's Timeout cannot reach, so the AI budget spent here is time no
	// caller can cancel: at the default 120s a stalled token endpoint would
	// pin a request past the server's 130s write deadline and truncate the
	// response mid-write.
	tokenFetchCtx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: tokenFetchTimeout})
	tokenSource := &failCachingTokenSource{base: oauthCfg.TokenSource(tokenFetchCtx)}
	httpClient := oauth2.NewClient(tokenFetchCtx, tokenSource)
	// oauth2.NewClient copies the token-fetch client's Timeout onto the
	// returned client; the AI call is the thing it actually bounds, so set it.
	httpClient.Timeout = timeout
	return NewClientWithHTTPClient(cfg, httpClient)
}

// requestTimeout is the deadline for a call to the AI service, guarding against
// a non-positive configured value which http.Client reads as no deadline at
// all. See defaultRequestTimeout.
func requestTimeout(cfg config.AIAgentConfig) time.Duration {
	if cfg.RequestTimeout <= 0 {
		return defaultRequestTimeout
	}
	return cfg.RequestTimeout
}

// failCachingTokenSource remembers a *failed* token fetch for tokenFailureTTL
// so a queue of concurrent requests does not each retry it in turn.
//
// oauth2's ReuseTokenSource holds one mutex for the whole fetch, and
// oauth2.Transport calls it before the AI request exists, so a request waiting
// its turn is not released by its own deadline -- it waits for every fetch
// ahead of it. With a hanging token endpoint that made five concurrent
// requests take five fetch timeouts end to end, the last one answering long
// after the client that asked had given up.
//
// Only failures are cached here. A successful fetch is handed straight back and
// its caching, expiry and refresh remain entirely the underlying
// ReuseTokenSource's job. The error is stored and returned by value so
// TokenFetchStatusFrom -- and errors.As for *oauth2.RetrieveError generally --
// still recognises it, which is what keeps a rejected client secret reported as
// rejected credentials instead of "AI service unavailable".
type failCachingTokenSource struct {
	base oauth2.TokenSource

	// mu is held across the fetch on purpose: ReuseTokenSource already
	// serializes it, so this adds no contention, and it means a caller that
	// waited behind an in-flight fetch sees that fetch's cached failure the
	// moment it acquires the lock rather than starting another one.
	mu       sync.Mutex
	lastErr  error
	failedAt time.Time
}

func (s *failCachingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastErr != nil && time.Since(s.failedAt) < tokenFailureTTL {
		return nil, s.lastErr
	}

	tok, err := s.base.Token()
	if err != nil {
		s.lastErr, s.failedAt = err, time.Now()
		return nil, err
	}
	s.lastErr = nil
	return tok, nil
}

// NewClientWithHTTPClient builds a Client using httpClient directly. This is
// intended for tests, where httpClient is typically an httptest.Server's
// client, but is also how NewClient assembles the production client.
func NewClientWithHTTPClient(cfg config.AIAgentConfig, httpClient *http.Client) *Client {
	return &Client{
		baseURL:    cfg.ServiceURL,
		httpClient: httpClient,
	}
}

// RetrieveMatches fetches recommended matches for the caller via
// POST {aiServiceURL}/networking/recommend, body {}.
func (c *Client) RetrieveMatches(ctx context.Context, jwtAssertion string) ([]models.RecommendedUser, error) {
	var out []models.RecommendedUser
	if err := c.postJSON(ctx, "networking/recommend", jwtAssertion, struct{}{}, &out); err != nil {
		return nil, fmt.Errorf("aiagent: retrieving matches: %w", err)
	}
	return out, nil
}

// RetrieveO2BarRecommendations fetches O2Bar recommendations for the caller
// via POST {aiServiceURL}/o2bar/recommend. When question is nil, no
// request body is sent at all -- not even "{}" -- matching the old
// `question is string ? {question} : ()` exactly.
func (c *Client) RetrieveO2BarRecommendations(ctx context.Context, jwtAssertion string, question *string) ([]models.O2BarRecommendationResponse, error) {
	var body any
	if question != nil {
		body = map[string]string{"question": *question}
	}

	var out []models.O2BarRecommendationResponse
	if err := c.postJSONOrNoBody(ctx, "o2bar/recommend", jwtAssertion, body, &out); err != nil {
		return nil, fmt.Errorf("aiagent: retrieving O2Bar recommendations: %w", err)
	}
	return out, nil
}

// SendProfileInfo forwards profile to the external AI agent service via
// POST {aiServiceURL}/profile/create, body
// {"override": true, "user": profile}. It returns the raw *http.Response
// for the caller to copy through untouched (status, headers, body) --
// matching the old raw-passthrough behavior exactly. The caller must close
// the response body.
func (c *Client) SendProfileInfo(ctx context.Context, jwtAssertion string, profile models.PersonalizeAgentUserProfile) (*http.Response, error) {
	payload := map[string]any{"override": true, "user": profile}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("aiagent: encoding profile payload: %w", err)
	}

	req, err := c.newRequest(ctx, "profile/create", jwtAssertion, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("aiagent: sending profile info: %w", err)
	}
	return resp, nil
}

// RetrieveAgendaRecommendations fetches personalized "Picked for You" agenda
// recommendations via POST {aiServiceURL}/agenda/create, body {}.
// The external service returns fully-formed session objects itself -- no DB
// enrichment happens here.
func (c *Client) RetrieveAgendaRecommendations(ctx context.Context, jwtAssertion string) ([]models.PickedForYouSession, error) {
	var out []models.PickedForYouSession
	if err := c.postJSON(ctx, "agenda/create", jwtAssertion, struct{}{}, &out); err != nil {
		return nil, fmt.Errorf("aiagent: retrieving agenda recommendations: %w", err)
	}
	return out, nil
}

// RetrieveChatResponse forwards req to the external AI agent service via
// POST {aiServiceURL}/assistant/chat, body = the whole request.
func (c *Client) RetrieveChatResponse(ctx context.Context, jwtAssertion string, req models.ChatRequest) (*models.ChatResponse, error) {
	var out models.ChatResponse
	if err := c.postJSON(ctx, "assistant/chat", jwtAssertion, req, &out); err != nil {
		return nil, fmt.Errorf("aiagent: retrieving chat response: %w", err)
	}
	return &out, nil
}

// postJSON sends body (always JSON-encoded, even if empty) and decodes the
// response into out.
func (c *Client) postJSON(ctx context.Context, path, jwtAssertion string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encoding request body: %w", err)
	}
	return c.doJSON(ctx, path, jwtAssertion, bytes.NewReader(b), out)
}

// postJSONOrNoBody sends body JSON-encoded, or no request body at all when
// body is nil, and decodes the response into out.
func (c *Client) postJSONOrNoBody(ctx context.Context, path, jwtAssertion string, body, out any) error {
	if body == nil {
		return c.doJSON(ctx, path, jwtAssertion, nil, out)
	}
	return c.postJSON(ctx, path, jwtAssertion, body, out)
}

func (c *Client) doJSON(ctx context.Context, path, jwtAssertion string, bodyReader io.Reader, out any) error {
	req, err := c.newRequest(ctx, path, jwtAssertion, bodyReader)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return redactedRequestError(req, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodyBytes))
		// Only the first maxErrBodyBytes reach the message; drain the rest so
		// the connection survives the failure.
		drainBody(resp.Body)
		return &StatusError{StatusCode: resp.StatusCode, Method: req.Method, URL: redactedURL(req.URL), Body: string(errBody)}
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding response body: %w", err)
	}
	// Decode stops at the end of the first JSON value, so anything after it --
	// trailing whitespace, chunked framing -- is still unread.
	drainBody(resp.Body)
	return nil
}

func (c *Client) newRequest(ctx context.Context, path, jwtAssertion string, bodyReader io.Reader) (*http.Request, error) {
	reqURL, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return nil, fmt.Errorf("building URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-jwt-assertion", jwtAssertion)
	return req, nil
}

// --- Admin AI-management calls -------------------------------------------
//
// These proxy con-ai's engineer-roster and attendee-profile *write* endpoints,
// which the attendee-facing features only ever read from. Unlike the recommend/
// chat calls above they are not all POST -- listing and existence probes are GET,
// removal is DELETE, a LinkedIn refresh is PATCH -- and several key on an `email`
// query parameter rather than a body. They share the same credentials as every
// other call: x-jwt-assertion carries the (admin) caller and Authorization carries
// this service's gateway token; con-ai still authenticates nobody, so a 401/403 on
// these hops is the gateway refusing this service, never the admin's JWT. Every
// non-2xx is returned as a *StatusError so the handler can tell a con-ai 400/404
// (relay verbatim) from a gateway 401/403 (a credential fault of this backend).

// CreateEngineer registers or, with req.Override, overwrites an O2Bar engineer
// via POST engineer/create. A 201 carries EngineerResponse; note con-ai also
// answers 201 with Created=false when the engineer already existed and Override
// was not set.
func (c *Client) CreateEngineer(ctx context.Context, jwtAssertion string, req models.EngineerCreateRequest) (*models.EngineerResponse, error) {
	var out models.EngineerResponse
	if err := c.doAdminJSON(ctx, http.MethodPost, "engineer/create", nil, jwtAssertion, req, &out); err != nil {
		return nil, fmt.Errorf("aiagent: creating engineer: %w", err)
	}
	return &out, nil
}

// ListEngineers returns the O2Bar staff directory via GET engineers. It sends no
// request body.
func (c *Client) ListEngineers(ctx context.Context, jwtAssertion string) ([]models.EngineerSummary, error) {
	var out []models.EngineerSummary
	if err := c.doAdminJSON(ctx, http.MethodGet, "engineers", nil, jwtAssertion, nil, &out); err != nil {
		return nil, fmt.Errorf("aiagent: listing engineers: %w", err)
	}
	return out, nil
}

// DeleteEngineer removes the engineer keyed on email via DELETE engineers?email=.
// con-ai answers 204 on success and 404 when no engineer has that email; a 404
// surfaces as a *StatusError for the handler to translate. Nothing is decoded.
func (c *Client) DeleteEngineer(ctx context.Context, jwtAssertion, email string) error {
	q := url.Values{"email": {email}}
	if err := c.doAdminJSON(ctx, http.MethodDelete, "engineers", q, jwtAssertion, nil, nil); err != nil {
		return fmt.Errorf("aiagent: deleting engineer: %w", err)
	}
	return nil
}

// EngineerExists reports whether email belongs to a registered O2Bar engineer
// via GET engineer/exists?email=.
func (c *Client) EngineerExists(ctx context.Context, jwtAssertion, email string) (*models.ExistsResponse, error) {
	q := url.Values{"email": {email}}
	var out models.ExistsResponse
	if err := c.doAdminJSON(ctx, http.MethodGet, "engineer/exists", q, jwtAssertion, nil, &out); err != nil {
		return nil, fmt.Errorf("aiagent: checking engineer exists: %w", err)
	}
	return &out, nil
}

// AdminCreateProfile registers or, with req.Override, overwrites an arbitrary
// attendee's researched profile via POST profile/create. The email is inside
// req.User and is not derived from the caller, which is what makes this an admin
// operation distinct from POST /users/profile.
func (c *Client) AdminCreateProfile(ctx context.Context, jwtAssertion string, req models.AdminProfileCreateRequest) (*models.ProfileResponse, error) {
	var out models.ProfileResponse
	if err := c.doAdminJSON(ctx, http.MethodPost, "profile/create", nil, jwtAssertion, req, &out); err != nil {
		return nil, fmt.Errorf("aiagent: creating profile: %w", err)
	}
	return &out, nil
}

// AdminGetProfile fetches the stored profile document for email via
// GET profile?email=. con-ai returns an open document, so it is decoded into a
// map rather than a fixed struct; a 404 surfaces as a *StatusError.
func (c *Client) AdminGetProfile(ctx context.Context, jwtAssertion, email string) (map[string]any, error) {
	q := url.Values{"email": {email}}
	var out map[string]any
	if err := c.doAdminJSON(ctx, http.MethodGet, "profile", q, jwtAssertion, nil, &out); err != nil {
		return nil, fmt.Errorf("aiagent: getting profile: %w", err)
	}
	return out, nil
}

// AdminUpdateProfile re-embeds an attendee's profile around fresh LinkedIn text
// via PATCH profile?email=. Like the GET, the response is an open document.
func (c *Client) AdminUpdateProfile(ctx context.Context, jwtAssertion, email string, req models.AdminProfileUpdateRequest) (map[string]any, error) {
	q := url.Values{"email": {email}}
	var out map[string]any
	if err := c.doAdminJSON(ctx, http.MethodPatch, "profile", q, jwtAssertion, req, &out); err != nil {
		return nil, fmt.Errorf("aiagent: updating profile: %w", err)
	}
	return out, nil
}

// AdminDeleteProfile removes an attendee's profile via DELETE profile?email=.
// con-ai answers 204 on success and 404 when none exists; nothing is decoded.
func (c *Client) AdminDeleteProfile(ctx context.Context, jwtAssertion, email string) error {
	q := url.Values{"email": {email}}
	if err := c.doAdminJSON(ctx, http.MethodDelete, "profile", q, jwtAssertion, nil, nil); err != nil {
		return fmt.Errorf("aiagent: deleting profile: %w", err)
	}
	return nil
}

// ProfileExists reports whether email has a stored attendee profile via
// GET profile/exists?email=.
func (c *Client) ProfileExists(ctx context.Context, jwtAssertion, email string) (*models.ExistsResponse, error) {
	q := url.Values{"email": {email}}
	var out models.ExistsResponse
	if err := c.doAdminJSON(ctx, http.MethodGet, "profile/exists", q, jwtAssertion, nil, &out); err != nil {
		return nil, fmt.Errorf("aiagent: checking profile exists: %w", err)
	}
	return &out, nil
}

// doAdminJSON is the shared transport for the admin AI-management calls. It
// generalizes doJSON along the two axes those calls need and the recommend/chat
// calls never did: an arbitrary HTTP method and an optional query string. body is
// JSON-encoded when non-nil and omitted entirely otherwise -- a GET or DELETE
// sends no body, matching what con-ai's routes expect. out may be nil for a call
// whose success carries no body (a 204 from DELETE), in which case a 2xx response
// body is drained and discarded rather than decoded. Every non-2xx becomes a
// *StatusError, exactly as doJSON does, so a con-ai 400/404 and a gateway 401/403
// stay distinguishable at the handler.
func (c *Client) doAdminJSON(ctx context.Context, method, path string, query url.Values, jwtAssertion string, body, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := c.newRequestWithMethod(ctx, method, path, query, jwtAssertion, bodyReader)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return redactedRequestError(req, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodyBytes))
		// Only the first maxErrBodyBytes reach the message; drain the rest so
		// the connection survives the failure.
		drainBody(resp.Body)
		return &StatusError{StatusCode: resp.StatusCode, Method: req.Method, URL: redactedURL(req.URL), Body: string(errBody)}
	}

	if out == nil {
		// A body-less success (204). Drain so the connection can be reused, but
		// decode nothing.
		drainBody(resp.Body)
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding response body: %w", err)
	}
	// Decode stops at the end of the first JSON value, so anything after it --
	// trailing whitespace, chunked framing -- is still unread.
	drainBody(resp.Body)
	return nil
}

// newRequestWithMethod builds a request for an arbitrary method with an optional
// query string, and only sets Content-Type when there is a body to describe.
// newRequest stays the POST-and-always-a-body path the recommend/chat calls rely
// on; this is its generalization for the admin calls, sharing the same URL join,
// Accept and x-jwt-assertion handling so the two cannot drift apart.
func (c *Client) newRequestWithMethod(ctx context.Context, method, path string, query url.Values, jwtAssertion string, bodyReader io.Reader) (*http.Request, error) {
	reqURL, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return nil, fmt.Errorf("building URL: %w", err)
	}
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("x-jwt-assertion", jwtAssertion)
	return req, nil
}
