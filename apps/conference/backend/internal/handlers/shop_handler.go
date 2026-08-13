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
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"wso2-coin-backend/internal/middleware"
	"wso2-coin-backend/internal/models"
	"wso2-coin-backend/internal/service"
)

type ShopHandler struct {
	svc        *service.ShopService
	adminRoles []string
}

func NewShopHandler(svc *service.ShopService, adminRoles []string) *ShopHandler {
	return &ShopHandler{svc: svc, adminRoles: adminRoles}
}

// --- Response DTOs matching frontend's expected JSON shape ---

// orderItemResponse maps to the frontend's CartItem interface.
// Frontend expects: { id, name, description, price, imageUrl, quantity, ... }
type orderItemResponse struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Description    *string `json:"description,omitempty"`
	Price          float64 `json:"price"`
	ImageURL       string  `json:"imageUrl,omitempty"`
	Quantity       int     `json:"quantity"`
	AvailableStock *int    `json:"availableStock,omitempty"`
	MaxPerUser     *int    `json:"maxPerUser,omitempty"`
	Category       string  `json:"category,omitempty"`
}

// orderResponse maps to the frontend's OrderRecord interface.
// Frontend expects: { orderId, eventId, status, items, total, date, txHash, shipping... }
type orderResponse struct {
	OrderID               string              `json:"orderId"`
	EventID               *string             `json:"eventId,omitempty"`
	Status                string              `json:"status"`
	Items                 []orderItemResponse `json:"items"`
	Total                 float64             `json:"total"`
	Date                  string              `json:"date"`
	TxHash                *string             `json:"txHash,omitempty"`
	ShippingRecipientName string              `json:"shippingRecipientName,omitempty"`
	ShippingEmail         string              `json:"shippingEmail,omitempty"`
	ShippingAddressLine1  string              `json:"shippingAddressLine1,omitempty"`
	ShippingAddressLine2  *string             `json:"shippingAddressLine2,omitempty"`
	ShippingCity          string              `json:"shippingCity,omitempty"`
	ShippingState         *string             `json:"shippingState,omitempty"`
	ShippingPostalCode    *string             `json:"shippingPostalCode,omitempty"`
	ShippingCountry       string              `json:"shippingCountry,omitempty"`
}

// toOrderResponse converts the internal ShopOrder model to the frontend-compatible shape.
func toOrderResponse(order models.ShopOrder) orderResponse {
	items := make([]orderItemResponse, 0, len(order.Items))
	for _, oi := range order.Items {
		item := orderItemResponse{
			ID:       oi.ItemID,
			Price:    oi.UnitPrice,
			Quantity: oi.Quantity,
		}
		// If the joined ShopItem data is available, populate name/description/image
		if oi.Item != nil {
			item.ID = oi.Item.ID
			item.Name = oi.Item.Name
			item.Description = oi.Item.Description
			item.ImageURL = oi.Item.ImageURL
			item.AvailableStock = &oi.Item.AvailableStock
			item.MaxPerUser = oi.Item.MaxPerUser
			item.Category = oi.Item.Category
		}
		items = append(items, item)
	}

	return orderResponse{
		OrderID:               order.ID,
		EventID:               order.EventID,
		Status:                order.Status,
		Items:                 items,
		Total:                 order.TotalCoinsAmount,
		Date:                  order.CreatedOn.Format("2006-01-02T15:04:05Z07:00"),
		TxHash:                order.TransactionHash,
		ShippingRecipientName: order.ShippingRecipientName,
		ShippingEmail:         order.ShippingEmail,
		ShippingAddressLine1:  order.ShippingAddressLine1,
		ShippingAddressLine2:  order.ShippingAddressLine2,
		ShippingCity:          order.ShippingCity,
		ShippingState:         order.ShippingState,
		ShippingPostalCode:    order.ShippingPostalCode,
		ShippingCountry:       order.ShippingCountry,
	}
}

func (h *ShopHandler) GetVisibleItems(c *gin.Context) {
	items, activeEventID, isShopOpen, err := h.svc.GetVisibleItems(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "failed to get visible items", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}
	
	resp := gin.H{
		"items":      items,
		"isShopOpen": isShopOpen,
	}
	
	if activeEventID != nil {
		resp["activeEventId"] = *activeEventID
	}
	
	c.JSON(http.StatusOK, resp)
}

func (h *ShopHandler) Checkout(c *gin.Context) {
	user := middleware.UserInfoFromContext(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "missing authentication"})
		return
	}

	var req service.CheckoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid request payload"})
		return
	}

	req.UserUUID = user.UserID

	order, err := h.svc.Checkout(c.Request.Context(), req)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "checkout failed", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// Frontend expects { orderId: "..." } to extract the order ID
	c.JSON(http.StatusCreated, gin.H{"orderId": order.ID})
}

func (h *ShopHandler) CheckoutConfirm(c *gin.Context) {
	user := middleware.UserInfoFromContext(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "missing authentication"})
		return
	}

	var req struct {
		OrderID         string  `json:"orderId"`
		TransactionHash *string `json:"transactionHash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid request payload"})
		return
	}

	if err := h.svc.CheckoutConfirm(c.Request.Context(), req.OrderID, user.UserID, user.Email, req.TransactionHash); err != nil {
		slog.ErrorContext(c.Request.Context(), "checkout confirm failed", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// Frontend expects { orderId: "..." } with 200 OK
	c.JSON(http.StatusOK, gin.H{"orderId": req.OrderID})
}

func (h *ShopHandler) GetUserOrders(c *gin.Context) {
	user := middleware.UserInfoFromContext(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "missing authentication"})
		return
	}

	orders, err := h.svc.GetUserOrders(c.Request.Context(), user.UserID)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "failed to get user orders", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}

	// Map internal models to the frontend-expected response shape
	response := make([]orderResponse, 0, len(orders))
	for _, o := range orders {
		response = append(response, toOrderResponse(o))
	}
	c.JSON(http.StatusOK, response)
}

// Admin endpoints

func (h *ShopHandler) GetAllOrders(c *gin.Context) {
	user := middleware.UserInfoFromContext(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "missing authentication"})
		return
	}

	isAdmin := false
	for _, g := range user.Groups {
		for _, adminRole := range h.adminRoles {
			if g == adminRole {
				isAdmin = true
				break
			}
		}
		if isAdmin {
			break
		}
	}
	if !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"message": "admin privileges required"})
		return
	}

	orders, err := h.svc.GetAllOrders(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "failed to get all orders", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, orders)
}

func (h *ShopHandler) UpdateOrderStatus(c *gin.Context) {
	user := middleware.UserInfoFromContext(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "missing authentication"})
		return
	}

	isAdmin := false
	for _, g := range user.Groups {
		for _, adminRole := range h.adminRoles {
			if g == adminRole {
				isAdmin = true
				break
			}
		}
		if isAdmin {
			break
		}
	}
	if !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"message": "admin privileges required"})
		return
	}

	orderID := c.Param("orderId")
	if orderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "missing orderId"})
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid request payload"})
		return
	}

	if err := h.svc.UpdateOrderStatus(c.Request.Context(), orderID, req.Status, user.UserID); err != nil {
		slog.ErrorContext(c.Request.Context(), "failed to update order status", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error"})
		return
	}

	c.Status(http.StatusNoContent)
}
