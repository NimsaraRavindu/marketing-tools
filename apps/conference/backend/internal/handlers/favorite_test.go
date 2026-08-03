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
	"testing"

	"github.com/gin-gonic/gin"

	"wso2-coin-backend/internal/middleware"
	"wso2-coin-backend/internal/models"
)

type fakeFavoritesReader struct {
	items     []models.Favorite
	listErr   error
	addErr    error
	removeErr error

	addedSession   string
	removedSession string
}

func (f *fakeFavoritesReader) List(ctx context.Context, userUUID string) ([]models.Favorite, error) {
	return f.items, f.listErr
}

func (f *fakeFavoritesReader) Add(ctx context.Context, userUUID, sessionID string) error {
	f.addedSession = sessionID
	return f.addErr
}

func (f *fakeFavoritesReader) Remove(ctx context.Context, userUUID, sessionID string) error {
	f.removedSession = sessionID
	return f.removeErr
}

func newFavoritesTestRouter(h *FavoritesHandler, user *middleware.UserInfo) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if user != nil {
			ctx := middleware.WithUserInfo(c.Request.Context(), user)
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	})
	r.GET("/users/me/favorites", h.List)
	r.PUT("/users/me/favorites/:sessionId", h.Add)
	r.DELETE("/users/me/favorites/:sessionId", h.Remove)
	return r
}

const validFavoriteSessionID = "11111111-1111-1111-1111-111111111111"

func TestFavoritesHandler_List_Unauthenticated(t *testing.T) {
	h := NewFavoritesHandler(&fakeFavoritesReader{})
	r := newFavoritesTestRouter(h, nil)

	w := doRequest(r, http.MethodGet, "/users/me/favorites", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestFavoritesHandler_List_ReturnsItems(t *testing.T) {
	reader := &fakeFavoritesReader{items: []models.Favorite{{SessionID: validFavoriteSessionID}}}
	h := NewFavoritesHandler(reader)
	r := newFavoritesTestRouter(h, testUser)

	w := doRequest(r, http.MethodGet, "/users/me/favorites", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got models.FavoritesList
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].SessionID != validFavoriteSessionID {
		t.Errorf("unexpected items: %+v", got.Items)
	}
}

func TestFavoritesHandler_List_EmptyIsArrayNotNull(t *testing.T) {
	h := NewFavoritesHandler(&fakeFavoritesReader{items: []models.Favorite{}})
	r := newFavoritesTestRouter(h, testUser)

	w := doRequest(r, http.MethodGet, "/users/me/favorites", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Body.String(); got != `{"items":[]}` {
		t.Errorf("body = %s, want {\"items\":[]}", got)
	}
}

func TestFavoritesHandler_List_RepoErrorMapsTo500(t *testing.T) {
	h := NewFavoritesHandler(&fakeFavoritesReader{listErr: errBoom})
	r := newFavoritesTestRouter(h, testUser)

	w := doRequest(r, http.MethodGet, "/users/me/favorites", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestFavoritesHandler_Add_Unauthenticated(t *testing.T) {
	h := NewFavoritesHandler(&fakeFavoritesReader{})
	r := newFavoritesTestRouter(h, nil)

	w := doRequest(r, http.MethodPut, "/users/me/favorites/"+validFavoriteSessionID, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestFavoritesHandler_Add_Succeeds(t *testing.T) {
	reader := &fakeFavoritesReader{}
	h := NewFavoritesHandler(reader)
	r := newFavoritesTestRouter(h, testUser)

	w := doRequest(r, http.MethodPut, "/users/me/favorites/"+validFavoriteSessionID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d, body: %s", w.Code, http.StatusNoContent, w.Body.String())
	}
	if reader.addedSession != validFavoriteSessionID {
		t.Errorf("Add called with %q, want %q", reader.addedSession, validFavoriteSessionID)
	}
}

func TestFavoritesHandler_Add_MalformedSessionIDIs400(t *testing.T) {
	reader := &fakeFavoritesReader{}
	h := NewFavoritesHandler(reader)
	r := newFavoritesTestRouter(h, testUser)

	w := doRequest(r, http.MethodPut, "/users/me/favorites/not-a-uuid", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if reader.addedSession != "" {
		t.Errorf("Add must not be called for a malformed sessionId")
	}
}

func TestFavoritesHandler_Add_RepoErrorMapsTo500(t *testing.T) {
	h := NewFavoritesHandler(&fakeFavoritesReader{addErr: errBoom})
	r := newFavoritesTestRouter(h, testUser)

	w := doRequest(r, http.MethodPut, "/users/me/favorites/"+validFavoriteSessionID, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestFavoritesHandler_Remove_Unauthenticated(t *testing.T) {
	h := NewFavoritesHandler(&fakeFavoritesReader{})
	r := newFavoritesTestRouter(h, nil)

	w := doRequest(r, http.MethodDelete, "/users/me/favorites/"+validFavoriteSessionID, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestFavoritesHandler_Remove_Succeeds(t *testing.T) {
	reader := &fakeFavoritesReader{}
	h := NewFavoritesHandler(reader)
	r := newFavoritesTestRouter(h, testUser)

	w := doRequest(r, http.MethodDelete, "/users/me/favorites/"+validFavoriteSessionID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d, body: %s", w.Code, http.StatusNoContent, w.Body.String())
	}
	if reader.removedSession != validFavoriteSessionID {
		t.Errorf("Remove called with %q, want %q", reader.removedSession, validFavoriteSessionID)
	}
}

func TestFavoritesHandler_Remove_MalformedSessionIDIs400(t *testing.T) {
	reader := &fakeFavoritesReader{}
	h := NewFavoritesHandler(reader)
	r := newFavoritesTestRouter(h, testUser)

	w := doRequest(r, http.MethodDelete, "/users/me/favorites/not-a-uuid", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if reader.removedSession != "" {
		t.Errorf("Remove must not be called for a malformed sessionId")
	}
}

func TestFavoritesHandler_Remove_RepoErrorMapsTo500(t *testing.T) {
	h := NewFavoritesHandler(&fakeFavoritesReader{removeErr: errBoom})
	r := newFavoritesTestRouter(h, testUser)

	w := doRequest(r, http.MethodDelete, "/users/me/favorites/"+validFavoriteSessionID, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
