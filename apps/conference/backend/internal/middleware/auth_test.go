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
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func unverifiedToken(t *testing.T, claims jwtClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte("test-signing-key-unused-by-unverified-parser"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

func newRouter(cfg AuthConfig) *gin.Engine {
	r := gin.New()
	r.Use(Auth(cfg))
	r.GET("/ping", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return r
}

func TestAuth_MissingHeader_Returns401(t *testing.T) {
	r := newRouter(AuthConfig{})
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuth_UnverifiedMode_DecodesValidToken(t *testing.T) {
	r := gin.New()
	r.Use(Auth(AuthConfig{TokenValidatorEnabled: false}))

	var got *UserInfo
	r.GET("/ping", func(c *gin.Context) {
		got = UserInfoFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	claims := jwtClaims{
		Email: "attendee@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "user-uuid-123",
		},
	}
	token := unverifiedToken(t, claims)

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(jwtAssertionHeader, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got == nil {
		t.Fatal("expected UserInfo to be set in context")
	}
	if got.Email != "attendee@example.com" {
		t.Errorf("Email = %q, want attendee@example.com", got.Email)
	}
	if got.UserID != "user-uuid-123" {
		t.Errorf("UserID = %q, want user-uuid-123", got.UserID)
	}
	if got.RawToken != token {
		t.Errorf("RawToken = %q, want the literal incoming header value %q", got.RawToken, token)
	}
}

func TestAuth_UnverifiedMode_ExtractsGroupsClaim(t *testing.T) {
	r := gin.New()
	r.Use(Auth(AuthConfig{TokenValidatorEnabled: false}))

	var got *UserInfo
	r.GET("/ping", func(c *gin.Context) {
		got = UserInfoFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	token := unverifiedToken(t, jwtClaims{
		Email:            "admin@example.com",
		Groups:           []string{"wso2-everyone", "app-con-registrant-admin"},
		RegisteredClaims: jwt.RegisteredClaims{Subject: "user-uuid-123"},
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(jwtAssertionHeader, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got == nil {
		t.Fatal("expected UserInfo to be set in context")
	}
	if len(got.Groups) != 2 || got.Groups[0] != "wso2-everyone" {
		t.Errorf("Groups = %v, want [wso2-everyone app-con-registrant-admin]", got.Groups)
	}
}

// The groups claim is optional: only admin-gated routes read it, and those
// deny on an empty list, so its absence must not reject the token.
func TestAuth_UnverifiedMode_MissingGroupsClaimIsAllowed(t *testing.T) {
	r := gin.New()
	r.Use(Auth(AuthConfig{TokenValidatorEnabled: false}))

	var got *UserInfo
	r.GET("/ping", func(c *gin.Context) {
		got = UserInfoFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	token := unverifiedToken(t, jwtClaims{
		Email:            "attendee@example.com",
		RegisteredClaims: jwt.RegisteredClaims{Subject: "user-uuid-123"},
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(jwtAssertionHeader, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(got.Groups) != 0 {
		t.Errorf("Groups = %v, want empty", got.Groups)
	}
	if got.HasAnyGroup([]string{"app-con-registrant-admin"}) {
		t.Error("HasAnyGroup returned true for a user with no groups")
	}
}

func TestUserInfo_HasAnyGroup(t *testing.T) {
	tests := []struct {
		name   string
		groups []string
		want   []string
		expect bool
	}{
		{"matches one of several", []string{"a", "b"}, []string{"x", "b"}, true},
		{"no overlap", []string{"a"}, []string{"x"}, false},
		{"empty allow-list denies", []string{"a"}, nil, false},
		{"no groups denies", nil, []string{"a"}, false},
		{"both empty denies", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &UserInfo{Groups: tt.groups}
			if got := u.HasAnyGroup(tt.want); got != tt.expect {
				t.Errorf("HasAnyGroup(%v) with groups %v = %v, want %v", tt.want, tt.groups, got, tt.expect)
			}
		})
	}
}

func TestAuth_UnverifiedMode_MissingEmailClaim_Returns401(t *testing.T) {
	r := newRouter(AuthConfig{TokenValidatorEnabled: false})
	claims := jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "user-uuid-123"},
	}
	token := unverifiedToken(t, claims)

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(jwtAssertionHeader, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing email claim, got %d", w.Code)
	}
}

func TestAuth_UnverifiedMode_MissingSubClaim_Returns401(t *testing.T) {
	r := newRouter(AuthConfig{TokenValidatorEnabled: false})
	claims := jwtClaims{Email: "attendee@example.com"}
	token := unverifiedToken(t, claims)

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(jwtAssertionHeader, token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing sub claim, got %d", w.Code)
	}
}

func TestAuth_UnverifiedMode_MalformedToken_Returns401(t *testing.T) {
	r := newRouter(AuthConfig{TokenValidatorEnabled: false})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(jwtAssertionHeader, "not-a-jwt")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for malformed token, got %d", w.Code)
	}
}

// --- Audience (aud) validation -------------------------------------------
//
// JWT_AUDIENCE accepts several Asgardeo client ids because more than one
// application calls this backend: the attendee microapp and the service
// application the AI service uses for its own reads. The contract these tests
// pin down is OR -- a token is valid when its aud names AT LEAST ONE accepted
// audience -- and it is pinned down at two levels: audienceAllowed directly,
// and end to end through the real Auth middleware with the token validator on,
// so a future change that hands the comparison back to the jwt library (where
// WithAudience and WithAllAudiences differ by one word and one unexported
// bool) cannot flip the semantics unnoticed.

// rsaJWKSServer starts an httptest JWKS endpoint serving one RS256 key and
// returns the private half, its kid and the endpoint URL. The server is closed
// when the test finishes.
func rsaJWKSServer(t *testing.T) (*rsa.PrivateKey, string, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	const kid = "aud-test-kid_RS256"
	jwks := map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes()),
	}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)
	return priv, kid, srv.URL
}

const audTestIssuer = "https://issuer.example/oauth2/token"

// signedTokenWithAudience mints a token that is valid in every respect the
// validator checks except, possibly, its audience -- correct signature, the
// expected issuer, a live exp, and the email/sub claims extractUserInfo
// requires. That isolation matters: a 401 in these tests can then only be the
// audience decision. Pass no audiences to omit the aud claim entirely.
func signedTokenWithAudience(t *testing.T, priv *rsa.PrivateKey, kid string, aud ...string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":   audTestIssuer,
		"sub":   "user-uuid-123",
		"email": "attendee@example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	switch len(aud) {
	case 0:
		// no aud claim at all
	case 1:
		// A bare string, which is how Asgardeo actually spells a single
		// audience and the shape jwt.ClaimStrings has to normalise.
		claims["aud"] = aud[0]
	default:
		claims["aud"] = aud
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func TestAuth_ValidatorOn_AudienceMatching(t *testing.T) {
	priv, kid, jwksURL := rsaJWKSServer(t)

	const (
		microappID = "microapp-client-id"
		aiSvcID    = "ai-service-client-id"
		strangerID = "some-other-app-client-id"
	)

	tests := []struct {
		name      string
		accepted  []string
		tokenAud  []string
		wantCode  int
		rationale string
	}{
		{
			name:      "single configured audience still accepted",
			accepted:  []string{microappID},
			tokenAud:  []string{microappID},
			wantCode:  http.StatusOK,
			rationale: "the pre-existing one-audience deployment must behave exactly as before",
		},
		{
			name:      "token matches the second of several configured audiences",
			accepted:  []string{microappID, aiSvcID},
			tokenAud:  []string{aiSvcID},
			wantCode:  http.StatusOK,
			rationale: "this is the AI service reading session details with its own service token",
		},
		{
			name:      "first configured audience still accepted once a second is added",
			accepted:  []string{microappID, aiSvcID},
			tokenAud:  []string{microappID},
			wantCode:  http.StatusOK,
			rationale: "adding an audience must not silently drop the one already in production",
		},
		{
			name:      "token audience matches none",
			accepted:  []string{microappID, aiSvcID},
			tokenAud:  []string{strangerID},
			wantCode:  http.StatusUnauthorized,
			rationale: "another app in the same Asgardeo org must not get in on a valid signature alone",
		},
		{
			name:      "multi-valued aud where one entry matches",
			accepted:  []string{microappID, aiSvcID},
			tokenAud:  []string{strangerID, aiSvcID},
			wantCode:  http.StatusOK,
			rationale: "aud is legitimately an array; one accepted entry is enough",
		},
		{
			name:      "multi-valued aud where no entry matches",
			accepted:  []string{microappID},
			tokenAud:  []string{strangerID, aiSvcID},
			wantCode:  http.StatusUnauthorized,
			rationale: "an array must not be treated as a wildcard",
		},
		{
			name:      "missing aud claim with the validator on",
			accepted:  []string{microappID, aiSvcID},
			tokenAud:  nil,
			wantCode:  http.StatusUnauthorized,
			rationale: "an unattributable token would let any org token read attendee data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRouter(AuthConfig{
				JWKSEndpoint:          jwksURL,
				Issuer:                audTestIssuer,
				Audiences:             tt.accepted,
				ClockSkew:             time.Minute,
				TokenValidatorEnabled: true,
			})

			req := httptest.NewRequest(http.MethodGet, "/ping", nil)
			req.Header.Set(jwtAssertionHeader, signedTokenWithAudience(t, priv, kid, tt.tokenAud...))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Fatalf("aud=%v accepted=%v: got %d, want %d (%s)",
					tt.tokenAud, tt.accepted, w.Code, tt.wantCode, tt.rationale)
			}
		})
	}
}

// TestAuth_ValidatorOn_MatchedAudienceStillPopulatesUserInfo guards against a
// fix that gets the accept/reject decision right but stops short of handing the
// claims on: the AI service's calls need a UserInfo in the context like any
// other caller, because the handlers below read the attendee identity off it.
func TestAuth_ValidatorOn_MatchedAudienceStillPopulatesUserInfo(t *testing.T) {
	priv, kid, jwksURL := rsaJWKSServer(t)

	r := gin.New()
	r.Use(Auth(AuthConfig{
		JWKSEndpoint:          jwksURL,
		Issuer:                audTestIssuer,
		Audiences:             []string{"microapp-client-id", "ai-service-client-id"},
		ClockSkew:             time.Minute,
		TokenValidatorEnabled: true,
	}))
	var got *UserInfo
	r.GET("/ping", func(c *gin.Context) {
		got = UserInfoFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(jwtAssertionHeader, signedTokenWithAudience(t, priv, kid, "ai-service-client-id"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got == nil {
		t.Fatal("expected UserInfo to be set in context")
	}
	if got.Email != "attendee@example.com" || got.UserID != "user-uuid-123" {
		t.Errorf("UserInfo = %+v, want email attendee@example.com / sub user-uuid-123", got)
	}
}

func TestAudienceAllowed(t *testing.T) {
	tests := []struct {
		name     string
		tokenAud jwt.ClaimStrings
		accepted []string
		wantErr  bool
	}{
		{"single accepted, exact match", jwt.ClaimStrings{"a"}, []string{"a"}, false},
		{"single accepted, mismatch", jwt.ClaimStrings{"b"}, []string{"a"}, true},
		{"several accepted, matches first", jwt.ClaimStrings{"a"}, []string{"a", "b"}, false},
		{"several accepted, matches second", jwt.ClaimStrings{"b"}, []string{"a", "b"}, false},
		{"several accepted, matches none", jwt.ClaimStrings{"z"}, []string{"a", "b"}, true},
		{"multi-valued aud, one match", jwt.ClaimStrings{"z", "b"}, []string{"a", "b"}, false},
		{"multi-valued aud, no match", jwt.ClaimStrings{"y", "z"}, []string{"a", "b"}, true},
		{"nil aud rejected", nil, []string{"a"}, true},
		{"empty-string aud counts as missing", jwt.ClaimStrings{""}, []string{"a"}, true},
		// An empty accepted set must never read as "accept anything";
		// config.Validate refuses to boot in that state, and this is the
		// second line of defence.
		{"no accepted audiences rejects everything", jwt.ClaimStrings{"a"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := audienceAllowed(tt.tokenAud, tt.accepted)
			if (err != nil) != tt.wantErr {
				t.Fatalf("audienceAllowed(%v, %v) error = %v, wantErr %v",
					tt.tokenAud, tt.accepted, err, tt.wantErr)
			}
		})
	}
}

// The rejection message is what an on-call engineer sees; "invalid token" alone
// cannot be told apart from an expiry. It must name both sides of the failed
// comparison and must not contain the token itself, which is a live credential.
func TestAudienceAllowed_ErrorMessageNamesBothSides(t *testing.T) {
	err := audienceAllowed(jwt.ClaimStrings{"stranger-id"}, []string{"microapp-id", "ai-id"})
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"audience", "stranger-id", "microapp-id", "ai-id"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}
