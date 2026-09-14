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
	"context"
	"log/slog"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	"wso2-coin-backend/internal/features"
	"wso2-coin-backend/internal/middleware"
	"wso2-coin-backend/internal/models"
)

// Keys this handler answers for itself rather than reading them out of the
// app_config table.
const (
	// MerchantWalletAddressKey carries SHOP_MASTER_WALLET_ADDRESS to the
	// microapp, which needs the destination wallet to render a checkout.
	// It is camelCase where every stored key is snake_case because it has
	// never been a row -- openapi.yaml documents it as synthetic.
	MerchantWalletAddressKey = "merchantWalletAddress"

	// ShopHiddenKey hides the Shop tab in the microapp's tab bar. When it
	// reads "1" the tab is removed and its slot goes to the AI assistant,
	// which also drops the floating assistant button because the tab
	// replaces it.
	//
	// Deliberately *not* spelled is_shop_<something>_enabled. That suffix
	// is the feature-flag convention, and features.apply discovers a
	// feature from any is_<x>_enabled row it finds, so an "_enabled"
	// spelling here would invent a phantom feature -- one with no entry in
	// the registry, no routes in the gate map and no placeholder copy.
	// This key gates no route and changes no response: it is
	// presentational only.
	//
	// is_shop_enabled remains the gate (503 plus coming-soon copy), and
	// the two are orthogonal: a shop can be enabled and still hidden,
	// which is how the tab bar is reshuffled without closing the shop.
	ShopHiddenKey = "is_shop_hidden"
)

// redactedConfigKeys are rows this endpoint holds back.
//
// GET /app-configs answers every authenticated attendee, and it returns rows
// verbatim precisely because a key is opaque operational data to it. That is
// fine for a flag and wrong for a row whose value is a list of real email
// addresses: features.GateBypassEmailsKey is read by this service's own gate
// middleware and by nothing in the microapp, so returning it would publish
// staff addresses to every phone holding a token and buy no client anything.
//
// Held back rather than blanked, and not by name in the response either: an
// entry with an empty value would still say "these people exist and this is
// what the row is called", and a client that keys its config by `key` handles
// a key it never receives exactly the way it already handles an unseeded one.
//
// Redaction lives here, not in the repository, because features.Resolver reads
// the row through the same AppConfigRepo.List -- filtering there would take
// the allowlist away from the middleware that is the only thing that wants it.
var redactedConfigKeys = map[string]struct{}{
	features.GateBypassEmailsKey: {},
}

// shopHiddenDefault keeps the Shop tab visible. It matches the '0' seeded by
// migrations/016_shop_hidden.sql, so an unseeded database and a seeded one
// answer identically.
const shopHiddenDefault = "0"

// How an is_<feature>_enabled row spells its two states. The microapp reads
// these as strings, not booleans, because every app_config value is text.
const (
	featureEnabled  = "1"
	featureDisabled = "0"
)

// AppConfigReader is satisfied by *repository.AppConfigRepo.
type AppConfigReader interface {
	List(ctx context.Context) ([]models.AppConfig, error)
}

// FeatureSnapshotter is the slice of *features.Resolver this handler needs.
type FeatureSnapshotter interface {
	Snapshot(ctx context.Context) map[features.Feature]features.State
	// BypassesGates reports whether this caller is on the app_config
	// allowlist (features.GateBypassEmailsKey). Part of this interface for
	// the same reason middleware.FeatureGateResolver carries it rather than
	// sniffing for it with a type assertion: a resolver that cannot answer
	// it would silently take the bypass away, and the symptom -- a tester
	// who still sees a hidden screen -- looks like a wrong row rather than
	// like missing wiring.
	BypassesGates(ctx context.Context, email string) bool
}

// AttendeeMembership is the slice of *features.Membership this handler needs,
// and the same interface middleware.FeatureGate takes: the two must agree
// about who is registered, or the microapp hides a screen whose routes answer
// (or, worse, shows one whose routes do not).
type AttendeeMembership interface {
	IsAttendee(ctx context.Context, email string) bool
}

// AppConfigHandler exposes the read-only app-configs HTTP endpoint. There is
// no write route through this API, matching the old service exactly (see
// .claude/PLAN.md).
type AppConfigHandler struct {
	configs               AppConfigReader
	features              FeatureSnapshotter
	members               AttendeeMembership
	merchantWalletAddress string
}

// NewAppConfigHandler constructs an AppConfigHandler. features may be nil, in
// which case no feature-flag rows are synthesised, no rule is applied on top
// of the table, and the response is the table's rows plus the presentational
// defaults withDefaults always emits. members may be nil too, and is read as
// "cannot tell who is registered", which grants rather than refuses -- the
// same direction features.Membership takes on a failed lookup, and for the
// same reason.
func NewAppConfigHandler(
	configs AppConfigReader,
	feats FeatureSnapshotter,
	members AttendeeMembership,
	merchantWalletAddress string,
) *AppConfigHandler {
	return &AppConfigHandler{
		configs:               configs,
		features:              feats,
		members:               members,
		merchantWalletAddress: merchantWalletAddress,
	}
}

// List handles GET /app-configs, returning every row verbatim regardless of
// what any given key means -- no filtering, no pagination.
//
// The response is per caller (see applyAccessRule), so it is never stored by
// any cache. RFC 9111 3.5 already keeps a compliant shared cache off a
// response to an Authorization-bearing request, but that is a rule about
// caches behaving, not a header on the response, and what is at stake is one
// attendee being served another's answer about which screens they may see.
// no-store says it outright, and it is stated here rather than in a
// route-local middleware so that it cannot be lost by a reordering of
// main.go's route table.
func (h *AppConfigHandler) List(c *gin.Context) {
	c.Header("Cache-Control", "no-store")

	configs, err := h.configs.List(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "fetching app configs failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}
	if configs == nil {
		configs = []models.AppConfig{}
	}

	// One read of the resolver for the whole response, so the rows this
	// endpoint synthesises and the rows the bypass overrides can never
	// disagree about which features exist or what state they are in.
	ctx := c.Request.Context()
	var snapshot map[features.Feature]features.State
	if h.features != nil {
		snapshot = h.features.Snapshot(ctx)
	}

	configs = redact(configs)
	configs = withDefaults(snapshot, configs)
	configs = h.applyAccessRule(ctx, snapshot, configs)

	if h.merchantWalletAddress != "" {
		configs = append(configs, models.AppConfig{
			Key:   MerchantWalletAddressKey,
			Value: h.merchantWalletAddress,
		})
	}

	c.JSON(http.StatusOK, configs)
}

// redact drops every row in redactedConfigKeys, in place, preserving the
// order of the rows that survive.
//
// Runs before withDefaults so that a redacted key cannot be re-added as a
// synthetic default -- today none of them is a key withDefaults knows about,
// but the ordering makes that a property of the pipeline rather than a
// coincidence between two lists.
func redact(configs []models.AppConfig) []models.AppConfig {
	kept := configs[:0]
	for _, cfg := range configs {
		if _, hidden := redactedConfigKeys[cfg.Key]; hidden {
			continue
		}
		kept = append(kept, cfg)
	}
	return kept
}

// applyAccessRule rewrites every is_<feature>_enabled row to what this
// particular caller is actually served, so that the screen the microapp shows
// and the routes middleware.FeatureGate answers agree. Both evaluate
// features.Grant.Allowed; this is the half the client sees.
//
// It makes the response per caller in two directions:
//
//   - Up, for an address on features.GateBypassEmailsKey. Without it the
//     allowlist opens the endpoints behind an unannounced feature while the
//     app still hides the screen in front of them, and the only way to click
//     through would be to flip the flag for every attendee at once -- which
//     is the state the allowlist exists to avoid.
//   - Down, for a caller with no row in attendees. They are told what they
//     would be told if the feature were switched off, because from where they
//     stand it is. Hiding the screen matters more here than closing the route:
//     a screen that renders and then 503s on its first fetch is a worse answer
//     than the coming-soon copy.
//
// Overriding, not defaulting: the stored rows say what they say on purpose, so
// unlike withDefaults this must beat a row that exists. It runs after
// withDefaults for that reason -- a synthesised flag and a stored one are
// equally in the caller's way.
//
// The stored row stays the authority on whether the feature is on, and the
// snapshot answers only for a row the table does not hold or spells in a way
// features.ParseEnabled makes nothing of. The rows this handler serves come
// straight from AppConfigRepo.List and are therefore up to DefaultTTL fresher
// than the snapshot; taking the flag from the snapshot instead would mean an
// operator who has just switched a feature on watches this endpoint report it
// off again for the next half minute.
//
// A row whose value the rule leaves standing is passed through byte for byte,
// so "true" stays "true" for the client that has always received it. Only a
// row the rule actually overturns is rewritten, and then to "1" or "0", which
// is what openapi.yaml documents.
//
// Only is_<feature>_enabled keys are touched, and only for features the
// resolver knows about. is_shop_hidden looks like a flag and is not one (see
// ShopHiddenKey): it is presentational, it gates no route, so neither half of
// the rule has any business reshuffling the tab bar. The coming-soon title and
// message rows are left alone too -- they are what the microapp renders
// *instead of* the screen, and a client reading "1" never reaches them.
//
// A nil user is neither allowlisted nor registered. GET /app-configs sits on
// the authenticated group, so nil means someone reordered the middleware;
// unlike the handlers that answer 401 on it, this one keeps serving -- an
// attendee who cannot read the config has no app at all -- but it grants no
// bypass to a caller it cannot name.
func (h *AppConfigHandler) applyAccessRule(
	ctx context.Context,
	snapshot map[features.Feature]features.State,
	configs []models.AppConfig,
) []models.AppConfig {
	if h.features == nil || len(snapshot) == 0 {
		return configs
	}

	var email string
	if user := middleware.UserInfoFromContext(ctx); user != nil {
		email = user.Email
	}

	bypasses := email != "" && h.features.BypassesGates(ctx, email)

	// Registered is worth the lookup only when it can change an answer, and
	// the allowlist already settles it. A nil members grants, matching the
	// direction features.Membership takes when it cannot reach the table:
	// a component that cannot tell who is registered must not black out
	// every screen.
	registered := true
	if !bypasses && h.members != nil {
		registered = h.members.IsAttendee(ctx, email)
	}

	// Which keys are flags, and what the snapshot would say about each if
	// the row itself does not parse.
	fallback := make(map[string]bool, len(snapshot))
	for f, state := range snapshot {
		fallback[f.EnabledKey()] = state.Enabled
	}

	var opened, closed []string
	for i := range configs {
		snapshotEnabled, isFlag := fallback[configs[i].Key]
		if !isFlag {
			continue
		}

		enabled, parsed := features.ParseEnabled(configs[i].Value)
		if !parsed {
			enabled = snapshotEnabled
		}

		allowed := features.Grant{
			Enabled:    enabled,
			Registered: registered,
			Bypasses:   bypasses,
		}.Allowed()
		if allowed == enabled {
			continue
		}

		if allowed {
			opened = append(opened, configs[i].Key)
			configs[i].Value = featureEnabled
		} else {
			closed = append(closed, configs[i].Key)
			configs[i].Value = featureDisabled
		}
	}

	// Warn on the widening, for the reason FeatureGate warns when it lets an
	// allowlisted caller past: a feature the operator switched off is being
	// shown, which is correct here and is also the shape of an allowlist
	// somebody forgot to clear. The narrowing is Info -- it is the
	// attendee check doing exactly its job, once per unregistered caller per
	// request. Neither logs the address: the allowlist row already holds the
	// ones that matter and a log aggregator should not.
	if len(opened) > 0 {
		slog.WarnContext(ctx, "reporting disabled features as enabled to an allowlisted caller",
			"keys", opened)
	}
	if len(closed) > 0 {
		slog.InfoContext(ctx, "reporting enabled features as disabled to an unregistered caller",
			"keys", closed)
	}

	return configs
}

// withDefaults appends a row for every key the microapp expects to always be
// there but which the table does not hold, so it receives a complete set
// against a database that has not been seeded (or that is behind on
// migrations).
//
// Rows that do exist win untouched -- this only fills gaps, so it can never
// contradict what an operator set. Synthetic rows carry Go zero values in the
// four audit fields, the same way the merchantWalletAddress row already does;
// the microapp reads only key and value.
//
// Without this, a missing row means "the client falls back to whatever its
// build compiled in", and the compiled-in default of an old build is exactly
// what a config row is supposed to override. Answering from the server keeps
// one source of truth for what the app does when nobody has configured it.
//
// Two kinds of default are filled, and the split matters:
//
//   - Presentational keys, currently just ShopHiddenKey, are unconditional.
//     They are not features, so nothing about them depends on the resolver
//     and they must be emitted even when this handler was built without one.
//   - Feature-flag keys come from the resolver snapshot and are therefore
//     skipped when it is empty -- which is what a handler built without a
//     resolver passes -- in which case the response is exactly what the
//     table holds plus the presentational defaults.
func withDefaults(snapshot map[features.Feature]features.State, configs []models.AppConfig) []models.AppConfig {
	present := make(map[string]struct{}, len(configs))
	for _, cfg := range configs {
		present[cfg.Key] = struct{}{}
	}

	appendIfMissing := func(key, value string) {
		if _, ok := present[key]; ok {
			return
		}
		present[key] = struct{}{}
		configs = append(configs, models.AppConfig{Key: key, Value: value})
	}

	appendIfMissing(ShopHiddenKey, shopHiddenDefault)

	for f, state := range snapshot {
		enabled := featureDisabled
		if state.Enabled {
			enabled = featureEnabled
		}
		appendIfMissing(f.EnabledKey(), enabled)
		appendIfMissing(f.TitleKey(), state.Title)
		appendIfMissing(f.MessageKey(), state.Message)
	}

	// Snapshot is a map, so the synthesised rows arrive in a random order.
	// The SQL rows are already sorted by config_key and the microapp keys
	// the array by `key`, but an endpoint whose payload reshuffles on every
	// request defeats any response-level diffing, so sort the tail.
	sort.Slice(configs, func(i, j int) bool { return configs[i].Key < configs[j].Key })
	return configs
}
