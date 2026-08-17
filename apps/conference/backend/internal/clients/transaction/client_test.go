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
	"strings"
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

func TestGetTransactionDetails_DecodesPayload(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"payload":{
			"found":true,"success":true,"status":"SUCCESS","amountFormatted":"100.0000",
			"decodedData":{"name":"transfer","args":["0xMASTER","100"]}
		}}`))
	}))
	defer srv.Close()

	got, err := NewClientWithHTTPClient(srv.URL, srv.Client()).
		GetTransactionDetails(context.Background(), "0xHASH")
	if err != nil {
		t.Fatalf("GetTransactionDetails returned error: %v", err)
	}
	if gotPath != "/api/v1/blockchain/get-transaction-details/0xHASH" {
		t.Errorf("path = %q, want the hash as a path segment", gotPath)
	}
	if !got.Found || !got.Success || got.Status != "SUCCESS" {
		t.Errorf("got %+v, want a found, successful transaction", got)
	}
	if got.AmountFormatted == nil || *got.AmountFormatted != "100.0000" {
		t.Errorf("amountFormatted = %v, want 100.0000", got.AmountFormatted)
	}
	if got.DecodedData == nil || got.DecodedData.Name != "transfer" {
		t.Fatalf("decodedData = %+v, want a transfer call", got.DecodedData)
	}
	if len(got.DecodedData.Args) != 2 || got.DecodedData.Args[0] != "0xMASTER" {
		t.Errorf("args = %v, want the recipient first", got.DecodedData.Args)
	}
}

// A hash that isn't on-chain comes back as a 200 with found=false, not an HTTP
// error -- the caller must be able to tell that apart from a transport failure.
func TestGetTransactionDetails_NotFoundIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"payload":{"found":false}}`))
	}))
	defer srv.Close()

	got, err := NewClientWithHTTPClient(srv.URL, srv.Client()).
		GetTransactionDetails(context.Background(), "0xHASH")
	if err != nil {
		t.Fatalf("GetTransactionDetails returned error: %v", err)
	}
	if got.Found {
		t.Error("found = true, want false")
	}
	if got.DecodedData != nil {
		t.Errorf("decodedData = %+v, want nil", got.DecodedData)
	}
}

func TestGetTransactionDetails_UpstreamErrorIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := NewClientWithHTTPClient(srv.URL, srv.Client()).
		GetTransactionDetails(context.Background(), "0xHASH")
	if err == nil {
		t.Error("expected an error for a 502 response")
	}
}

// The hash lands in the request path and this request carries the backend's own
// client-credentials token, so the transport must not let a caller steer it off
// the configured base path -- regardless of whether the caller validated first.
func TestGetTransactionDetails_HashCannotEscapeTheBasePath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"payload":{"found":false}}`))
	}))
	defer srv.Close()

	const wantPrefix = "/api/v1/blockchain/get-transaction-details/"

	for _, hash := range []string{
		"../../../../admin/internal",
		"foo/../../bar",
		"..%2Fadmin",
	} {
		t.Run(hash, func(t *testing.T) {
			gotPath = ""
			_, err := NewClientWithHTTPClient(srv.URL, srv.Client()).
				GetTransactionDetails(context.Background(), hash)
			// Either refusing outright or escaping is acceptable; silently
			// issuing a request to a climbed-out path is not.
			if err != nil {
				return
			}
			if !strings.HasPrefix(gotPath, wantPrefix) {
				t.Errorf("request path = %q, want it to stay under %q", gotPath, wantPrefix)
			}
		})
	}
}

// A segment of only dots survives url.PathEscape untouched, so escaping alone
// would still traverse. This is the case the explicit reject exists for.
func TestGetTransactionDetails_DotSegmentIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no request should have been sent, got %q", r.URL.Path)
	}))
	defer srv.Close()

	for _, hash := range []string{"", ".", "..", "..."} {
		if _, err := NewClientWithHTTPClient(srv.URL, srv.Client()).
			GetTransactionDetails(context.Background(), hash); err == nil {
			t.Errorf("GetTransactionDetails(%q) returned no error, want a refusal", hash)
		}
	}
}
