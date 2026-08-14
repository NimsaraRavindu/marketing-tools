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
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"wso2-coin-backend/internal/middleware"
	"wso2-coin-backend/internal/models"
)

type fakeWalletLookup struct {
	wallet    *models.Wallet
	err       error
	lastEmail string
}

func (f *fakeWalletLookup) GetPrimaryWallet(ctx context.Context, email string) (*models.Wallet, error) {
	f.lastEmail = email
	return f.wallet, f.err
}

type fakeBalanceLookup struct {
	balance     float64
	err         error
	lastAddress string
	calls       int
}

func (f *fakeBalanceLookup) GetBalance(ctx context.Context, address string) (float64, error) {
	f.calls++
	f.lastAddress = address
	return f.balance, f.err
}

func newWalletTestRouter(h *WalletHandler, user *middleware.UserInfo) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if user != nil {
			c.Request = c.Request.WithContext(middleware.WithUserInfo(c.Request.Context(), user))
		}
		c.Next()
	})
	r.GET("/wallets/balances/me", h.Balance)
	return r
}

func TestWalletHandler_Balance_ReturnsAddressAndBalance(t *testing.T) {
	wallets := &fakeWalletLookup{wallet: &models.Wallet{WalletAddress: "0xWALLET"}}
	balances := &fakeBalanceLookup{balance: 123.45}

	rec := doRequest(newWalletTestRouter(NewWalletHandler(wallets, balances), testUser),
		http.MethodGet, "/wallets/balances/me", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad response: %v", err)
	}
	if got["walletAddress"] != "0xWALLET" {
		t.Errorf("walletAddress = %v, want 0xWALLET", got["walletAddress"])
	}
	// The client does arithmetic on this directly, so it must be a JSON number
	// even though the upstream service reports it as a quoted string.
	if _, isNumber := got["balance"].(float64); !isNumber {
		t.Errorf("balance = %#v, want a JSON number", got["balance"])
	}
	if got["balance"] != 123.45 {
		t.Errorf("balance = %v, want 123.45", got["balance"])
	}

	if wallets.lastEmail != testUser.Email {
		t.Errorf("wallet looked up for %q, want the caller %q", wallets.lastEmail, testUser.Email)
	}
	if balances.lastAddress != "0xWALLET" {
		t.Errorf("balance looked up for %q, want 0xWALLET", balances.lastAddress)
	}
}

// A user who has never created a wallet gets 404, not a zero balance: reporting
// zero would tell them their wallet is empty rather than absent.
func TestWalletHandler_Balance_NoWalletIs404(t *testing.T) {
	wallets := &fakeWalletLookup{wallet: nil}
	balances := &fakeBalanceLookup{}

	rec := doRequest(newWalletTestRouter(NewWalletHandler(wallets, balances), testUser),
		http.MethodGet, "/wallets/balances/me", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["message"] != "Wallet not found" {
		t.Errorf("message = %v, want \"Wallet not found\"", got["message"])
	}
	if balances.calls != 0 {
		t.Error("balance was queried despite there being no wallet address")
	}
}

// A present-but-blank wallet address is the same as no wallet: querying a
// balance for "" would build a URL with an empty path segment.
func TestWalletHandler_Balance_BlankAddressIs404(t *testing.T) {
	wallets := &fakeWalletLookup{wallet: &models.Wallet{WalletAddress: ""}}
	balances := &fakeBalanceLookup{}

	rec := doRequest(newWalletTestRouter(NewWalletHandler(wallets, balances), testUser),
		http.MethodGet, "/wallets/balances/me", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if balances.calls != 0 {
		t.Error("balance was queried for a blank wallet address")
	}
}

func TestWalletHandler_Balance_WalletLookupErrorIs500(t *testing.T) {
	wallets := &fakeWalletLookup{err: errors.New("upstream down")}

	rec := doRequest(newWalletTestRouter(NewWalletHandler(wallets, &fakeBalanceLookup{}), testUser),
		http.MethodGet, "/wallets/balances/me", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// A failed balance lookup must not degrade to zero: that would misreport a
// funded wallet as empty and block a checkout the user could afford.
func TestWalletHandler_Balance_BalanceLookupErrorIs500(t *testing.T) {
	wallets := &fakeWalletLookup{wallet: &models.Wallet{WalletAddress: "0xWALLET"}}
	balances := &fakeBalanceLookup{err: errors.New("chain unreachable")}

	rec := doRequest(newWalletTestRouter(NewWalletHandler(wallets, balances), testUser),
		http.MethodGet, "/wallets/balances/me", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWalletHandler_Balance_MissingUserIs401(t *testing.T) {
	rec := doRequest(newWalletTestRouter(NewWalletHandler(&fakeWalletLookup{}, &fakeBalanceLookup{}), nil),
		http.MethodGet, "/wallets/balances/me", nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// A zero balance is a real answer, not a missing one.
func TestWalletHandler_Balance_ZeroBalanceIsOK(t *testing.T) {
	wallets := &fakeWalletLookup{wallet: &models.Wallet{WalletAddress: "0xWALLET"}}
	balances := &fakeBalanceLookup{balance: 0}

	rec := doRequest(newWalletTestRouter(NewWalletHandler(wallets, balances), testUser),
		http.MethodGet, "/wallets/balances/me", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["balance"] != float64(0) {
		t.Errorf("balance = %v, want 0", got["balance"])
	}
}
