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
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"wso2-coin-backend/internal/clients/aiagent"
	"wso2-coin-backend/internal/middleware"
	"wso2-coin-backend/internal/models"
)

// This file holds the admin-only management routes for the two con-ai datasets
// the mobile app never writes: the O2Bar engineer roster and the attendee AI
// profiles that feed matchmaking / picked-for-you. con-ai exposes both as
// unrestricted routes (its own docstrings say "roster management, unrestricted"
// and "any caller may replace any attendee's researched profile"), and it
// authenticates nobody -- the only thing in front of it is the managed gateway,
// which checks this backend's token, not the caller's. So the caller-facing gate
// lives here: every handler below requires the caller to be in one of
// h.aiAdminRoles before it will proxy anything, mirroring
// NotificationHandler.Create. An empty allow-list denies everyone.
//
// Unlike the six attendee-facing AI routes, these carry no featureStatus gate:
// an operator seeds the roster and profiles *before* switching O2Bar or the
// personalization features on, so gating them on the feature flag would make the
// one workflow that needs them impossible.

// requireAIAdmin resolves the caller and enforces the admin allow-list. It
// returns the caller and true when the request may proceed; otherwise it has
// already written the 401/403 response and the handler must return.
func (h *AIAgentHandler) requireAIAdmin(c *gin.Context) (*middleware.UserInfo, bool) {
	user := middleware.UserInfoFromContext(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "missing authentication"})
		return nil, false
	}
	if !user.HasAnyGroup(h.aiAdminRoles) {
		slog.WarnContext(c.Request.Context(),
			"non-admin attempted an AI management action", "user", user.UserID, "path", c.FullPath())
		c.JSON(http.StatusForbidden, gin.H{"message": "forbidden"})
		return nil, false
	}
	return user, true
}

// requiredEmailQuery reads the mandatory ?email= query parameter, writing a 400
// and returning ("", false) when it is absent. The engineer/profile routes key
// on email, so a missing one is a client error, not an upstream call.
func requiredEmailQuery(c *gin.Context) (string, bool) {
	email := c.Query("email")
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "email is required"})
		return "", false
	}
	return email, true
}

// CreateEngineer handles POST /admin/o2bar/engineers: register or (with
// Override) overwrite an O2Bar engineer. con-ai answers 201 even for an
// already-exists no-op, so a 201 here does not guarantee a new row -- the
// Created flag in the body does.
func (h *AIAgentHandler) CreateEngineer(c *gin.Context) {
	user, ok := h.requireAIAdmin(c)
	if !ok {
		return
	}

	var req models.EngineerCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid request body"})
		return
	}

	resp, err := h.client.CreateEngineer(c.Request.Context(), user.RawToken, req)
	if err != nil {
		respondAIAdminError(c, "creating O2Bar engineer failed", err, "")
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// ListEngineers handles GET /admin/o2bar/engineers: the O2Bar staff directory
// with each engineer's availability. Returns [] (never null) when the roster is
// empty, so the client can render an empty list without a nil check.
func (h *AIAgentHandler) ListEngineers(c *gin.Context) {
	user, ok := h.requireAIAdmin(c)
	if !ok {
		return
	}

	engineers, err := h.client.ListEngineers(c.Request.Context(), user.RawToken)
	if err != nil {
		respondAIAdminError(c, "listing O2Bar engineers failed", err, "")
		return
	}
	if engineers == nil {
		engineers = []models.EngineerSummary{}
	}
	c.JSON(http.StatusOK, engineers)
}

// DeleteEngineer handles DELETE /admin/o2bar/engineers?email=: remove an
// engineer from every attendee's recommendations. A 404 from con-ai means no
// engineer holds that email.
func (h *AIAgentHandler) DeleteEngineer(c *gin.Context) {
	user, ok := h.requireAIAdmin(c)
	if !ok {
		return
	}
	email, ok := requiredEmailQuery(c)
	if !ok {
		return
	}

	if err := h.client.DeleteEngineer(c.Request.Context(), user.RawToken, email); err != nil {
		respondAIAdminError(c, "deleting O2Bar engineer failed", err, "engineer not found")
		return
	}
	c.Status(http.StatusNoContent)
}

// EngineerExists handles GET /admin/o2bar/engineers/exists?email=: whether that
// address belongs to an engineer on O2Bar duty.
func (h *AIAgentHandler) EngineerExists(c *gin.Context) {
	user, ok := h.requireAIAdmin(c)
	if !ok {
		return
	}
	email, ok := requiredEmailQuery(c)
	if !ok {
		return
	}

	resp, err := h.client.EngineerExists(c.Request.Context(), user.RawToken, email)
	if err != nil {
		respondAIAdminError(c, "checking O2Bar engineer existence failed", err, "")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// AdminCreateProfile handles POST /admin/ai-profiles: create or (with Override)
// replace the researched AI profile of an arbitrary attendee. Unlike
// POST /users/profile -- which pins the profile to the caller's own JWT email --
// the target email rides in the body, so it must be present.
func (h *AIAgentHandler) AdminCreateProfile(c *gin.Context) {
	user, ok := h.requireAIAdmin(c)
	if !ok {
		return
	}

	var req models.AdminProfileCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid request body"})
		return
	}
	if req.User.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "user.email is required"})
		return
	}

	resp, err := h.client.AdminCreateProfile(c.Request.Context(), user.RawToken, req)
	if err != nil {
		respondAIAdminError(c, "creating attendee AI profile failed", err, "")
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// AdminGetProfile handles GET /admin/ai-profiles?email=: the stored AI profile
// document for an attendee. con-ai returns an arbitrary object, relayed as-is.
func (h *AIAgentHandler) AdminGetProfile(c *gin.Context) {
	user, ok := h.requireAIAdmin(c)
	if !ok {
		return
	}
	email, ok := requiredEmailQuery(c)
	if !ok {
		return
	}

	profile, err := h.client.AdminGetProfile(c.Request.Context(), user.RawToken, email)
	if err != nil {
		respondAIAdminError(c, "fetching attendee AI profile failed", err, "profile not found")
		return
	}
	c.JSON(http.StatusOK, profile)
}

// AdminUpdateProfile handles PATCH /admin/ai-profiles?email=: re-embed an
// attendee's profile around fresh LinkedIn text.
func (h *AIAgentHandler) AdminUpdateProfile(c *gin.Context) {
	user, ok := h.requireAIAdmin(c)
	if !ok {
		return
	}
	email, ok := requiredEmailQuery(c)
	if !ok {
		return
	}

	var req models.AdminProfileUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid request body"})
		return
	}

	profile, err := h.client.AdminUpdateProfile(c.Request.Context(), user.RawToken, email, req)
	if err != nil {
		respondAIAdminError(c, "updating attendee AI profile failed", err, "profile not found")
		return
	}
	c.JSON(http.StatusOK, profile)
}

// AdminDeleteProfile handles DELETE /admin/ai-profiles?email=: drop an
// attendee's AI profile. A 404 means no profile is stored for that email.
func (h *AIAgentHandler) AdminDeleteProfile(c *gin.Context) {
	user, ok := h.requireAIAdmin(c)
	if !ok {
		return
	}
	email, ok := requiredEmailQuery(c)
	if !ok {
		return
	}

	if err := h.client.AdminDeleteProfile(c.Request.Context(), user.RawToken, email); err != nil {
		respondAIAdminError(c, "deleting attendee AI profile failed", err, "profile not found")
		return
	}
	c.Status(http.StatusNoContent)
}

// ProfileExists handles GET /admin/ai-profiles/exists?email=: whether an
// attendee has a stored AI profile (a registration oracle on con-ai's side).
func (h *AIAgentHandler) ProfileExists(c *gin.Context) {
	user, ok := h.requireAIAdmin(c)
	if !ok {
		return
	}
	email, ok := requiredEmailQuery(c)
	if !ok {
		return
	}

	resp, err := h.client.ProfileExists(c.Request.Context(), user.RawToken, email)
	if err != nil {
		respondAIAdminError(c, "checking attendee AI profile existence failed", err, "")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// respondAIAdminError maps an aiagent client error to a client-facing response
// for the admin routes.
//
// The split turns on who is at fault. A 401/403 from the upstream hop is never
// con-ai (it authenticates nobody) and never anything the admin typed -- it is
// the managed gateway refusing *this backend's* OAuth token -- so it goes
// through respondAIUpstreamError, which emits the generic 500 plus the
// credential hint the six attendee-facing routes already use, and keeps the
// gateway's token taxonomy out of the admin's response. con-ai's own 400/404/
// 409/422 are client-fixable (a bad time-slot format, an unknown email) and are
// relayed at the same status with the upstream's own message, so the admin sees
// what to correct. Everything else -- a transport failure, a rejected token
// fetch, an unexpected 5xx -- falls back to respondAIUpstreamError's
// unreachable-vs-bug handling.
//
// notFoundMessage, when non-empty, is the message substituted for an upstream
// 404 so a delete/get of an absent engineer or profile reads as a clean
// resource-not-found rather than con-ai's internal phrasing.
func respondAIAdminError(c *gin.Context, logMsg string, err error, notFoundMessage string) {
	var statusErr *aiagent.StatusError
	if !errors.As(err, &statusErr) {
		respondAIUpstreamError(c, logMsg, err)
		return
	}

	switch statusErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		// Gateway refused this backend's credentials; not the admin's problem to
		// fix and it carries the gateway's token codes, so use the shared path.
		respondAIUpstreamError(c, logMsg, err)
		return
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity:
		slog.WarnContext(c.Request.Context(), logMsg,
			"upstreamStatus", statusErr.StatusCode, "upstreamURL", statusErr.URL)
		fallback := "request rejected by the AI service"
		if statusErr.StatusCode == http.StatusNotFound && notFoundMessage != "" {
			fallback = notFoundMessage
		}
		message := extractUpstreamMessage(statusErr.Body)
		if message == "" {
			message = fallback
		}
		c.JSON(statusErr.StatusCode, gin.H{"message": message})
		return
	default:
		respondAIUpstreamError(c, logMsg, err)
	}
}

// extractUpstreamMessage pulls a human-readable reason out of an upstream JSON
// error body, trying con-ai's FastAPI shape ({"detail": ...}) first and this
// codebase's own shape ({"message": ...}) second. It returns "" when the body
// is not JSON or carries neither key, leaving the caller to supply a default. A
// non-string "detail" (FastAPI validation errors are a list) is ignored rather
// than rendered as Go's map/slice formatting.
func extractUpstreamMessage(body string) string {
	if body == "" {
		return ""
	}
	var parsed struct {
		Detail  json.RawMessage `json:"detail"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return ""
	}
	if len(parsed.Detail) > 0 {
		var detail string
		if err := json.Unmarshal(parsed.Detail, &detail); err == nil && detail != "" {
			return detail
		}
	}
	return parsed.Message
}
