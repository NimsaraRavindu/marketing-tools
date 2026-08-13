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

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"wso2-coin-backend/internal/config"
	"wso2-coin-backend/internal/models"
	"wso2-coin-backend/internal/repository"
)

type ShopService struct {
	repo         repository.ShopRepository
	configRepo   repository.AppConfigReader
	coinRepo     repository.CoinAllocationRepository
	cfg          *config.Config
	txClient     TransactionClient
	emailClient  EmailClient
}

type EmailClient interface {
	SendEmail(ctx context.Context, to []string, subject, template string) error
}

func NewShopService(repo repository.ShopRepository, configRepo repository.AppConfigReader, coinRepo repository.CoinAllocationRepository, txClient TransactionClient, emailClient EmailClient, cfg *config.Config) *ShopService {
	s := &ShopService{
		repo:         repo,
		configRepo:   configRepo,
		coinRepo:     coinRepo,
		txClient:     txClient,
		emailClient:  emailClient,
		cfg:          cfg,
	}

	// Start background cron for stale orders
	go s.staleOrderCron()

	return s
}

func (s *ShopService) GetVisibleItems(ctx context.Context) ([]models.ShopItem, *string, bool, error) {
	activeEventID, err := s.repo.GetActiveEventID(ctx)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to get active event ID: %w", err)
	}
	
	isShopOpen := true
	closingTime, err := s.repo.GetShopClosingTime(ctx, activeEventID)
	if err == nil && closingTime != nil && time.Now().After(*closingTime) {
		isShopOpen = false
	}
	
	items, err := s.repo.GetVisibleItems(ctx, activeEventID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to fetch visible shop items: %w", err)
	}
	return items, activeEventID, isShopOpen, nil
}

func (s *ShopService) GetUserOrders(ctx context.Context, userUUID string) ([]models.ShopOrder, error) {
	activeEventID, err := s.repo.GetActiveEventID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get active event ID: %w", err)
	}
	return s.repo.GetUserOrders(ctx, userUUID, activeEventID)
}

func (s *ShopService) GetAllOrders(ctx context.Context) ([]models.ShopOrder, error) {
	return s.repo.GetAllOrders(ctx)
}

func (s *ShopService) UpdateOrderStatus(ctx context.Context, orderID, status, updatedBy string) error {
	return s.repo.UpdateOrderStatus(ctx, orderID, status, updatedBy)
}

type CheckoutRequestItem struct {
	ID       string `json:"id"`
	Quantity int    `json:"quantity"`
}

type CheckoutRequest struct {
	UserUUID              string                `json:"-"`
	IdempotencyKey        *string               `json:"idempotencyKey"`
	EventID               *string               `json:"eventId"`
	TotalCost             float64               `json:"totalCost"`
	Items                 []CheckoutRequestItem `json:"items"`
	ShippingRecipientName string                `json:"shippingRecipientName"`
	ShippingEmail         string                `json:"shippingEmail"`
	ShippingAddressLine1  string                `json:"shippingAddressLine1"`
	ShippingAddressLine2  *string               `json:"shippingAddressLine2"`
	ShippingCity          string                `json:"shippingCity"`
	ShippingState         *string               `json:"shippingState"`
	ShippingPostalCode    *string               `json:"shippingPostalCode"`
	ShippingCountry       string                `json:"shippingCountry"`
}

func (s *ShopService) Checkout(ctx context.Context, req CheckoutRequest) (models.ShopOrder, error) {
	activeEventID, err := s.repo.GetActiveEventID(ctx)
	if err != nil {
		return models.ShopOrder{}, fmt.Errorf("failed to get active event ID: %w", err)
	}

	// 1. Idempotency Check
	if req.IdempotencyKey != nil && *req.IdempotencyKey != "" {
		existingOrder, err := s.repo.GetPendingOrderByIdempotencyKey(ctx, req.UserUUID, *req.IdempotencyKey)
		if err != nil {
			return models.ShopOrder{}, fmt.Errorf("failed to check idempotency key: %w", err)
		}
		if existingOrder != nil {
			// Update shipping details
			updateReq := models.ShopOrder{
				ShippingRecipientName: req.ShippingRecipientName,
				ShippingEmail:         req.ShippingEmail,
				ShippingAddressLine1:  req.ShippingAddressLine1,
				ShippingAddressLine2:  req.ShippingAddressLine2,
				ShippingCity:          req.ShippingCity,
				ShippingState:         req.ShippingState,
				ShippingPostalCode:    req.ShippingPostalCode,
				ShippingCountry:       req.ShippingCountry,
			}
			err = s.repo.UpdateShopOrderShippingDetails(ctx, existingOrder.ID, updateReq)
			if err != nil {
				slog.ErrorContext(ctx, "Failed to update shipping details on idempotent request", "error", err)
			}
			return *existingOrder, nil
		}
	}

	// 2. Check shop closing time from conference config
	closingTime, err := s.repo.GetShopClosingTime(ctx, activeEventID)
	if err != nil {
		return models.ShopOrder{}, fmt.Errorf("failed to get shop closing time: %w", err)
	}
	if closingTime != nil && time.Now().After(*closingTime) {
		return models.ShopOrder{}, errors.New("shop is closed")
	}

	// 3. Check pending orders limit
	pendingCount, err := s.repo.GetUserPendingOrdersCount(ctx, req.UserUUID, activeEventID)
	if err != nil {
		return models.ShopOrder{}, fmt.Errorf("failed to check pending orders: %w", err)
	}
	if pendingCount >= s.cfg.CoinMaxPendingOrdersPerUser {
		return models.ShopOrder{}, errors.New("max pending orders limit reached")
	}

	// 4. Validate items against activeEventID
	items, err := s.repo.GetVisibleItems(ctx, activeEventID)
	if err != nil {
		return models.ShopOrder{}, fmt.Errorf("failed to get visible items: %w", err)
	}

	itemsMap := make(map[string]models.ShopItem)
	for _, i := range items {
		itemsMap[i.ID] = i
	}

	// Fetch past purchased quantities for maxPerUser check
	pastPurchased, err := s.repo.GetPastPurchasedQuantities(ctx, req.UserUUID, activeEventID)
	if err != nil {
		return models.ShopOrder{}, fmt.Errorf("failed to fetch past purchased quantities: %w", err)
	}

	var totalCoins float64
	var finalItems []models.ShopOrderItem
	for _, orderItem := range req.Items {
		item, ok := itemsMap[orderItem.ID]
		if !ok {
			return models.ShopOrder{}, fmt.Errorf("item not found or not visible: %s", orderItem.ID)
		}
		
		// 5. Max per user limit check (combining current order quantity + past purchases)
		if item.MaxPerUser != nil {
			pastQty := pastPurchased[item.ID]
			if pastQty+orderItem.Quantity > *item.MaxPerUser {
				return models.ShopOrder{}, fmt.Errorf("quantity exceeds max per user for item: %s", item.Name)
			}
		}
		
		finalItems = append(finalItems, models.ShopOrderItem{
			ItemID:    item.ID,
			Quantity:  orderItem.Quantity,
			UnitPrice: item.Price,
		})
		
		totalCoins += item.Price * float64(orderItem.Quantity)
	}

	if totalCoins <= 0 {
		return models.ShopOrder{}, errors.New("invalid total cost")
	}

	// 6. Validate client-provided totalCost matches server computation
	if req.TotalCost != totalCoins {
		return models.ShopOrder{}, errors.New("Total cost mismatch. Please refresh your cart and try again.")
	}

	// 7. Check idempotency (if the user already submitted this request and it is PENDING)
	if req.IdempotencyKey != nil && *req.IdempotencyKey != "" {
		existing, err := s.repo.GetPendingOrderByIdempotencyKey(ctx, req.UserUUID, *req.IdempotencyKey)
		if err != nil {
			return models.ShopOrder{}, fmt.Errorf("failed to check idempotency key: %w", err)
		}
		if existing != nil {
			// Update shipping details on the existing pending order
			updateReq := models.ShopOrder{
				ShippingRecipientName: req.ShippingRecipientName,
				ShippingEmail:         req.ShippingEmail,
				ShippingAddressLine1:  req.ShippingAddressLine1,
				ShippingAddressLine2:  req.ShippingAddressLine2,
				ShippingCity:          req.ShippingCity,
				ShippingState:         req.ShippingState,
				ShippingPostalCode:    req.ShippingPostalCode,
				ShippingCountry:       req.ShippingCountry,
			}
			if err := s.repo.UpdateShopOrderShippingDetails(ctx, existing.ID, updateReq); err != nil {
				return models.ShopOrder{}, fmt.Errorf("failed to update shipping details: %w", err)
			}
			return *existing, nil
		}
	}

	orderID := "ORD-" + uuid.NewString()
	order := models.ShopOrder{
		ID:                    orderID,
		UserUUID:              req.UserUUID,
		Status:                "PENDING",
		TotalCoinsAmount:      totalCoins,
		CreatedOn:             time.Now(),
		CreatedBy:             req.UserUUID,
		UpdatedOn:             time.Now(),
		UpdatedBy:             req.UserUUID,
		IdempotencyKey:        req.IdempotencyKey,
		EventID:               activeEventID,
		ShippingRecipientName: req.ShippingRecipientName,
		ShippingEmail:         req.ShippingEmail,
		ShippingAddressLine1:  req.ShippingAddressLine1,
		ShippingAddressLine2:  req.ShippingAddressLine2,
		ShippingCity:          req.ShippingCity,
		ShippingState:         req.ShippingState,
		ShippingPostalCode:    req.ShippingPostalCode,
		ShippingCountry:       req.ShippingCountry,
		Items:                 finalItems,
	}

	if err := s.repo.CreateOrder(ctx, order); err != nil {
		return models.ShopOrder{}, fmt.Errorf("failed to create order: %w", err)
	}

	return order, nil
}

func (s *ShopService) CheckoutConfirm(ctx context.Context, orderID string, userUUID string, userEmail string, txHash *string) error {
	// Transition the order status from PENDING to CONFIRMED.
	
	// Fetch the order directly
	found, err := s.repo.GetOrderWithItemsById(ctx, orderID)
	if err != nil {
		return fmt.Errorf("failed to fetch order: %w", err)
	}

	if found == nil || found.UserUUID != userUUID {
		return errors.New("order not found or does not belong to user")
	}

	if found.Status != "PENDING" {
		return fmt.Errorf("order is %s. Cannot confirm.", found.Status)
	}

	if txHash != nil && *txHash != "" {
		// Check transaction hash uniqueness across all orders
		existingTx, err := s.repo.GetOrderByTransactionHash(ctx, *txHash)
		if err != nil {
			return fmt.Errorf("failed to check transaction hash reuse: %w", err)
		}
		if existingTx != nil && existingTx.ID != orderID {
			return errors.New("This transaction hash has already been used for another order.")
		}

		err = s.txClient.ConfirmTransaction(ctx, *txHash, s.cfg.CoinMasterWalletAddress, found.TotalCoinsAmount)
		if err != nil {
			slog.ErrorContext(ctx, "transaction verification failed", "txHash", *txHash, "error", err)
			// Restore stock on payment failure
			if cancelErr := s.repo.CancelOrderAndRestoreStock(ctx, orderID); cancelErr != nil {
				slog.ErrorContext(ctx, "failed to cancel order and restore stock", "order_id", orderID, "error", cancelErr)
			}
			return fmt.Errorf("transaction verification failed: %w", err)
		}
	}

	err = s.repo.ConfirmOrder(ctx, orderID, userUUID, txHash)
	if err != nil {
		return err
	}

	// Send confirmation email asynchronously
	go func(o models.ShopOrder) {
		// Use a background context since the parent ctx might be cancelled
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		eventName, _ := s.repo.GetEventName(bgCtx, o.EventID)
		subject := "Order Confirmation - Event Shop"
		if eventName != "" {
			subject = fmt.Sprintf("Order Confirmation - %s Event Shop", eventName)
		}

		recipientName := o.ShippingRecipientName
		if recipientName == "" {
			recipientName = "there"
		}

		htmlContent := GenerateOrderConfirmationEmail(o, recipientName)

		var emailTo string
		if o.ShippingEmail != "" {
			emailTo = o.ShippingEmail
		} else {
			emailTo = userEmail
		}
		
		if emailTo != "" {
			err := s.emailClient.SendEmail(bgCtx, []string{emailTo}, subject, htmlContent)
			if err != nil {
				slog.Error("failed to send order confirmation email", "orderID", o.ID, "error", err)
			} else {
				slog.Info("Order confirmation email sent", "orderID", o.ID, "email", emailTo)
			}
		}
	}(*found)

	return nil
}

func (s *ShopService) staleOrderCron() {
	interval := time.Duration(s.cfg.StaleOrderCleanupIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		count, err := s.repo.MarkStaleOrders(ctx, s.cfg.CoinStaleOrderTimeoutMinutes)
		cancel()
		if err != nil {
			slog.Error("staleOrderCron", "error", err)
		} else if count > 0 {
			slog.Info(fmt.Sprintf("staleOrderCron marked %d orders as stale", count))
		}
	}
}
