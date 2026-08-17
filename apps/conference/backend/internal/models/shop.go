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
	"errors"
	"math"
	"strings"
	"time"
)

// Shop item visibility values, from the shared shop_item.visibility column
// (upstream migration 019, which replaced the boolean is_active from 017).
//
// VISIBLE and HIDDEN are what the organizer's admin UI can set. DELETED is
// terminal and only ever written server-side by that UI's delete path, which
// soft-deletes rather than removing rows so orders referencing the item keep
// their FK. The attendee-facing catalog shows VISIBLE only, so the distinction
// between HIDDEN and DELETED does not matter here -- but the set is three
// values, not a boolean, and code must not assume otherwise.
const (
	ShopVisibilityVisible = "VISIBLE"
	ShopVisibilityHidden  = "HIDDEN"
	ShopVisibilityDeleted = "DELETED"
)

// Shop order statuses, from the shared shop_order.status column.
//
// Only PENDING and CONFIRMED are ever written by this service: an order starts
// PENDING at checkout and becomes CONFIRMED once its on-chain payment verifies.
// FULFILLED, EXPIRED and FAILED are set by the organizer's admin UI (physical
// fulfillment, or reversing an abandoned order and restoring its stock).
const (
	ShopOrderStatusPending   = "PENDING"
	ShopOrderStatusConfirmed = "CONFIRMED"
	ShopOrderStatusFulfilled = "FULFILLED"
	ShopOrderStatusExpired   = "EXPIRED"
	ShopOrderStatusFailed    = "FAILED"
)

// coinScale is the number of decimal places the shared schema stores coin
// amounts at (DECIMAL(10,4) on shop_item.price, shop_order.total_coins_amount
// and shop_order_item.unit_price).
const coinScale = 10000

// ScaleCoins converts a coin amount to an integer number of ten-thousandths,
// the precision the database itself stores.
//
// Coin amounts travel as JSON numbers and are held as float64, so comparing two
// of them with == invites a rounding artifact deciding whether a payment is
// valid. Every equality comparison on money goes through this instead, which
// makes "equal" mean "equal at the precision the schema actually keeps" rather
// than "identical IEEE-754 bits".
func ScaleCoins(amount float64) int64 {
	return int64(math.Round(amount * coinScale))
}

// ShopItem is one catalog entry, as returned inside GET /shops/items.
//
// Stock is exposed as AvailableStock; the client also reads an optional `stock`
// which nothing upstream stores, so it is not emitted. MaxPerUser is a pointer
// because "no limit" (NULL) and "a limit of zero" are different things and the
// client keys its per-item cap on the field being present at all.
type ShopItem struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	Price          float64 `json:"price"`
	ImageURL       string  `json:"imageUrl"`
	AvailableStock int     `json:"availableStock"`
	MaxPerUser     *int    `json:"maxPerUser,omitempty"`
	Category       string  `json:"category"`
	Visibility     string  `json:"visibility"`
}

// ShopCatalog is the GET /shops/items response.
//
// Deliberately an envelope rather than the bare array both the legacy service
// and the organizer's admin API return: the client needs to know whether the
// shop is still open and which event the catalog belongs to, and it reads both
// off this response (Shop.tsx). Neither is derivable from the item list.
type ShopCatalog struct {
	Items []ShopItem `json:"items"`
	// IsShopOpen is false once the event's conference_config.shop_closing_time
	// has passed. A NULL closing time means the shop never closes.
	IsShopOpen bool `json:"isShopOpen"`
	// ActiveEventID is the conference the catalog is scoped to. The client pins
	// its cart and its per-item purchase history to this, so it must be the same
	// event the checkout path will write orders against.
	ActiveEventID string `json:"activeEventId"`
}

// ShopOrderLine is one purchased line in an order's history entry.
//
// The item identity and unit price are each emitted under two names. The client
// reads `itemId || id` and `priceAtPurchase || price`, because it was written
// against two different backends whose field names disagreed; emitting both
// spellings means it resolves correctly regardless of which branch it takes,
// and costs a handful of bytes.
//
// Both price fields carry the *frozen* unit_price recorded at checkout, never
// the item's current catalog price -- a receipt has to show what was paid.
type ShopOrderLine struct {
	ItemID          string  `json:"itemId"`
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Description     string  `json:"description,omitempty"`
	ImageURL        string  `json:"imageUrl,omitempty"`
	Category        string  `json:"category,omitempty"`
	Quantity        int     `json:"quantity"`
	Price           float64 `json:"price"`
	PriceAtPurchase float64 `json:"priceAtPurchase"`
}

// ShopOrder is one entry of the GET /shops/orders/me response.
//
// Field names follow what the client actually reads -- orderId/total/date/txHash
// rather than the column names id/total_coins_amount/created_on/transaction_hash.
type ShopOrder struct {
	OrderID string          `json:"orderId"`
	EventID string          `json:"eventId,omitempty"`
	Status  string          `json:"status"`
	Items   []ShopOrderLine `json:"items"`
	Total   float64         `json:"total"`
	Date    time.Time       `json:"date"`
	TxHash  string          `json:"txHash,omitempty"`

	ShippingRecipientName string `json:"shippingRecipientName,omitempty"`
	ShippingEmail         string `json:"shippingEmail,omitempty"`
	ShippingAddressLine1  string `json:"shippingAddressLine1,omitempty"`
	ShippingAddressLine2  string `json:"shippingAddressLine2,omitempty"`
	ShippingCity          string `json:"shippingCity,omitempty"`
	ShippingState         string `json:"shippingState,omitempty"`
	ShippingPostalCode    string `json:"shippingPostalCode,omitempty"`
	ShippingCountry       string `json:"shippingCountry,omitempty"`
}

// CheckoutItem is one requested line in a checkout request.
type CheckoutItem struct {
	ID       string `json:"id"`
	Quantity int    `json:"quantity"`
}

// CheckoutRequest is the POST /shops/checkout body.
//
// TotalCost is accepted and then ignored: the total is always recomputed from
// live shop_item.price rows. Trusting a client-supplied total would let a caller
// name its own price. It stays in the struct because the client sends it and
// rejecting unknown fields would break it for no gain.
type CheckoutRequest struct {
	IdempotencyKey string         `json:"idempotencyKey"`
	EventID        string         `json:"eventId"`
	TotalCost      float64        `json:"totalCost"`
	Items          []CheckoutItem `json:"items"`

	ShippingRecipientName string `json:"shippingRecipientName"`
	ShippingEmail         string `json:"shippingEmail"`
	ShippingAddressLine1  string `json:"shippingAddressLine1"`
	ShippingAddressLine2  string `json:"shippingAddressLine2"`
	ShippingCity          string `json:"shippingCity"`
	ShippingState         string `json:"shippingState"`
	ShippingPostalCode    string `json:"shippingPostalCode"`
	ShippingCountry       string `json:"shippingCountry"`
}

// maxShippingFieldLen bounds every free-text shipping field. The columns are
// unbounded TEXT, so without a limit here a caller could store megabytes per
// order.
const maxShippingFieldLen = 255

// maxCheckoutLines bounds how many distinct items one order may contain. Each
// line costs an INSERT plus a conditional stock UPDATE inside a single
// transaction, so an unbounded list is a cheap way to hold locks for a long
// time. The catalog is a few dozen items; 50 is well clear of any real cart.
const maxCheckoutLines = 50

// maxLineQuantity bounds a single line's quantity, so a typo of 100000000
// becomes a 400 instead of an arithmetic overflow in the total.
const maxLineQuantity = 1000

// Validate checks a checkout request for structural problems, returning a
// client-facing error message. It deliberately does not look at prices, stock,
// or per-user caps: those need the database and belong to the service layer.
//
// Trimming happens in place so the stored shipping address doesn't carry the
// client's stray whitespace, and so a field of nothing but spaces fails the
// required check rather than being stored as blank.
func (r *CheckoutRequest) Validate() error {
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	r.EventID = strings.TrimSpace(r.EventID)
	r.ShippingRecipientName = strings.TrimSpace(r.ShippingRecipientName)
	r.ShippingEmail = strings.TrimSpace(r.ShippingEmail)
	r.ShippingAddressLine1 = strings.TrimSpace(r.ShippingAddressLine1)
	r.ShippingAddressLine2 = strings.TrimSpace(r.ShippingAddressLine2)
	r.ShippingCity = strings.TrimSpace(r.ShippingCity)
	r.ShippingState = strings.TrimSpace(r.ShippingState)
	r.ShippingPostalCode = strings.TrimSpace(r.ShippingPostalCode)
	r.ShippingCountry = strings.TrimSpace(r.ShippingCountry)

	if len(r.Items) == 0 {
		return errors.New("at least one item is required")
	}
	if len(r.Items) > maxCheckoutLines {
		return errors.New("too many items in one order")
	}

	// A repeated item id would make the per-line stock decrement race against
	// itself and violate shop_order_item's (order_id, item_id) primary key
	// mid-transaction. Rejecting it up front gives a clear 400 instead.
	seen := make(map[string]struct{}, len(r.Items))
	for i := range r.Items {
		r.Items[i].ID = strings.TrimSpace(r.Items[i].ID)
		if r.Items[i].ID == "" {
			return errors.New("every item requires an id")
		}
		if _, dup := seen[r.Items[i].ID]; dup {
			return errors.New("duplicate item in cart: " + r.Items[i].ID)
		}
		seen[r.Items[i].ID] = struct{}{}
		if r.Items[i].Quantity < 1 {
			return errors.New("item quantity must be at least 1")
		}
		if r.Items[i].Quantity > maxLineQuantity {
			return errors.New("item quantity is too large")
		}
	}

	required := []struct {
		name  string
		value string
	}{
		{"shippingRecipientName", r.ShippingRecipientName},
		{"shippingEmail", r.ShippingEmail},
		{"shippingAddressLine1", r.ShippingAddressLine1},
		{"shippingCity", r.ShippingCity},
		{"shippingCountry", r.ShippingCountry},
	}
	for _, f := range required {
		if f.value == "" {
			return errors.New(f.name + " is required")
		}
	}

	all := []struct {
		name  string
		value string
	}{
		{"shippingRecipientName", r.ShippingRecipientName},
		{"shippingEmail", r.ShippingEmail},
		{"shippingAddressLine1", r.ShippingAddressLine1},
		{"shippingAddressLine2", r.ShippingAddressLine2},
		{"shippingCity", r.ShippingCity},
		{"shippingState", r.ShippingState},
		{"shippingPostalCode", r.ShippingPostalCode},
		{"shippingCountry", r.ShippingCountry},
		{"idempotencyKey", r.IdempotencyKey},
	}
	for _, f := range all {
		if len(f.value) > maxShippingFieldLen {
			return errors.New(f.name + " is too long")
		}
	}

	return nil
}

// CheckoutResponse is the POST /shops/checkout and POST /shops/checkout/confirm
// response. The client reads only OrderID from both; TransactionHash is null on
// checkout and populated on confirm.
type CheckoutResponse struct {
	OrderID         string  `json:"orderId"`
	TransactionHash *string `json:"transactionHash"`
}

// CheckoutConfirmRequest is the POST /shops/checkout/confirm body.
type CheckoutConfirmRequest struct {
	OrderID         string `json:"orderId"`
	TransactionHash string `json:"transactionHash"`
}

// maxTxHashLen bounds the transaction hash. A real hash is 66 characters
// ("0x" + 64 hex); this leaves room for other encodings without letting an
// arbitrary blob reach the UNIQUE index on shop_order.transaction_hash.
const maxTxHashLen = 128

// isHashSafe reports whether s is alphanumeric, which every hash encoding this
// might carry (hex, with or without an 0x prefix, or base58) already is.
//
// A length bound alone is not enough. This value is interpolated into the path
// of an outbound request to the transaction service, and url.JoinPath applies
// path cleaning, so a ".." segment does not 404 -- it climbs out of the
// configured base path. Since that request carries this backend's own OAuth2
// client-credentials token, an unconstrained hash would let any authenticated
// attendee aim a privileged GET at an arbitrary path on an internal service.
// Rejecting the separators here removes the traversal rather than escaping it,
// and keeps the check at the boundary where the value first arrives.
func isHashSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		default:
			return false
		}
	}
	return true
}

// Validate checks a confirm request, trimming both fields in place.
func (r *CheckoutConfirmRequest) Validate() error {
	r.OrderID = strings.TrimSpace(r.OrderID)
	r.TransactionHash = strings.TrimSpace(r.TransactionHash)

	if r.OrderID == "" {
		return errors.New("orderId is required")
	}
	if len(r.OrderID) > maxShippingFieldLen {
		return errors.New("orderId is too long")
	}
	if r.TransactionHash == "" {
		return errors.New("transactionHash is required")
	}
	if len(r.TransactionHash) > maxTxHashLen {
		return errors.New("transactionHash is too long")
	}
	if !isHashSafe(r.TransactionHash) {
		return errors.New("transactionHash must be alphanumeric")
	}
	return nil
}
