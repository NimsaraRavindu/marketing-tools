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

	"wso2-coin-backend/internal/models"
)

// ActivityReader is satisfied by *repository.ActivityRepo.
type ActivityReader interface {
	List(ctx context.Context) ([]models.Activity, error)
}

// ActivityHandler exposes the read-only activities HTTP endpoint.
type ActivityHandler struct {
	activities ActivityReader
}

// NewActivityHandler constructs an ActivityHandler.
func NewActivityHandler(activities ActivityReader) *ActivityHandler {
	return &ActivityHandler{activities: activities}
}

// List handles GET /activities, returning every activity as a flat array.
// Activities that share a name are separate entries here; the client groups
// them into one card with several times.
//
// Unlike the old service this sits behind the JWT-gated route group, since
// there is no unauthenticated group in this server -- the old endpoint took no
// request context at all and was reachable without a token.
func (h *ActivityHandler) List(c *gin.Context) {
	activities, err := h.activities.List(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "fetching activities failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}
	if activities == nil {
		activities = []models.Activity{}
	}
	c.JSON(http.StatusOK, activities)
}
