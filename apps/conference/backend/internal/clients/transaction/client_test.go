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

package transaction

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"wso2-coin-backend/internal/config"
)

func TestNewClient_SetsBaseURLAndTimeout(t *testing.T) {
	c := NewClient(config.ExternalServiceConfig{
		Endpoint: "https://transaction.example.com",
		OAuth: config.OAuthClientConfig{
			TokenURL:     "https://idp.example.com/oauth2/token",
			ClientID:     "client-id",
			ClientSecret: "client-secret",
		},
	})

	if c.baseURL != "https://transaction.example.com" {
		t.Errorf("baseURL = %q, want https://transaction.example.com", c.baseURL)
	}
	if c.httpClient.Timeout != oauthHTTPTimeout {
		t.Errorf("httpClient.Timeout = %v, want %v", c.httpClient.Timeout, oauthHTTPTimeout)
	}
}

func TestTransferToken_Success(t *testing.T) {
	const recipient = "0xABCDEF1234567890"
	const amount = 25.5

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/blockchain/transfer-token" {
			t.Errorf("expected path /api/v1/blockchain/transfer-token, got %q", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}

		var body TransferRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		if body.RecipientWalletAddress != recipient {
			t.Errorf("RecipientWalletAddress = %q, want %q", body.RecipientWalletAddress, recipient)
		}
		if body.Amount != amount {
			t.Errorf("Amount = %v, want %v", body.Amount, amount)
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClientWithHTTPClient(server.URL, server.Client())

	if err := client.TransferToken(context.Background(), recipient, amount); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTransferToken_BadRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid recipient"}`))
	}))
	defer server.Close()

	client := NewClientWithHTTPClient(server.URL, server.Client())

	err := client.TransferToken(context.Background(), "bad-address", 10)
	if err == nil {
		t.Fatal("expected error for 400 response, got nil")
	}
}

func TestTransferToken_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	client := NewClientWithHTTPClient(server.URL, server.Client())

	err := client.TransferToken(context.Background(), "0xSomeWallet", 10)
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

// The blockchain service reports the balance as a *quoted* decimal string.
// Unmarshalling straight into a float64 would fail against the real service, so
// this pins the string decode.
func TestGetBalance_ParsesStringEncodedBalance(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"payload":{"balance":"123.4567"}}`))
	}))
	defer srv.Close()

	c := NewClientWithHTTPClient(srv.URL, srv.Client())
	got, err := c.GetBalance(context.Background(), "0xWALLET")
	if err != nil {
		t.Fatalf("GetBalance returned error: %v", err)
	}
	if got != 123.4567 {
		t.Errorf("balance = %v, want 123.4567", got)
	}
	if gotPath != "/api/v1/blockchain/get-balance/0xWALLET" {
		t.Errorf("path = %q, want the address as a path segment", gotPath)
	}
}

// A wallet that has never received a token is a normal state, not a broken
// response.
func TestGetBalance_EmptyBalanceIsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"payload":{"balance":""}}`))
	}))
	defer srv.Close()

	got, err := NewClientWithHTTPClient(srv.URL, srv.Client()).GetBalance(context.Background(), "0xW")
	if err != nil {
		t.Fatalf("GetBalance returned error: %v", err)
	}
	if got != 0 {
		t.Errorf("balance = %v, want 0", got)
	}
}

func TestGetBalance_UnparseableBalanceIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"payload":{"balance":"not-a-number"}}`))
	}))
	defer srv.Close()

	if _, err := NewClientWithHTTPClient(srv.URL, srv.Client()).GetBalance(context.Background(), "0xW"); err == nil {
		t.Error("expected an error for an unparseable balance")
	}
}

// Unlike the wallet service's primary-wallet lookup, a 404 here is a real fault:
// the address was just resolved, so a miss must not be reported as zero coins.
func TestGetBalance_NotFoundIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := NewClientWithHTTPClient(srv.URL, srv.Client()).GetBalance(context.Background(), "0xW"); err == nil {
		t.Error("expected an error for a 404 balance lookup")
	}
}

func TestGetBalance_ServerErrorIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := NewClientWithHTTPClient(srv.URL, srv.Client()).GetBalance(context.Background(), "0xW"); err == nil {
		t.Error("expected an error for a 500 balance lookup")
	}
}
