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
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wso2-coin-backend/internal/features"
	"wso2-coin-backend/internal/models"
)

type fakeAppConfigReader struct {
	configs []models.AppConfig
	err     error
}

func (f *fakeAppConfigReader) List(ctx context.Context) ([]models.AppConfig, error) {
	return f.configs, f.err
}

func newAppConfigTestRouter(h *AppConfigHandler) *gin.Engine {
	r := gin.New()
	r.GET("/app-configs", h.List)
	return r
}

// decodeAppConfigs flattens the response into key -> value, which is how the
// microapp reads it and how most of these assertions are phrased.
func decodeAppConfigs(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var items []models.AppConfig
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := make(map[string]string, len(items))
	for _, item := range items {
		out[item.Key] = item.Value
	}
	return out
}

func TestAppConfigHandler_List_Success(t *testing.T) {
	reader := &fakeAppConfigReader{configs: []models.AppConfig{
		{Key: "ATTENDEES_SYNC", Value: "COMPLETED", CreatedBy: "SYSTEM", UpdatedBy: "SYSTEM"},
	}}
	h := NewAppConfigHandler(reader, nil, "")
	r := newAppConfigTestRouter(h)

	w := doRequest(r, http.MethodGet, "/app-configs", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	got := decodeAppConfigs(t, w.Body.Bytes())
	if got["ATTENDEES_SYNC"] != "COMPLETED" {
		t.Errorf("unexpected response: %+v", got)
	}
}

// An empty table must still serialise as a JSON array. It is no longer the
// *empty* array -- the presentational defaults are always emitted -- but a
// `null` body is what the microapp cannot survive, so that is what this pins.
func TestAppConfigHandler_List_EmptyReturnsArrayNotNull(t *testing.T) {
	h := NewAppConfigHandler(&fakeAppConfigReader{configs: nil}, nil, "")
	r := newAppConfigTestRouter(h)

	w := doRequest(r, http.MethodGet, "/app-configs", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if body := w.Body.String(); body == "null" || body[0] != '[' {
		t.Errorf("body = %q, want a JSON array", body)
	}
	got := decodeAppConfigs(t, w.Body.Bytes())
	if len(got) != 1 {
		t.Errorf("response = %+v, want only the presentational defaults", got)
	}
}

func TestAppConfigHandler_List_RepoErrorMapsTo500(t *testing.T) {
	h := NewAppConfigHandler(&fakeAppConfigReader{err: errBoom}, nil, "")
	r := newAppConfigTestRouter(h)

	w := doRequest(r, http.MethodGet, "/app-configs", nil)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// fakeFeatureSnapshotter stands in for *features.Resolver.
type fakeFeatureSnapshotter struct {
	states map[features.Feature]features.State
}

func (f *fakeFeatureSnapshotter) Snapshot(context.Context) map[features.Feature]features.State {
	return f.states
}

// The microapp must be able to read a feature's state even when nobody has
// seeded a row for it, so the handler fills the gaps from the resolver.
func TestAppConfigHandler_List_SynthesisesMissingFeatureRows(t *testing.T) {
	reader := &fakeAppConfigReader{configs: []models.AppConfig{
		{Key: "cache_version", Value: "1", CreatedBy: "SYSTEM", UpdatedBy: "SYSTEM"},
	}}
	feats := &fakeFeatureSnapshotter{states: map[features.Feature]features.State{
		features.AIChat: {Feature: features.AIChat, Enabled: false, Title: "Later", Message: "Much later"},
	}}
	h := NewAppConfigHandler(reader, feats, "")
	r := newAppConfigTestRouter(h)

	w := doRequest(r, http.MethodGet, "/app-configs", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	got := decodeAppConfigs(t, w.Body.Bytes())

	if got["is_ai_chat_enabled"] != "0" {
		t.Errorf("is_ai_chat_enabled = %q, want %q", got["is_ai_chat_enabled"], "0")
	}
	if got["ai_chat_coming_soon_title"] != "Later" {
		t.Errorf("title = %q", got["ai_chat_coming_soon_title"])
	}
	if got["ai_chat_coming_soon_message"] != "Much later" {
		t.Errorf("message = %q", got["ai_chat_coming_soon_message"])
	}
	if got["cache_version"] != "1" {
		t.Error("unrelated rows must survive untouched")
	}
}

// A real row always wins: the synthesiser fills gaps, it never contradicts an
// operator.
func TestAppConfigHandler_List_StoredRowBeatsTheDefault(t *testing.T) {
	reader := &fakeAppConfigReader{configs: []models.AppConfig{
		{Key: "is_ai_chat_enabled", Value: "1", CreatedBy: "SYSTEM", UpdatedBy: "SYSTEM"},
	}}
	feats := &fakeFeatureSnapshotter{states: map[features.Feature]features.State{
		features.AIChat: {Feature: features.AIChat, Enabled: false, Title: "Later", Message: "Much later"},
	}}
	h := NewAppConfigHandler(reader, feats, "")
	r := newAppConfigTestRouter(h)

	w := doRequest(r, http.MethodGet, "/app-configs", nil)

	var items []models.AppConfig
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := 0
	for _, item := range items {
		if item.Key != "is_ai_chat_enabled" {
			continue
		}
		seen++
		if item.Value != "1" {
			t.Errorf("value = %q, want the stored row to win", item.Value)
		}
	}
	if seen != 1 {
		t.Errorf("is_ai_chat_enabled appeared %d times, want exactly one row", seen)
	}
}

// Response order must be stable: the resolver hands back a map, and an endpoint
// whose payload reshuffles every request defeats any response-level diffing.
func TestAppConfigHandler_List_IsSortedByKey(t *testing.T) {
	feats := &fakeFeatureSnapshotter{states: map[features.Feature]features.State{
		features.AIChat:  {Feature: features.AIChat, Enabled: true, Title: "a", Message: "b"},
		features.Agenda:  {Feature: features.Agenda, Enabled: true, Title: "a", Message: "b"},
		features.Shop:    {Feature: features.Shop, Enabled: true, Title: "a", Message: "b"},
		features.Wallet:  {Feature: features.Wallet, Enabled: true, Title: "a", Message: "b"},
		features.Coin:    {Feature: features.Coin, Enabled: true, Title: "a", Message: "b"},
		features.Profile: {Feature: features.Profile, Enabled: true, Title: "a", Message: "b"},
	}}
	h := NewAppConfigHandler(&fakeAppConfigReader{}, feats, "")
	r := newAppConfigTestRouter(h)

	w := doRequest(r, http.MethodGet, "/app-configs", nil)

	var items []models.AppConfig
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := 1; i < len(items); i++ {
		if items[i-1].Key > items[i].Key {
			t.Fatalf("out of order at %d: %q then %q", i, items[i-1].Key, items[i].Key)
		}
	}
}

// is_shop_hidden is presentational, not a feature flag, so it is emitted
// unconditionally -- against an unseeded database and, unlike the feature
// rows, even when this handler was built without a resolver.
func TestAppConfigHandler_List_ShopHiddenDefaultsToVisible(t *testing.T) {
	feats := &fakeFeatureSnapshotter{states: map[features.Feature]features.State{
		features.Shop: {Feature: features.Shop, Enabled: true, Title: "a", Message: "b"},
	}}

	tests := []struct {
		name  string
		rows  []models.AppConfig
		feats FeatureSnapshotter
		want  string
	}{
		{
			name: "no row seeded",
			rows: nil,
			// The whole point: 016 has not run, the microapp
			// still learns the tab bar layout from the server.
			feats: feats,
			want:  "0",
		},
		{
			name:  "stored row wins",
			rows:  []models.AppConfig{{Key: ShopHiddenKey, Value: "1", CreatedBy: "SYSTEM", UpdatedBy: "SYSTEM"}},
			feats: feats,
			want:  "1",
		},
		{
			name: "nil snapshotter still emits it",
			rows: nil,
			// withDefaults skips the feature rows when features
			// is nil; this key must not go with them.
			feats: nil,
			want:  "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewAppConfigHandler(&fakeAppConfigReader{configs: tt.rows}, tt.feats, "")
			r := newAppConfigTestRouter(h)

			w := doRequest(r, http.MethodGet, "/app-configs", nil)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
			}
			var items []models.AppConfig
			if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
				t.Fatalf("decode: %v", err)
			}
			seen := 0
			for _, item := range items {
				if item.Key != ShopHiddenKey {
					continue
				}
				seen++
				if item.Value != tt.want {
					t.Errorf("%s = %q, want %q", ShopHiddenKey, item.Value, tt.want)
				}
			}
			if seen != 1 {
				t.Errorf("%s appeared %d times, want exactly one row", ShopHiddenKey, seen)
			}
		})
	}
}

// The key must not be mistaken for a feature flag by the resolver's
// is_<x>_enabled discovery: a phantom `shop_hidden` feature would show up in
// every snapshot with copy nobody wrote. features_test.go asserts the same
// thing from the resolver's side; this pins the spelling the handler emits.
func TestShopHiddenKeyIsNotAFeatureFlagSpelling(t *testing.T) {
	if ShopHiddenKey != "is_shop_hidden" {
		t.Errorf("ShopHiddenKey = %q, want the spelling the microapp and migration 016 use", ShopHiddenKey)
	}
	if strings.HasSuffix(ShopHiddenKey, "_enabled") {
		t.Errorf("%q ends in _enabled, so features.apply would read it as a feature", ShopHiddenKey)
	}
	if ShopHiddenKey == features.Shop.EnabledKey() {
		t.Errorf("%q collides with the shop's gating flag", ShopHiddenKey)
	}
}
