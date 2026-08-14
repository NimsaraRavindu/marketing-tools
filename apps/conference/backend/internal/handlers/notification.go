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

// NotificationRecipientReader is satisfied by *repository.AttendeeProfileRepo.
type NotificationRecipientReader interface {
	ListAllUUIDs(ctx context.Context) ([]string, error)
}

// NotificationSender is satisfied by *notification.Client.
type NotificationSender interface {
	SendAttendeeNotification(ctx context.Context, senderUUID string, recipients []string, title, body string) error
}

// NotificationHandler exposes the admin broadcast-push endpoint.
type NotificationHandler struct {
	recipients NotificationRecipientReader
	sender     NotificationSender
	adminRoles []string
}

// NewNotificationHandler constructs a NotificationHandler. adminRoles is the
// allow-list of JWT groups permitted to broadcast (config.Config.AdminRoles,
// from RBAC_ADMIN_ROLES); leaving it empty locks the endpoint down rather
// than opening it up.
func NewNotificationHandler(recipients NotificationRecipientReader, sender NotificationSender, adminRoles []string) *NotificationHandler {
	return &NotificationHandler{recipients: recipients, sender: sender, adminRoles: adminRoles}
}

// Create handles POST /users/notifications: a broadcast push to every
// attendee, restricted to callers in one of the configured admin groups.
//
// The frontend hides its trigger UI behind the same group check, but that is
// cosmetic -- this check is the real gate, and it is the reason the endpoint
// reads the JWT groups claim at all.
//
// Delivery is delegated wholesale to the external notification service, so
// there is no per-recipient status to report and the response carries no body;
// the client treats any 2xx as sent.
func (h *NotificationHandler) Create(c *gin.Context) {
	user := middleware.UserInfoFromContext(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "missing authentication"})
		return
	}

	if !user.HasAnyGroup(h.adminRoles) {
		slog.WarnContext(c.Request.Context(),
			"non-admin attempted to broadcast a notification", "user", user.UserID)
		c.JSON(http.StatusForbidden, gin.H{"message": "forbidden"})
		return
	}

	var req models.UserNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid request body"})
		return
	}
	if problem := req.Validate(); problem != "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": problem})
		return
	}

	recipients, err := h.recipients.ListAllUUIDs(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "listing notification recipients failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}

	if err := h.sender.SendAttendeeNotification(
		c.Request.Context(), user.UserID, recipients, req.Title, req.Description,
	); err != nil {
		slog.ErrorContext(c.Request.Context(), "sending broadcast notification failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}

	slog.InfoContext(c.Request.Context(), "broadcast notification sent",
		"sender", user.UserID, "recipients", len(recipients))
	c.Status(http.StatusOK)
}
