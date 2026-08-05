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

	"github.com/gin-gonic/gin"

	"wso2-coin-backend/internal/middleware"
	"wso2-coin-backend/internal/models"
)

// FavoritesReader is satisfied by *repository.FavoritesRepo.
type FavoritesReader interface {
	List(ctx context.Context, userUUID string) ([]models.Favorite, error)
	Add(ctx context.Context, userUUID, sessionID string) error
	Remove(ctx context.Context, userUUID, sessionID string) error
}

// FavoritesHandler exposes the caller's session-favorites HTTP endpoints.
type FavoritesHandler struct {
	favorites FavoritesReader
}

// NewFavoritesHandler constructs a FavoritesHandler.
func NewFavoritesHandler(favorites FavoritesReader) *FavoritesHandler {
	return &FavoritesHandler{favorites: favorites}
}

// List handles GET /users/me/favorites, returning the caller's favorites as
// references (session id + createdAt) the client resolves against live session
// data -- never a stored snapshot of session fields.
func (h *FavoritesHandler) List(c *gin.Context) {
	user := middleware.UserInfoFromContext(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "missing authentication"})
		return
	}

	items, err := h.favorites.List(c.Request.Context(), user.UserID)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "listing favorites failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, models.FavoritesList{Items: items})
}

// Add handles PUT /users/me/favorites/:sessionId. Idempotent: adding a session
// already favorited succeeds with 204 rather than erroring (the repo's INSERT
// ... ON CONFLICT DO NOTHING makes the write a no-op). The sessionId is
// validated as a UUID first so a malformed value is a 400, not a Postgres 500.
func (h *FavoritesHandler) Add(c *gin.Context) {
	user := middleware.UserInfoFromContext(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "missing authentication"})
		return
	}

	sessionID := c.Param("sessionId")
	if !uuidPattern.MatchString(sessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "sessionId must be a valid UUID"})
		return
	}

	if err := h.favorites.Add(c.Request.Context(), user.UserID, sessionID); err != nil {
		slog.ErrorContext(c.Request.Context(), "adding favorite failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}

// Remove handles DELETE /users/me/favorites/:sessionId. Idempotent: removing a
// session that isn't favorited succeeds with 204 (the DELETE affects zero
// rows). The sessionId is validated as a UUID first.
func (h *FavoritesHandler) Remove(c *gin.Context) {
	user := middleware.UserInfoFromContext(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "missing authentication"})
		return
	}

	sessionID := c.Param("sessionId")
	if !uuidPattern.MatchString(sessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "sessionId must be a valid UUID"})
		return
	}

	if err := h.favorites.Remove(c.Request.Context(), user.UserID, sessionID); err != nil {
		slog.ErrorContext(c.Request.Context(), "removing favorite failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}
