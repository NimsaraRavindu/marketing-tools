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

// AppConfigHandler exposes the read-only app-configs HTTP endpoint. There is
// no write route through this API, matching the old service exactly (see
// .claude/PLAN.md).
type AppConfigHandler struct {
	configs               AppConfigReader
	features              FeatureSnapshotter
	merchantWalletAddress string
}

// NewAppConfigHandler constructs an AppConfigHandler. features may be nil, in
// which case no feature-flag rows are synthesised and the response is the
// table's rows plus the presentational defaults withDefaults always emits.
func NewAppConfigHandler(configs AppConfigReader, feats FeatureSnapshotter, merchantWalletAddress string) *AppConfigHandler {
	return &AppConfigHandler{configs: configs, features: feats, merchantWalletAddress: merchantWalletAddress}
}

// List handles GET /app-configs, returning every row verbatim regardless of
// what any given key means -- no filtering, no pagination.
func (h *AppConfigHandler) List(c *gin.Context) {
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
	configs = h.unhideForBypass(ctx, snapshot, configs)

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

// unhideForBypass reports every feature as enabled to a caller on the
// features.GateBypassEmailsKey allowlist, leaving the response untouched for
// everybody else.
//
// This is the client-side half of the bypass that middleware.FeatureGate
// already implements for routes. Without it the allowlist opens the endpoints
// behind an unannounced feature but the microapp still hides the screen in
// front of them, because the screen is hidden by the very rows this endpoint
// serves -- so the only way to click through a switched-off feature was to
// flip is_<feature>_enabled, which shows it to every attendee at the same
// time. That is exactly the state the allowlist exists to avoid.
//
// Overriding, not defaulting: the stored rows say '0' and they say it on
// purpose, so unlike withDefaults this must beat a row that exists. It runs
// after withDefaults for that reason -- a synthesised flag and a stored one
// are equally in the way of a tester.
//
// Only is_<feature>_enabled keys are touched, and only for features the
// resolver knows about. is_shop_hidden looks like a flag and is not one (see
// ShopHiddenKey): it is presentational, it gates no route, so the bypass has
// no business reshuffling the tab bar. The coming-soon title and message rows
// are left alone too -- they are what the microapp renders *instead of* the
// screen, and a client reading enabled='1' never reaches them.
//
// The allowlist is consulted only after the response is otherwise built, so an
// ordinary attendee's request pays one map lookup for a feature that is not
// for them.
//
// A nil user is not on the list. GET /app-configs sits on the authenticated
// group, so nil means someone reordered the middleware; unlike the handlers
// that answer 401 on it, this one keeps serving -- the flags are the same for
// every attendee and refusing them would black out the whole microapp -- but
// it grants no bypass to a caller it cannot name.
func (h *AppConfigHandler) unhideForBypass(
	ctx context.Context,
	snapshot map[features.Feature]features.State,
	configs []models.AppConfig,
) []models.AppConfig {
	if h.features == nil || len(snapshot) == 0 {
		return configs
	}

	user := middleware.UserInfoFromContext(ctx)
	if user == nil || !h.features.BypassesGates(ctx, user.Email) {
		return configs
	}

	enabledKeys := make(map[string]struct{}, len(snapshot))
	for f := range snapshot {
		enabledKeys[f.EnabledKey()] = struct{}{}
	}

	unhidden := make([]string, 0, len(snapshot))
	for i := range configs {
		if _, isFlag := enabledKeys[configs[i].Key]; !isFlag {
			continue
		}
		if configs[i].Value == featureEnabled {
			continue
		}
		configs[i].Value = featureEnabled
		unhidden = append(unhidden, configs[i].Key)
	}

	if len(unhidden) > 0 {
		// Warn, not Info, and for the same reason FeatureGate warns when it
		// lets one of these callers past: a feature the operator switched off
		// is being shown, which is correct here but is also the shape of an
		// allowlist someone forgot to clear. The addresses stay out of the
		// log -- the row already holds them and a log aggregator should not.
		slog.WarnContext(ctx, "reporting disabled features as enabled to an allowlisted caller",
			"keys", unhidden)
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
