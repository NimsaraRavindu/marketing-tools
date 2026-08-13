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
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wso2-coin-backend/internal/models"
)

type fakeActivityReader struct {
	items []models.Activity
	err   error
}

func (f *fakeActivityReader) List(ctx context.Context) ([]models.Activity, error) {
	return f.items, f.err
}

func newActivityTestRouter(h *ActivityHandler) *gin.Engine {
	r := gin.New()
	r.GET("/activities", h.List)
	return r
}

func TestActivityHandler_List_ReturnsActivities(t *testing.T) {
	start := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	reader := &fakeActivityReader{items: []models.Activity{{
		ID:          "11111111-1111-1111-1111-111111111111",
		Name:        "Registration",
		Description: "Pick up your badge",
		StartTime:   start,
		EndTime:     start.Add(3 * time.Hour),
		Location: &models.ActivityLocation{
			Name:         "Main Foyer",
			Address:      "123 Conference Way",
			FloorPlanURL: "https://example.com/floor.png",
		},
	}}}

	w := doRequest(newActivityTestRouter(NewActivityHandler(reader)), http.MethodGet, "/activities", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got []models.Activity
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d activities, want 1", len(got))
	}
	if got[0].Name != "Registration" {
		t.Errorf("Name = %q, want Registration", got[0].Name)
	}
	if got[0].Location == nil || got[0].Location.Address != "123 Conference Way" {
		t.Errorf("Location = %+v, want the address populated", got[0].Location)
	}
	if !got[0].StartTime.Equal(start) {
		t.Errorf("StartTime = %v, want %v", got[0].StartTime, start)
	}
}

// The client reads location.name with optional chaining, so an activity with
// no location must omit the key rather than send an object of empty strings.
func TestActivityHandler_List_OmitsAbsentLocation(t *testing.T) {
	reader := &fakeActivityReader{items: []models.Activity{{
		ID:   "11111111-1111-1111-1111-111111111111",
		Name: "Hallway track",
	}}}

	w := doRequest(newActivityTestRouter(NewActivityHandler(reader)), http.MethodGet, "/activities", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if strings.Contains(w.Body.String(), "\"location\"") {
		t.Errorf("expected no location key, got %s", w.Body.String())
	}
}

// An empty result must serialize as [] rather than null: the client maps over
// the response directly.
func TestActivityHandler_List_EmptyIsEmptyArray(t *testing.T) {
	w := doRequest(newActivityTestRouter(NewActivityHandler(&fakeActivityReader{})), http.MethodGet, "/activities", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("body = %q, want []", got)
	}
}

func TestActivityHandler_List_RepoErrorIs500(t *testing.T) {
	reader := &fakeActivityReader{err: errors.New("db down")}
	w := doRequest(newActivityTestRouter(NewActivityHandler(reader)), http.MethodGet, "/activities", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
