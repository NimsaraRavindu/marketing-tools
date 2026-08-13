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

type WalletClient interface {
	GetPrimaryWallet(ctx context.Context, email, jwtAssertion string) (*models.Wallet, error)
}

type TransactionClient interface {
	GetWalletBalance(ctx context.Context, walletAddress, jwtAssertion string) (float64, error)
}

type LocalBalanceReader interface {
	GetUserAvailableBalance(ctx context.Context, userUUID string) (float64, error)
}

type WalletHandler struct {
	client     WalletClient
	txClient   TransactionClient
	localStore LocalBalanceReader
}

func NewWalletHandler(client WalletClient, txClient TransactionClient, localStore LocalBalanceReader) *WalletHandler {
	return &WalletHandler{client: client, txClient: txClient, localStore: localStore}
}

func (h *WalletHandler) GetBalance(c *gin.Context) {
	user := middleware.UserInfoFromContext(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "missing authentication"})
		return
	}

	wallet, err := h.client.GetPrimaryWallet(c.Request.Context(), user.Email, user.RawToken)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "fetching wallet balance failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}

	if wallet == nil {
		if h.localStore != nil {
			localBal, err := h.localStore.GetUserAvailableBalance(c.Request.Context(), user.UserID)
			if err != nil {
				slog.ErrorContext(c.Request.Context(), "fetching local balance failed", "error", err)
				c.JSON(http.StatusOK, gin.H{"balance": 0})
				return
			}
			c.JSON(http.StatusOK, gin.H{"balance": localBal})
			return
		}
		c.JSON(http.StatusOK, gin.H{"balance": 0})
		return
	}

	balance, err := h.txClient.GetWalletBalance(c.Request.Context(), wallet.WalletAddress, user.RawToken)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "fetching on-chain balance failed", "error", err, "walletAddress", wallet.WalletAddress)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error fetching balance"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"balance": balance})
}
