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

package models

import (
	"time"
)

type ShopItem struct {
	ID             string  `json:"id" db:"id"`
	Name           string  `json:"name" db:"name"`
	Description    *string `json:"description" db:"description"`
	Price          float64 `json:"price" db:"price"`
	ImageURL       string  `json:"imageUrl" db:"image_url"`
	AvailableStock int     `json:"availableStock" db:"available_stock"`
	Category       string  `json:"category" db:"category"`
	MaxPerUser     *int    `json:"maxPerUser" db:"max_per_user"`
	Visibility     string  `json:"visibility" db:"visibility"`
	EventID        *string `json:"eventId" db:"event_id"`
}

type ShopOrder struct {
	ID                    string    `json:"id" db:"id"`
	UserUUID              string    `json:"userUuid" db:"user_uuid"`
	Status                string    `json:"status" db:"status"`
	TransactionHash       *string   `json:"transactionHash" db:"transaction_hash"`
	TotalCoinsAmount      float64   `json:"totalCoinsAmount" db:"total_coins_amount"`
	CreatedOn             time.Time `json:"createdOn" db:"created_on"`
	CreatedBy             string    `json:"createdBy" db:"created_by"`
	UpdatedOn             time.Time `json:"updatedOn" db:"updated_on"`
	UpdatedBy             string    `json:"updatedBy" db:"updated_by"`
	IdempotencyKey        *string   `json:"idempotencyKey" db:"idempotency_key"`
	ShippingRecipientName string    `json:"shippingRecipientName" db:"shipping_recipient_name"`
	ShippingEmail         string    `json:"shippingEmail" db:"shipping_email"`
	ShippingAddressLine1  string    `json:"shippingAddressLine1" db:"shipping_address_line1"`
	ShippingAddressLine2  *string   `json:"shippingAddressLine2" db:"shipping_address_line2"`
	ShippingCity          string    `json:"shippingCity" db:"shipping_city"`
	ShippingState         *string   `json:"shippingState" db:"shipping_state"`
	ShippingPostalCode    *string   `json:"shippingPostalCode" db:"shipping_postal_code"`
	ShippingCountry       string    `json:"shippingCountry" db:"shipping_country"`
	EventID               *string   `json:"eventId" db:"event_id"`

	// Joined/Nested fields
	Items []ShopOrderItem `json:"items,omitempty" db:"-"`
}

type ShopOrderItem struct {
	OrderID   string  `json:"orderId" db:"order_id"`
	ItemID    string  `json:"itemId" db:"item_id"`
	ItemName  string  `json:"itemName" db:"item_name"`
	Quantity  int     `json:"quantity" db:"quantity"`
	UnitPrice float64 `json:"unitPrice" db:"unit_price"`

	// Joined field
	Item *ShopItem `json:"item,omitempty" db:"-"`
}
