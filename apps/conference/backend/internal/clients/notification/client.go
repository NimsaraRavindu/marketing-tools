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

// Package notification provides an HTTP client for the external WSO2
// notification service, which owns the actual push delivery (FCM and
// everything under it). This backend only assembles the recipient list and
// hands it over -- there is no Firebase integration here, the same split the
// old Ballerina service had.
package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"wso2-coin-backend/internal/config"
)

const (
	// maxErrBodyBytes caps how much of an error response body we read into an
	// error message, so a huge/unexpected body doesn't blow up logs.
	maxErrBodyBytes = 2048
	// requestTimeout bounds both the OAuth2 token fetch and the send itself.
	// Generous relative to the other clients: the request body carries every
	// attendee uuid in one shot, so the service has real work to do before it
	// answers.
	requestTimeout = 60 * time.Second

	// eventType and source are fixed strings the notification service routes
	// on. Carried over verbatim from the old Ballerina payload -- changing
	// either silently stops notifications being delivered.
	eventType = "attendee_notification"
	source    = "wso2con"
)

// Client is an HTTP client for the external notification service.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient builds a production Client that authenticates using OAuth2
// client-credentials, per cfg. The returned client is lazy: it does not
// contact the token endpoint until the first real request.
func NewClient(cfg config.ExternalServiceConfig) *Client {
	oauthCfg := clientcredentials.Config{
		ClientID:     cfg.OAuth.ClientID,
		ClientSecret: cfg.OAuth.ClientSecret,
		TokenURL:     cfg.OAuth.TokenURL,
		Scopes:       cfg.OAuth.Scopes,
	}
	tokenFetchCtx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: requestTimeout})
	httpClient := oauthCfg.Client(tokenFetchCtx)
	httpClient.Timeout = requestTimeout
	return &Client{baseURL: cfg.Endpoint, httpClient: httpClient}
}

// NewClientWithHTTPClient builds a Client pointed at baseURL using httpClient
// directly, bypassing OAuth2 entirely. Intended for tests.
func NewClientWithHTTPClient(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, httpClient: httpClient}
}

// userList is the {"users": [...]} envelope the service wraps both the
// sender and the recipients in.
type userList struct {
	Users []string `json:"users"`
}

type notificationContext struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type eventRequest struct {
	Actor     userList            `json:"actor"`
	Context   notificationContext `json:"context"`
	EventType string              `json:"eventType"`
	Source    string              `json:"source"`
	Target    userList            `json:"target"`
}

// SendAttendeeNotification pushes one notification to every uuid in
// recipients via POST {baseURL}/event, attributed to senderUUID.
//
// This is a single call carrying the whole recipient list, not a per-recipient
// fan-out, so there is no partial success to report: it either delivers to the
// service or it doesn't. Passing an empty recipients list is a no-op rather
// than a request, since a broadcast to nobody has nothing to deliver.
func (c *Client) SendAttendeeNotification(ctx context.Context, senderUUID string, recipients []string, title, body string) error {
	if len(recipients) == 0 {
		return nil
	}

	payload := eventRequest{
		Actor:     userList{Users: []string{senderUUID}},
		Context:   notificationContext{Title: title, Body: body},
		EventType: eventType,
		Source:    source,
		Target:    userList{Users: recipients},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notification: encoding request body: %w", err)
	}

	reqURL, err := url.JoinPath(c.baseURL, "event")
	if err != nil {
		return fmt.Errorf("notification: building URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("notification: building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("notification: request to %s failed: %w", reqURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodyBytes))
		return fmt.Errorf("notification: POST %s returned status %d: %s", reqURL, resp.StatusCode, errBody)
	}

	// The service answers with {event_id, message}; nothing downstream needs
	// either, and the handler returns no body, so the response is discarded.
	return nil
}
