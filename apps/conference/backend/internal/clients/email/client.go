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

package email

import (
	"bytes"
	"context"
	"encoding/base64"
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
	maxErrBodyBytes = 2048
	oauthHTTPTimeout = 15 * time.Second
)

type Payload struct {
	To       []string `json:"to"`
	From     string   `json:"from"`
	Subject  string   `json:"subject"`
	Template string   `json:"template"`
}

type Client struct {
	baseURL    string
	from       string
	httpClient *http.Client
}

func NewClient(cfg config.EmailServiceConfig) *Client {
	var httpClient *http.Client
	if cfg.OAuth.TokenURL != "" {
		oauthCfg := clientcredentials.Config{
			ClientID:     cfg.OAuth.ClientID,
			ClientSecret: cfg.OAuth.ClientSecret,
			TokenURL:     cfg.OAuth.TokenURL,
		}
		tokenFetchCtx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: oauthHTTPTimeout})
		httpClient = oauthCfg.Client(tokenFetchCtx)
		httpClient.Timeout = oauthHTTPTimeout
	} else {
		httpClient = &http.Client{Timeout: oauthHTTPTimeout}
	}
	return &Client{
		baseURL:    cfg.Endpoint,
		from:       cfg.From,
		httpClient: httpClient,
	}
}

func NewClientWithHTTPClient(baseURL, from string, httpClient *http.Client) *Client {
	return &Client{
		baseURL:    baseURL,
		from:       from,
		httpClient: httpClient,
	}
}

func (c *Client) SendEmail(ctx context.Context, to []string, subject, template string) error {
	if c.baseURL == "" {
		return nil // gracefully skip if email service is not configured locally
	}

	reqURL, err := url.JoinPath(c.baseURL, "send-email")
	if err != nil {
		return fmt.Errorf("email: building URL: %w", err)
	}

	encodedTemplate := base64.StdEncoding.EncodeToString([]byte(template))

	payload := Payload{
		To:       to,
		From:     c.from,
		Subject:  subject,
		Template: encodedTemplate,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("email: encoding payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("email: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("email: request to %s failed: %w", reqURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodyBytes))
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("email: POST %s returned status %d: %s", reqURL, resp.StatusCode, respBody)
	}

	io.Copy(io.Discard, resp.Body)
	return nil
}
