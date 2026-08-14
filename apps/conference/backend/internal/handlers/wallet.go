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

// WalletLookup resolves a user's primary wallet. Satisfied by *wallet.Client.
//
// A nil wallet with a nil error means "this user has no wallet" -- the client
// maps the upstream service's 404 to that rather than to a failure, and this
// handler depends on the distinction.
type WalletLookup interface {
	GetPrimaryWallet(ctx context.Context, email string) (*models.Wallet, error)
}

// BalanceLookup reads a wallet address's token balance. Satisfied by
// *transaction.Client.
type BalanceLookup interface {
	GetBalance(ctx context.Context, address string) (float64, error)
}

// WalletHandler exposes the wallet balance endpoint.
//
// Nothing here touches the database: this endpoint is a pass-through over two
// external services, which is why it has no repository and no service layer.
type WalletHandler struct {
	wallets  WalletLookup
	balances BalanceLookup
}

// NewWalletHandler constructs a WalletHandler.
func NewWalletHandler(wallets WalletLookup, balances BalanceLookup) *WalletHandler {
	return &WalletHandler{wallets: wallets, balances: balances}
}

// Balance handles GET /wallets/balances/me, returning {walletAddress, balance}
// for the authenticated caller.
//
// A caller with no wallet gets 404, not a zero balance: "you have not created a
// wallet" and "your wallet is empty" call for different things from the user, and
// reporting zero for the former hides the fact that there is nothing to pay from.
//
// The upstream balance arrives as a quoted decimal string and is emitted here as
// a JSON number, matching the legacy response and the client, which does
// arithmetic on it directly.
func (h *WalletHandler) Balance(c *gin.Context) {
	user := middleware.UserInfoFromContext(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "missing authentication"})
		return
	}

	w, err := h.wallets.GetPrimaryWallet(c.Request.Context(), user.Email)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "retrieving primary wallet failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}
	if w == nil || w.WalletAddress == "" {
		c.JSON(http.StatusNotFound, gin.H{"message": "Wallet not found"})
		return
	}

	balance, err := h.balances.GetBalance(c.Request.Context(), w.WalletAddress)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "retrieving blockchain wallet balance failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}

	c.JSON(http.StatusOK, models.WalletBalance{
		WalletAddress: w.WalletAddress,
		Balance:       balance,
	})
}
