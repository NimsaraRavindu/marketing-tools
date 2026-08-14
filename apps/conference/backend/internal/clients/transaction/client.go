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

// Package transaction provides an HTTP client for the external
// Transaction/Blockchain service, which executes token transfers as part of
// the WSO2 Coin / O2C flow.
//
// NOTE: as of this port, nothing in the service layer actually calls
// TransferToken yet — the coin allocation flow currently mirrors the (buggy)
// production behavior of never invoking a real transfer and force-marking
// allocations FAILED instead. This client exists so that behavior is easy to
// flip on later without rebuilding the HTTP integration.
package transaction

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"wso2-coin-backend/internal/config"
)

const (
	// maxErrBodyBytes caps how much of an error response body we read into an
	// error message, so a huge/unexpected body doesn't blow up logs.
	maxErrBodyBytes = 2048
	// oauthHTTPTimeout bounds both the OAuth2 token fetch and the actual API
	// request, so an unreachable IdP or upstream service can't hang the scan
	// flow indefinitely.
	oauthHTTPTimeout = 15 * time.Second
)

// Client is an HTTP client for the external Transaction/Blockchain service.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// TransferRequest is the JSON request body sent to the transfer-token endpoint.
type TransferRequest struct {
	RecipientWalletAddress string  `json:"recipientWalletAddress"`
	Amount                 float64 `json:"amount"`
}

// balanceResponse is the get-balance response envelope.
//
// Balance is a string, not a number: the blockchain service reports a formatted
// token balance as a quoted JSON value. Decoding it into a float64 would fail
// against the real service, so it is parsed explicitly instead.
type balanceResponse struct {
	Payload struct {
		Balance string `json:"balance"`
	} `json:"payload"`
}

// TransactionDetails is the verified subset of a blockchain transaction, as
// returned by the get-transaction-details endpoint.
type TransactionDetails struct {
	// Found is false when the hash is not on-chain at all.
	Found bool `json:"found"`
	// Success and Status are reported separately by the service and are checked
	// independently -- a transaction can be present and mined but not successful.
	Success bool   `json:"success"`
	Status  string `json:"status"`
	// AmountFormatted is the transferred amount as a decimal string, same
	// encoding as the balance field above.
	AmountFormatted *string `json:"amountFormatted"`
	// DecodedData carries the decoded contract call. Verification requires
	// Name == "transfer" and reads the recipient from Args[0].
	DecodedData *struct {
		Name string   `json:"name"`
		Args []string `json:"args"`
	} `json:"decodedData"`
}

// transactionDetailsResponse is the get-transaction-details response envelope.
type transactionDetailsResponse struct {
	Payload TransactionDetails `json:"payload"`
}

// NewClient builds a production Client that authenticates to the
// Transaction/Blockchain service using OAuth2 client-credentials, per cfg.
// The returned client is lazy: it does not contact the token endpoint until
// the first real HTTP request is made.
func NewClient(cfg config.ExternalServiceConfig) *Client {
	oauthCfg := clientcredentials.Config{
		ClientID:     cfg.OAuth.ClientID,
		ClientSecret: cfg.OAuth.ClientSecret,
		TokenURL:     cfg.OAuth.TokenURL,
	}
	// oauth2.HTTPClient bounds the token-fetch request; the same timeout is
	// applied to the returned client below to also bound the actual API call.
	tokenFetchCtx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: oauthHTTPTimeout})
	httpClient := oauthCfg.Client(tokenFetchCtx)
	httpClient.Timeout = oauthHTTPTimeout
	return &Client{
		baseURL:    cfg.Endpoint,
		httpClient: httpClient,
	}
}

// NewClientWithHTTPClient builds a Client pointed at baseURL using httpClient
// directly, bypassing OAuth2 entirely. This is intended for tests, where
// httpClient is typically an httptest.Server's client.
func NewClientWithHTTPClient(baseURL string, httpClient *http.Client) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: httpClient,
	}
}

// TransferToken calls POST {baseURL}/api/v1/blockchain/transfer-token with a
// JSON body of {recipientWalletAddress, amount}. Any non-2xx response is
// returned as an error.
func (c *Client) TransferToken(ctx context.Context, recipientWalletAddress string, amount float64) error {
	reqURL, err := url.JoinPath(c.baseURL, "api", "v1", "blockchain", "transfer-token")
	if err != nil {
		return fmt.Errorf("transaction: building URL: %w", err)
	}

	payload, err := json.Marshal(TransferRequest{
		RecipientWalletAddress: recipientWalletAddress,
		Amount:                 amount,
	})
	if err != nil {
		return fmt.Errorf("transaction: marshaling request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("transaction: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("transaction: request to %s failed: %w", reqURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodyBytes))
		return fmt.Errorf("transaction: POST %s returned status %d: %s", reqURL, resp.StatusCode, body)
	}

	return nil
}

// GetBalance calls GET {baseURL}/api/v1/blockchain/get-balance/{address} and
// returns the wallet's token balance.
//
// Any non-2xx response is an error, including 404: unlike the wallet service's
// primary-wallet lookup, where 404 legitimately means "this user has no wallet",
// a balance lookup for an address that was just resolved should always succeed,
// so a miss is a real fault rather than an empty balance. Reporting zero would
// tell the user they have no coins.
func (c *Client) GetBalance(ctx context.Context, address string) (float64, error) {
	reqURL, err := url.JoinPath(c.baseURL, "api", "v1", "blockchain", "get-balance", address)
	if err != nil {
		return 0, fmt.Errorf("transaction: building URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, fmt.Errorf("transaction: building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("transaction: request to %s failed: %w", reqURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodyBytes))
		return 0, fmt.Errorf("transaction: GET %s returned status %d: %s", reqURL, resp.StatusCode, body)
	}

	var decoded balanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return 0, fmt.Errorf("transaction: decoding response body: %w", err)
	}

	// An absent or empty balance is zero rather than a parse failure: a wallet
	// that has never received a token is a normal state, not a broken response.
	if strings.TrimSpace(decoded.Payload.Balance) == "" {
		return 0, nil
	}

	balance, err := strconv.ParseFloat(strings.TrimSpace(decoded.Payload.Balance), 64)
	if err != nil {
		return 0, fmt.Errorf("transaction: parsing balance %q: %w", decoded.Payload.Balance, err)
	}
	return balance, nil
}

// GetTransactionDetails calls
// GET {baseURL}/api/v1/blockchain/get-transaction-details/{txHash} and returns
// the decoded transaction, for the shop checkout-confirm verification.
//
// This performs no verification itself -- it only fetches and decodes. The
// policy decisions (right recipient, right amount, actually succeeded) live in
// the service layer, so they are testable without an HTTP server and so this
// client stays a transport.
func (c *Client) GetTransactionDetails(ctx context.Context, txHash string) (TransactionDetails, error) {
	reqURL, err := url.JoinPath(c.baseURL, "api", "v1", "blockchain", "get-transaction-details", txHash)
	if err != nil {
		return TransactionDetails{}, fmt.Errorf("transaction: building URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return TransactionDetails{}, fmt.Errorf("transaction: building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return TransactionDetails{}, fmt.Errorf("transaction: request to %s failed: %w", reqURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodyBytes))
		return TransactionDetails{}, fmt.Errorf("transaction: GET %s returned status %d: %s", reqURL, resp.StatusCode, body)
	}

	var decoded transactionDetailsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return TransactionDetails{}, fmt.Errorf("transaction: decoding response body: %w", err)
	}
	return decoded.Payload, nil
}
