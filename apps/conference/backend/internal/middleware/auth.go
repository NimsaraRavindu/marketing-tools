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
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const jwtAssertionHeader = "x-jwt-assertion"

type contextKey string

const userInfoKey contextKey = "user-info"

// UserInfo holds the authenticated user's identity extracted from the JWT.
type UserInfo struct {
	Email      string
	UserID     string // JWT sub claim
	GivenName  string
	FamilyName string
	// Groups is the JWT groups claim, the IdP's role memberships for this
	// user. Only admin-gated routes read it (see HasAnyGroup); an absent
	// claim yields nil, which denies rather than allows.
	Groups []string
	// RawToken is the literal incoming x-jwt-assertion value, before any
	// parsing. The AI agent routes forward it as the caller's identity ("who
	// is asking"); the credential that gets them past the AI service's
	// gateway travels separately in Authorization (see
	// internal/clients/aiagent). Everything else uses the parsed claims
	// above instead.
	RawToken string
}

// HasAnyGroup reports whether the user belongs to at least one of want.
// An empty want denies: an admin-gated route whose allow-list was never
// configured must not fall open to every authenticated caller.
func (u *UserInfo) HasAnyGroup(want []string) bool {
	for _, w := range want {
		for _, g := range u.Groups {
			if g == w {
				return true
			}
		}
	}
	return false
}

// AuthConfig holds JWT validation configuration.
type AuthConfig struct {
	JWKSEndpoint string
	Issuer       string
	// Audiences is the set of `aud` values this deployment accepts, in
	// OR fashion: a token is valid when its aud claim names at least one of
	// them (see audienceAllowed). Several Asgardeo applications call this
	// backend -- the attendee microapp and the service application the AI
	// service uses for its own reads -- and each stamps its own client id as
	// the audience, so a single accepted value would 401 every call from the
	// others. Only consulted when TokenValidatorEnabled is true.
	Audiences             []string
	ClockSkew             time.Duration
	TokenValidatorEnabled bool
}

type jwtClaims struct {
	Email                string   `json:"email"`
	GivenName            string   `json:"given_name"`
	FamilyName           string   `json:"family_name"`
	Groups               []string `json:"groups"`
	jwt.RegisteredClaims          // Sub carries the user UUID
}

// Auth returns a Gin middleware that validates the x-jwt-assertion header on
// every request and stores the resulting UserInfo in the request context.
// When AuthConfig.TokenValidatorEnabled is false the token is only decoded
// without signature verification (see extractUserInfo's ParseUnverified path):
// forged and expired tokens are accepted, so this is for local development
// ONLY. Production can never reach this branch -- main.go fails closed at
// startup when TOKEN_VALIDATOR_ENABLED is false in a production environment
// (see config.InsecureAuthConfig), so a prod container never boots with the
// validator off.
func Auth(cfg AuthConfig) gin.HandlerFunc {
	var keyFunc jwt.Keyfunc
	if cfg.TokenValidatorEnabled {
		kf, err := newJWKSKeyfunc(cfg.JWKSEndpoint, jwksRefreshInterval)
		if err != nil {
			panic("auth: failed to initialise JWKS from " + cfg.JWKSEndpoint + ": " + err.Error())
		}
		keyFunc = kf
	}

	return func(c *gin.Context) {
		tokenStr := c.GetHeader(jwtAssertionHeader)
		if tokenStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization token"})
			return
		}

		info, err := extractUserInfo(tokenStr, cfg, keyFunc)
		if err != nil {
			slog.WarnContext(c.Request.Context(), "auth: token validation failed", "err", err)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		info.RawToken = tokenStr

		ctx := context.WithValue(c.Request.Context(), userInfoKey, info)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// jwksRefreshInterval is how often the signing keys are re-fetched so that an
// IdP key rotation is picked up without a restart. Asgardeo rotates rarely, so
// an hour is ample and keeps request-path work to an in-memory map lookup.
const jwksRefreshInterval = time.Hour

// jwkKey is one entry of an RS256 JWKS document: the key id plus the RSA public
// key as its raw modulus (n) and exponent (e). We deliberately read only these
// fields and ignore x5c/x5t. Asgardeo's JWKS ships an x5c whose certificate has
// a negative serial number and an x5t#S256 that is a hex string rather than the
// spec's base64url(raw SHA-256); Go 1.23+ crypto/x509 rejects the former and
// the strict jwkset parser rejects the latter, so a cert-based loader (e.g.
// keyfunc.NewDefault) discards the whole key and every token then fails as
// "unverifiable". The n/e values are well-formed, so we build the key from them.
type jwkKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksDoc struct {
	Keys []jwkKey `json:"keys"`
}

// newJWKSKeyfunc fetches the JWKS once (returning an error if that first fetch
// yields no usable key, so startup fails fast on a misconfigured endpoint) and
// then refreshes it in the background every refresh interval. The returned
// jwt.Keyfunc resolves the token's kid against the current key set.
func newJWKSKeyfunc(endpoint string, refresh time.Duration) (jwt.Keyfunc, error) {
	var store atomic.Pointer[map[string]*rsa.PublicKey]

	keys, err := fetchJWKS(endpoint)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("JWKS at %s contained no usable RSA keys", endpoint)
	}
	store.Store(&keys)

	go func() {
		ticker := time.NewTicker(refresh)
		defer ticker.Stop()
		for range ticker.C {
			refreshed, err := fetchJWKS(endpoint)
			if err != nil {
				slog.Warn("auth: JWKS refresh failed, keeping previous keys", "err", err, "endpoint", endpoint)
				continue
			}
			if len(refreshed) == 0 {
				slog.Warn("auth: JWKS refresh returned no keys, keeping previous keys", "endpoint", endpoint)
				continue
			}
			store.Store(&refreshed)
		}
	}()

	return func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		current := store.Load()
		if current != nil {
			if pk, ok := (*current)[kid]; ok {
				return pk, nil
			}
		}
		return nil, fmt.Errorf("no signing key for kid %q", kid)
	}, nil
}

// fetchJWKS retrieves the JWKS document and builds RSA public keys from the
// n/e parameters only. Keys without those parameters (or non-RSA keys) are
// skipped rather than failing the whole set.
func fetchJWKS(endpoint string) (map[string]*rsa.PublicKey, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch JWKS: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read JWKS: %w", err)
	}
	var doc jwksDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.N == "" || k.E == "" || k.Kid == "" {
			continue
		}
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nb),
			E: int(new(big.Int).SetBytes(eb).Int64()),
		}
	}
	return keys, nil
}

// UserInfoFromContext retrieves the authenticated user's info from the context.
// Returns nil if the auth middleware was not applied.
func UserInfoFromContext(ctx context.Context) *UserInfo {
	v, _ := ctx.Value(userInfoKey).(*UserInfo)
	return v
}

// WithUserInfo returns a copy of ctx carrying the given UserInfo.
// Call this in tests to bypass JWT parsing and inject a fake authenticated user.
func WithUserInfo(ctx context.Context, user *UserInfo) context.Context {
	return context.WithValue(ctx, userInfoKey, user)
}

// audienceAllowed reports, as an error or its absence, whether a token's `aud`
// claim names at least one of the audiences this deployment accepts.
//
// This is deliberately hand-rolled instead of handed to jwt.WithAudience, and
// the reason is a trap worth recording, because every way of getting it wrong
// fails silently rather than loudly:
//
//   - In golang-jwt/jwt v5 (v5.3.1 is what go.mod pins), WithAudience is
//     variadic and ASSIGNS its argument: `p.validator.expectedAud = aud`. So
//     the intuitive "one call per accepted audience" -- WithAudience(a) then
//     WithAudience(b) -- does not accumulate and does not error. The second
//     call overwrites the first, `a` quietly stops being accepted, and the
//     microapp's own tokens start 401ing the moment a second audience is added
//     to the config. Nothing in the logs points at the option list.
//   - A single variadic call, WithAudience(a, b), does mean "any of" today
//     (the validator's expectAllAud defaults to false and verifyAudience
//     returns nil on the first match), while WithAllAudiences(a, b) is the
//     "all of" variant. Two constructors one word apart, differing only in an
//     unexported bool, deciding whether this backend accepts one caller or
//     none -- that is not a distinction worth resting the auth boundary on
//     across a future dependency bump.
//
// Doing the comparison here makes the intended semantics -- OR, at least one
// match -- readable at the call site and directly testable without minting
// signed tokens through the whole parser.
//
// The empty/missing-aud case matches what the library does when an audience is
// expected: reject. An `aud`-less token is one this backend cannot attribute to
// any registered application, and accepting it would mean any token the IdP
// ever issued, for any unrelated Asgardeo app in the org, is good enough to
// read attendee session details.
func audienceAllowed(tokenAud jwt.ClaimStrings, accepted []string) error {
	// Defence in depth against a caller constructing AuthConfig by hand with
	// the validator on and no audiences: config.Validate already refuses to
	// boot in that state, and an empty accepted set must never read as
	// "accept anything".
	if len(accepted) == 0 {
		return fmt.Errorf("token audience check: no accepted audiences configured")
	}

	// A JWT may carry aud as a bare string or an array; jwt.ClaimStrings
	// normalises both to a slice. An array of one empty string is how some
	// issuers spell "no audience", so it is filtered out here and treated as
	// missing, the same as the library's own verifyAudience does.
	present := make([]string, 0, len(tokenAud))
	for _, a := range tokenAud {
		if a != "" {
			present = append(present, a)
		}
	}
	if len(present) == 0 {
		return fmt.Errorf("token has no aud claim; accepted audiences are %v", accepted)
	}

	for _, a := range present {
		for _, want := range accepted {
			if a == want {
				return nil
			}
		}
	}

	// Both sides are named in the message on purpose: these are OAuth client
	// ids, not secrets, and they are the whole content of the failure. Without
	// them the log line says only "invalid token" and the on-call engineer
	// cannot tell a wrong-application token from an expired one. The token
	// itself is never logged -- it is a live bearer credential, and the caller
	// (Auth) logs only this error.
	return fmt.Errorf("token audience %v is not among the accepted audiences %v", present, accepted)
}

func extractUserInfo(tokenStr string, cfg AuthConfig, keyFunc jwt.Keyfunc) (*UserInfo, error) {
	var c jwtClaims

	if !cfg.TokenValidatorEnabled {
		if _, _, err := new(jwt.Parser).ParseUnverified(tokenStr, &c); err != nil {
			return nil, fmt.Errorf("decode token: %w", err)
		}
	} else {
		// No jwt.WithAudience here on purpose -- the audience is checked below
		// by audienceAllowed. Everything else (signature via keyFunc, issuer,
		// clock skew, a mandatory exp) stays with the library exactly as
		// before; only the aud comparison moves out. See audienceAllowed for
		// why.
		token, err := jwt.ParseWithClaims(tokenStr, &c, keyFunc,
			jwt.WithIssuer(cfg.Issuer),
			jwt.WithLeeway(cfg.ClockSkew),
			jwt.WithExpirationRequired(),
		)
		if err != nil {
			return nil, fmt.Errorf("validate token: %w", err)
		}
		if !token.Valid {
			return nil, fmt.Errorf("invalid token")
		}
		// Ordered after ParseWithClaims so an unsigned, expired or
		// wrong-issuer token is rejected on those grounds first and its aud
		// claim -- which at that point is attacker-controlled text -- is never
		// the thing we reason about, nor the thing we put in a log line.
		if err := audienceAllowed(c.Audience, cfg.Audiences); err != nil {
			return nil, err
		}
	}

	if c.Email == "" {
		return nil, fmt.Errorf("token missing email claim")
	}
	if c.Subject == "" {
		return nil, fmt.Errorf("token missing sub claim")
	}

	// A missing groups claim is not an error: every route except the
	// admin-gated ones ignores it, and those deny on an empty list anyway.
	return &UserInfo{
		Email:      c.Email,
		UserID:     c.Subject,
		GivenName:  c.GivenName,
		FamilyName: c.FamilyName,
		Groups:     c.Groups,
	}, nil
}
