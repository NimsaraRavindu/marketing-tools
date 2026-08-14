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
	"encoding/json"
	"strings"
	"testing"
)

func validCheckout() CheckoutRequest {
	return CheckoutRequest{
		IdempotencyKey:        "key-1",
		Items:                 []CheckoutItem{{ID: "item-1", Quantity: 2}},
		ShippingRecipientName: "Jane Doe",
		ShippingEmail:         "jane@example.com",
		ShippingAddressLine1:  "1 Main St",
		ShippingCity:          "Colombo",
		ShippingCountry:       "LK",
	}
}

func TestCheckoutRequest_ValidAccepted(t *testing.T) {
	req := validCheckout()
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate rejected a valid request: %v", err)
	}
}

func TestCheckoutRequest_RejectsStructuralProblems(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CheckoutRequest)
	}{
		{"no items", func(r *CheckoutRequest) { r.Items = nil }},
		{"empty item id", func(r *CheckoutRequest) { r.Items[0].ID = "  " }},
		{"zero quantity", func(r *CheckoutRequest) { r.Items[0].Quantity = 0 }},
		{"negative quantity", func(r *CheckoutRequest) { r.Items[0].Quantity = -3 }},
		{"absurd quantity", func(r *CheckoutRequest) { r.Items[0].Quantity = 1_000_000 }},
		{"missing recipient", func(r *CheckoutRequest) { r.ShippingRecipientName = "" }},
		{"whitespace-only recipient", func(r *CheckoutRequest) { r.ShippingRecipientName = "   " }},
		{"missing email", func(r *CheckoutRequest) { r.ShippingEmail = "" }},
		{"missing address line 1", func(r *CheckoutRequest) { r.ShippingAddressLine1 = "" }},
		{"missing city", func(r *CheckoutRequest) { r.ShippingCity = "" }},
		{"missing country", func(r *CheckoutRequest) { r.ShippingCountry = "" }},
		{"oversized city", func(r *CheckoutRequest) { r.ShippingCity = strings.Repeat("x", 256) }},
		{"oversized idempotency key", func(r *CheckoutRequest) { r.IdempotencyKey = strings.Repeat("k", 256) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validCheckout()
			tc.mutate(&req)
			if err := req.Validate(); err == nil {
				t.Error("Validate accepted an invalid request")
			}
		})
	}
}

// Optional fields are genuinely optional: a cart with no apartment number or
// postal code is normal and must not be rejected.
func TestCheckoutRequest_OptionalShippingFieldsMayBeEmpty(t *testing.T) {
	req := validCheckout()
	req.ShippingAddressLine2 = ""
	req.ShippingState = ""
	req.ShippingPostalCode = ""

	if err := req.Validate(); err != nil {
		t.Fatalf("Validate rejected a request with only optional fields empty: %v", err)
	}
}

// A repeated item id would race the per-line stock decrement against itself and
// violate shop_order_item's (order_id, item_id) primary key mid-transaction.
func TestCheckoutRequest_RejectsDuplicateItems(t *testing.T) {
	req := validCheckout()
	req.Items = []CheckoutItem{{ID: "item-1", Quantity: 1}, {ID: "item-1", Quantity: 2}}

	err := req.Validate()
	if err == nil {
		t.Fatal("Validate accepted a cart with the same item twice")
	}
	if !strings.Contains(err.Error(), "item-1") {
		t.Errorf("error %q does not name the duplicated item", err)
	}
}

func TestCheckoutRequest_RejectsTooManyLines(t *testing.T) {
	req := validCheckout()
	req.Items = make([]CheckoutItem, 51)
	for i := range req.Items {
		req.Items[i] = CheckoutItem{ID: string(rune('a'+i%26)) + strings.Repeat("x", i), Quantity: 1}
	}

	if err := req.Validate(); err == nil {
		t.Error("Validate accepted more lines than the cap allows")
	}
}

// Trimming happens in place so a stored address doesn't carry the client's stray
// whitespace.
func TestCheckoutRequest_TrimsInPlace(t *testing.T) {
	req := validCheckout()
	req.ShippingRecipientName = "  Jane Doe  "
	req.ShippingCity = "\tColombo\n"
	req.Items[0].ID = "  item-1  "

	if err := req.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if req.ShippingRecipientName != "Jane Doe" {
		t.Errorf("recipient = %q, want trimmed", req.ShippingRecipientName)
	}
	if req.ShippingCity != "Colombo" {
		t.Errorf("city = %q, want trimmed", req.ShippingCity)
	}
	if req.Items[0].ID != "item-1" {
		t.Errorf("item id = %q, want trimmed", req.Items[0].ID)
	}
}

func TestCheckoutConfirmRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		req     CheckoutConfirmRequest
		wantErr bool
	}{
		{"valid", CheckoutConfirmRequest{OrderID: "ORD-1", TransactionHash: "0xabc"}, false},
		{"missing order id", CheckoutConfirmRequest{TransactionHash: "0xabc"}, true},
		{"whitespace order id", CheckoutConfirmRequest{OrderID: "   ", TransactionHash: "0xabc"}, true},
		{"missing hash", CheckoutConfirmRequest{OrderID: "ORD-1"}, true},
		{"whitespace hash", CheckoutConfirmRequest{OrderID: "ORD-1", TransactionHash: " "}, true},
		{
			"oversized hash",
			CheckoutConfirmRequest{OrderID: "ORD-1", TransactionHash: strings.Repeat("a", 129)},
			true,
		},
		// The hash becomes a path segment on an outbound request that carries
		// this backend's own credentials, and url.JoinPath resolves "..", so a
		// length check alone would let a caller redirect that request.
		{
			"hash traversing out of the base path",
			CheckoutConfirmRequest{OrderID: "ORD-1", TransactionHash: "../../../../admin/internal"},
			true,
		},
		{"dot segment hash", CheckoutConfirmRequest{OrderID: "ORD-1", TransactionHash: ".."}, true},
		{"hash with a slash", CheckoutConfirmRequest{OrderID: "ORD-1", TransactionHash: "abc/def"}, true},
		{"percent encoded hash", CheckoutConfirmRequest{OrderID: "ORD-1", TransactionHash: "..%2Fadmin"}, true},
		{
			"realistic full length hash still accepted",
			CheckoutConfirmRequest{OrderID: "ORD-1", TransactionHash: "0x" + strings.Repeat("aF9", 21) + "b"},
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			err := req.Validate()
			if tc.wantErr && err == nil {
				t.Error("Validate accepted an invalid request")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Validate rejected a valid request: %v", err)
			}
		})
	}
}

func TestScaleCoins_ComparesAtSchemaPrecision(t *testing.T) {
	// The classic float artifact must compare equal to its exact decimal.
	if ScaleCoins(0.1+0.2) != ScaleCoins(0.3) {
		t.Error("0.1+0.2 does not scale equal to 0.3")
	}
	if ScaleCoins(100) != 1_000_000 {
		t.Errorf("ScaleCoins(100) = %d, want 1000000", ScaleCoins(100))
	}
	// A difference the schema can represent must stay a difference.
	if ScaleCoins(1.0001) == ScaleCoins(1.0002) {
		t.Error("two amounts that differ at the 4th decimal scaled equal")
	}
	// Rounds rather than truncates, so a value just under a tick is not lost.
	if ScaleCoins(0.00009999) != 1 {
		t.Errorf("ScaleCoins(0.00009999) = %d, want 1 (rounded)", ScaleCoins(0.00009999))
	}
}

// The client reads `itemId || id` and `priceAtPurchase || price`, so both
// spellings must be present whichever branch it takes.
func TestShopOrderLine_EmitsBothIDAndPriceSpellings(t *testing.T) {
	b, err := json.Marshal(ShopOrderLine{
		ItemID: "item-1", ID: "item-1", Quantity: 2, Price: 10, PriceAtPurchase: 10,
	})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	for _, key := range []string{`"itemId"`, `"id"`, `"price"`, `"priceAtPurchase"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("serialized line is missing %s: %s", key, b)
		}
	}
}

// A no-limit item must omit maxPerUser entirely: the client keys its per-item cap
// on the field being present, so a 0 would read as "you may buy none".
func TestShopItem_NoLimitOmitsMaxPerUser(t *testing.T) {
	b, err := json.Marshal(ShopItem{ID: "i1", MaxPerUser: nil})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(b), "maxPerUser") {
		t.Errorf("maxPerUser present for an unlimited item: %s", b)
	}

	limit := 3
	b, _ = json.Marshal(ShopItem{ID: "i1", MaxPerUser: &limit})
	if !strings.Contains(string(b), `"maxPerUser":3`) {
		t.Errorf("maxPerUser missing for a limited item: %s", b)
	}
}

// The catalog must be an envelope, and its items must serialize as [] rather than
// null so the client can map over the result unguarded.
func TestShopCatalog_EmptyItemsSerializeAsArray(t *testing.T) {
	b, err := json.Marshal(ShopCatalog{Items: []ShopItem{}, IsShopOpen: true, ActiveEventID: "e1"})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(b), `"items":[]`) {
		t.Errorf("empty items did not serialize as []: %s", b)
	}
	if !strings.Contains(string(b), `"isShopOpen":true`) || !strings.Contains(string(b), `"activeEventId":"e1"`) {
		t.Errorf("catalog envelope is incomplete: %s", b)
	}
}

// transactionHash must be an explicit null on checkout, not an omitted key: the
// client reads it off the body directly.
func TestCheckoutResponse_NullHashIsExplicit(t *testing.T) {
	b, err := json.Marshal(CheckoutResponse{OrderID: "ORD-1"})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(b), `"transactionHash":null`) {
		t.Errorf("transactionHash is not an explicit null: %s", b)
	}
}
