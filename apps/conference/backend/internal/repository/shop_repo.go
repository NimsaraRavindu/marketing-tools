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

package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wso2-coin-backend/internal/models"
)

type ShopRepo struct {
	pool *pgxpool.Pool
}

func NewShopRepo(pool *pgxpool.Pool) *ShopRepo {
	return &ShopRepo{pool: pool}
}

func (r *ShopRepo) GetActiveEventID(ctx context.Context) (*string, error) {
	query := `
		WITH ActiveConfigName AS (
			SELECT name FROM conference_config 
			ORDER BY 
			  CASE WHEN start_date <= (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::DATE THEN 1 ELSE 0 END DESC,
			  start_date DESC,
			  created_at DESC
			LIMIT 1
		)
		SELECT id::text FROM conference_config 
		WHERE name = (SELECT name FROM ActiveConfigName) 
		ORDER BY start_date DESC 
		LIMIT 1
	`
	var eventID string
	err := r.pool.QueryRow(ctx, query).Scan(&eventID)
	if err != nil {
		// It's possible there is no active config.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get active event id: %w", err)
	}
	return &eventID, nil
}

func (r *ShopRepo) GetUserAvailableBalance(ctx context.Context, userUUID string) (float64, error) {
	var totalAllocated, totalSpent float64
	
	err := r.pool.QueryRow(ctx, "SELECT COALESCE(SUM(coins_allocated), 0) FROM coin_allocation WHERE user_uuid = $1", userUUID).Scan(&totalAllocated)
	if err != nil {
		return 0, fmt.Errorf("failed to get total allocated coins: %w", err)
	}
	
	err = r.pool.QueryRow(ctx, "SELECT COALESCE(SUM(total_coins_amount), 0) FROM shop_order WHERE user_uuid = $1 AND status IN ('CONFIRMED', 'FULFILLED')", userUUID).Scan(&totalSpent)
	if err != nil {
		return 0, fmt.Errorf("failed to get total spent coins: %w", err)
	}
	
	return totalAllocated - totalSpent, nil
}

func (r *ShopRepo) GetShopClosingTime(ctx context.Context, activeEventID *string) (*time.Time, error) {
	if activeEventID == nil {
		return nil, nil
	}
	var closingTime *time.Time
	err := r.pool.QueryRow(ctx, "SELECT shop_closing_time FROM conference_config WHERE id = $1::uuid", *activeEventID).Scan(&closingTime)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get shop closing time: %w", err)
	}
	return closingTime, nil
}

func (r *ShopRepo) GetVisibleItems(ctx context.Context, activeEventID *string) ([]models.ShopItem, error) {
	query := `
		SELECT
			id, name, description, price, image_url, available_stock,
			category, max_per_user, visibility, event_id
		FROM shop_item
		WHERE visibility = 'VISIBLE'
	`
	args := []interface{}{}
	if activeEventID != nil {
		query += ` AND (event_id = $1 OR event_id IS NULL)`
		args = append(args, *activeEventID)
	}
	query += ` ORDER BY price DESC`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query shop items: %w", err)
	}
	defer rows.Close()

	var items []models.ShopItem
	for rows.Next() {
		var i models.ShopItem
		if err := rows.Scan(
			&i.ID, &i.Name, &i.Description, &i.Price, &i.ImageURL, &i.AvailableStock,
			&i.Category, &i.MaxPerUser, &i.Visibility, &i.EventID,
		); err != nil {
			return nil, fmt.Errorf("scan shop item: %w", err)
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *ShopRepo) GetPendingOrderByIdempotencyKey(ctx context.Context, userUUID, idempotencyKey string) (*models.ShopOrder, error) {
	var order models.ShopOrder
	query := `
		SELECT id, user_uuid, status, transaction_hash, total_coins_amount, 
		       created_on, created_by, updated_on, updated_by, idempotency_key, 
		       shipping_recipient_name, shipping_email, shipping_address_line1, 
		       shipping_address_line2, shipping_city, shipping_state, 
		       shipping_postal_code, shipping_country, event_id
		FROM shop_order 
		WHERE user_uuid = $1::uuid AND idempotency_key = $2 AND status = 'PENDING'
	`
	err := r.pool.QueryRow(ctx, query, userUUID, idempotencyKey).Scan(
		&order.ID, &order.UserUUID, &order.Status, &order.TransactionHash, &order.TotalCoinsAmount,
		&order.CreatedOn, &order.CreatedBy, &order.UpdatedOn, &order.UpdatedBy, &order.IdempotencyKey,
		&order.ShippingRecipientName, &order.ShippingEmail, &order.ShippingAddressLine1,
		&order.ShippingAddressLine2, &order.ShippingCity, &order.ShippingState,
		&order.ShippingPostalCode, &order.ShippingCountry, &order.EventID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get pending order by idempotency key: %w", err)
	}
	return &order, nil
}

func (r *ShopRepo) UpdateShopOrderShippingDetails(ctx context.Context, orderID string, req models.ShopOrder) error {
	query := `
		UPDATE shop_order 
		SET shipping_recipient_name = $1, shipping_email = $2, shipping_address_line1 = $3, 
		    shipping_address_line2 = $4, shipping_city = $5, shipping_state = $6, 
		    shipping_postal_code = $7, shipping_country = $8
		WHERE id = $9
	`
	_, err := r.pool.Exec(ctx, query,
		req.ShippingRecipientName, req.ShippingEmail, req.ShippingAddressLine1,
		req.ShippingAddressLine2, req.ShippingCity, req.ShippingState,
		req.ShippingPostalCode, req.ShippingCountry, orderID,
	)
	if err != nil {
		return fmt.Errorf("update shipping details: %w", err)
	}
	return nil
}

func (r *ShopRepo) GetPastPurchasedQuantities(ctx context.Context, userUUID string, activeEventID *string) (map[string]int, error) {
	query := `
		SELECT oi.item_id, SUM(oi.quantity)
		FROM shop_order o
		JOIN shop_order_item oi ON o.id = oi.order_id
		WHERE o.user_uuid = $1::uuid AND o.status NOT IN ('EXPIRED', 'FAILED')
	`
	args := []interface{}{userUUID}
	if activeEventID != nil {
		query += ` AND o.event_id = $2`
		args = append(args, *activeEventID)
	}
	query += ` GROUP BY oi.item_id`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get past purchased quantities: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var itemID string
		var quantity int
		if err := rows.Scan(&itemID, &quantity); err != nil {
			return nil, fmt.Errorf("scan past quantity: %w", err)
		}
		result[itemID] = quantity
	}
	return result, rows.Err()
}

func (r *ShopRepo) GetUserPendingOrdersCount(ctx context.Context, userUUID string, activeEventID *string) (int, error) {
	var count int
	query := `
		SELECT COUNT(*) 
		FROM shop_order 
		WHERE user_uuid = $1::uuid AND status IN ('PENDING', 'PROCESSING')
	`
	args := []interface{}{userUUID}
	if activeEventID != nil {
		query += ` AND (event_id = $2 OR event_id IS NULL)`
		args = append(args, *activeEventID)
	}

	err := r.pool.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pending orders: %w", err)
	}
	return count, nil
}

func (r *ShopRepo) CreateOrder(ctx context.Context, order models.ShopOrder) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Insert order
	orderQuery := `
		INSERT INTO shop_order (
			id, user_uuid, status, transaction_hash, total_coins_amount, 
			created_on, created_by, updated_on, updated_by, idempotency_key, 
			shipping_recipient_name, shipping_email, shipping_address_line1, 
			shipping_address_line2, shipping_city, shipping_state, 
			shipping_postal_code, shipping_country, event_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 
			$11, $12, $13, $14, $15, $16, $17, $18, $19
		)
	`
	_, err = tx.Exec(ctx, orderQuery,
		order.ID, order.UserUUID, order.Status, order.TransactionHash, order.TotalCoinsAmount,
		order.CreatedOn, order.CreatedBy, order.UpdatedOn, order.UpdatedBy, order.IdempotencyKey,
		order.ShippingRecipientName, order.ShippingEmail, order.ShippingAddressLine1,
		order.ShippingAddressLine2, order.ShippingCity, order.ShippingState,
		order.ShippingPostalCode, order.ShippingCountry, order.EventID,
	)
	if err != nil {
		return fmt.Errorf("insert shop_order: %w", err)
	}

	// Insert order items
	if len(order.Items) > 0 {
		var values []interface{}
		var placeholders []string
		for i, item := range order.Items {
			offset := i * 4
			placeholders = append(placeholders, fmt.Sprintf("($%d, $%d, $%d, $%d)", offset+1, offset+2, offset+3, offset+4))
			values = append(values, order.ID, item.ItemID, item.Quantity, item.UnitPrice)
		}

		itemQuery := fmt.Sprintf(`
			INSERT INTO shop_order_item (order_id, item_id, quantity, unit_price)
			VALUES %s
		`, strings.Join(placeholders, ","))

		_, err = tx.Exec(ctx, itemQuery, values...)
		if err != nil {
			return fmt.Errorf("insert shop_order_items: %w", err)
		}

		// Decrement stock
		for _, item := range order.Items {
			_, err = tx.Exec(ctx, `UPDATE shop_item SET available_stock = available_stock - $1 WHERE id = $2`, item.Quantity, item.ItemID)
			if err != nil {
				return fmt.Errorf("decrement stock for item %s: %w", item.ItemID, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (r *ShopRepo) UpdateOrderStatus(ctx context.Context, orderID, status, updatedBy string) error {
	query := `
		UPDATE shop_order 
		SET status = $1, updated_on = NOW(), updated_by = $2 
		WHERE id = $3
	`
	_, err := r.pool.Exec(ctx, query, status, updatedBy, orderID)
	if err != nil {
		return fmt.Errorf("update shop_order status: %w", err)
	}
	return nil
}

func (r *ShopRepo) GetEventName(ctx context.Context, eventID *string) (string, error) {
	var name string
	var err error
	if eventID != nil {
		err = r.pool.QueryRow(ctx, `SELECT name FROM conference_config WHERE id = $1`, *eventID).Scan(&name)
	} else {
		err = r.pool.QueryRow(ctx, `SELECT name FROM conference_config WHERE is_active = true LIMIT 1`).Scan(&name)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return name, nil
}

func (r *ShopRepo) ConfirmOrder(ctx context.Context, orderID, updatedBy string, txHash *string) error {
	query := `
		UPDATE shop_order 
		SET status = 'CONFIRMED', transaction_hash = $1, updated_on = NOW(), updated_by = $2 
		WHERE id = $3
	`
	_, err := r.pool.Exec(ctx, query, txHash, updatedBy, orderID)
	if err != nil {
		return fmt.Errorf("confirm shop_order: %w", err)
	}
	return nil
}

func (r *ShopRepo) MarkStaleOrders(ctx context.Context, timeoutMinutes int) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Fetch orders to cancel to restore their stock
	selectQuery := `
		SELECT id FROM shop_order
		WHERE status = 'PENDING' 
		  AND created_on < NOW() - INTERVAL '1 minute' * $1
	`
	rows, err := tx.Query(ctx, selectQuery, timeoutMinutes)
	if err != nil {
		return 0, fmt.Errorf("select stale orders: %w", err)
	}
	var staleOrderIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan stale order id: %w", err)
		}
		staleOrderIDs = append(staleOrderIDs, id)
	}
	rows.Close()

	if len(staleOrderIDs) == 0 {
		return 0, nil
	}

	// Restore stock for these orders
	for _, orderID := range staleOrderIDs {
		// Cancel the order
		_, err = tx.Exec(ctx, `UPDATE shop_order SET status = 'EXPIRED', updated_on = NOW(), updated_by = 'SYSTEM' WHERE id = $1`, orderID)
		if err != nil {
			return 0, fmt.Errorf("update stale order %s: %w", orderID, err)
		}

		// Restore stock
		_, err = tx.Exec(ctx, `
			UPDATE shop_item i
			SET available_stock = i.available_stock + oi.quantity
			FROM shop_order_item oi
			WHERE i.id = oi.item_id AND oi.order_id = $1
		`, orderID)
		if err != nil {
			return 0, fmt.Errorf("restore stock for stale order %s: %w", orderID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit stale orders tx: %w", err)
	}
	return len(staleOrderIDs), nil
}

func (r *ShopRepo) CancelOrderAndRestoreStock(ctx context.Context, orderID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Cancel the order
	_, err = tx.Exec(ctx, `UPDATE shop_order SET status = 'FAILED', updated_on = NOW(), updated_by = 'SYSTEM' WHERE id = $1 AND status = 'PENDING'`, orderID)
	if err != nil {
		return fmt.Errorf("update failed order %s: %w", orderID, err)
	}

	// Restore stock
	_, err = tx.Exec(ctx, `
		UPDATE shop_item i
		SET available_stock = i.available_stock + oi.quantity
		FROM shop_order_item oi
		WHERE i.id = oi.item_id AND oi.order_id = $1
	`, orderID)
	if err != nil {
		return fmt.Errorf("restore stock for failed order %s: %w", orderID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cancel order tx: %w", err)
	}
	return nil
}

func (r *ShopRepo) GetOrderByTransactionHash(ctx context.Context, txHash string) (*models.ShopOrder, error) {
	query := `
		SELECT id, status, user_uuid
		FROM shop_order 
		WHERE transaction_hash = $1
	`
	
	var o models.ShopOrder
	err := r.pool.QueryRow(ctx, query, txHash).Scan(&o.ID, &o.Status, &o.UserUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get order by tx hash: %w", err)
	}
	return &o, nil
}

func (r *ShopRepo) GetOrderById(ctx context.Context, orderID string) (*models.ShopOrder, error) {
	query := `
		SELECT id, user_uuid, status, total_coins_amount, event_id
		FROM shop_order 
		WHERE id = $1
	`
	var o models.ShopOrder
	err := r.pool.QueryRow(ctx, query, orderID).Scan(&o.ID, &o.UserUUID, &o.Status, &o.TotalCoinsAmount, &o.EventID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get order by id: %w", err)
	}
	return &o, nil
}

func (r *ShopRepo) GetOrderWithItemsById(ctx context.Context, orderID string) (*models.ShopOrder, error) {
	// First fetch the order
	query := `
		SELECT 
			id, user_uuid, status, transaction_hash, total_coins_amount,
			created_on, created_by, updated_on, updated_by, idempotency_key,
			shipping_recipient_name, shipping_email, shipping_address_line1,
			shipping_address_line2, shipping_city, shipping_state,
			shipping_postal_code, shipping_country, event_id
		FROM shop_order 
		WHERE id = $1
	`
	var o models.ShopOrder
	err := r.pool.QueryRow(ctx, query, orderID).Scan(
		&o.ID, &o.UserUUID, &o.Status, &o.TransactionHash, &o.TotalCoinsAmount,
		&o.CreatedOn, &o.CreatedBy, &o.UpdatedOn, &o.UpdatedBy, &o.IdempotencyKey,
		&o.ShippingRecipientName, &o.ShippingEmail, &o.ShippingAddressLine1,
		&o.ShippingAddressLine2, &o.ShippingCity, &o.ShippingState,
		&o.ShippingPostalCode, &o.ShippingCountry, &o.EventID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get order with items by id: %w", err)
	}

	// Then fetch the items joined with shop_item to get names
	itemsQuery := `
		SELECT oi.order_id, oi.item_id, oi.quantity, oi.unit_price, i.name as item_name
		FROM shop_order_item oi
		JOIN shop_item i ON oi.item_id = i.id
		WHERE oi.order_id = $1
	`
	rows, err := r.pool.Query(ctx, itemsQuery, orderID)
	if err != nil {
		return nil, fmt.Errorf("query order items: %w", err)
	}
	defer rows.Close()

	var items []models.ShopOrderItem
	for rows.Next() {
		var item models.ShopOrderItem
		if err := rows.Scan(&item.OrderID, &item.ItemID, &item.Quantity, &item.UnitPrice, &item.ItemName); err != nil {
			return nil, fmt.Errorf("scan order item: %w", err)
		}
		items = append(items, item)
	}

	o.Items = items
	return &o, nil
}

func (r *ShopRepo) GetUserOrders(ctx context.Context, userUUID string, activeEventID *string) ([]models.ShopOrder, error) {
	query := `
		SELECT 
			o.id, o.user_uuid, o.status, o.transaction_hash, o.total_coins_amount,
			o.created_on, o.created_by, o.updated_on, o.updated_by, o.idempotency_key,
			o.shipping_recipient_name, o.shipping_email, o.shipping_address_line1,
			o.shipping_address_line2, o.shipping_city, o.shipping_state,
			o.shipping_postal_code, o.shipping_country, o.event_id
		FROM shop_order o
		WHERE o.user_uuid = $1::uuid AND o.status IN ('CONFIRMED', 'FULFILLED')
	`
	args := []interface{}{userUUID}

	if activeEventID != nil {
		// Mimicking Ballerina logic for getting orders:
		query += ` AND (
			(o.event_id IS NOT NULL AND o.event_id = $2)
			OR
			(o.event_id IS NULL AND o.created_on >= (
				SELECT MIN(date)::timestamp FROM conference_days
				WHERE config_id = $2::uuid
			))
		)`
		args = append(args, *activeEventID)
	}

	query += ` ORDER BY o.created_on DESC`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query user orders: %w", err)
	}
	defer rows.Close()

	var orders []models.ShopOrder
	for rows.Next() {
		var o models.ShopOrder
		if err := rows.Scan(
			&o.ID, &o.UserUUID, &o.Status, &o.TransactionHash, &o.TotalCoinsAmount,
			&o.CreatedOn, &o.CreatedBy, &o.UpdatedOn, &o.UpdatedBy, &o.IdempotencyKey,
			&o.ShippingRecipientName, &o.ShippingEmail, &o.ShippingAddressLine1,
			&o.ShippingAddressLine2, &o.ShippingCity, &o.ShippingState,
			&o.ShippingPostalCode, &o.ShippingCountry, &o.EventID,
		); err != nil {
			return nil, fmt.Errorf("scan user order: %w", err)
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(orders) == 0 {
		return orders, nil
	}

	// Fetch items for these orders
	var orderIDs []interface{}
	var placeholders []string
	for i, o := range orders {
		orderIDs = append(orderIDs, o.ID)
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
	}
	
	itemsQuery := fmt.Sprintf(`
		SELECT i.order_id, i.item_id, i.quantity, i.unit_price,
			   s.id, s.name, s.description, s.image_url
		FROM shop_order_item i
		JOIN shop_item s ON i.item_id = s.id
		WHERE i.order_id IN (%s)
	`, strings.Join(placeholders, ","))

	itemRows, err := r.pool.Query(ctx, itemsQuery, orderIDs...)
	if err != nil {
		return nil, fmt.Errorf("query order items: %w", err)
	}
	defer itemRows.Close()

	itemsMap := make(map[string][]models.ShopOrderItem)
	for itemRows.Next() {
		var i models.ShopOrderItem
		var s models.ShopItem
		if err := itemRows.Scan(
			&i.OrderID, &i.ItemID, &i.Quantity, &i.UnitPrice,
			&s.ID, &s.Name, &s.Description, &s.ImageURL,
		); err != nil {
			return nil, fmt.Errorf("scan order item: %w", err)
		}
		i.Item = &s
		itemsMap[i.OrderID] = append(itemsMap[i.OrderID], i)
	}

	for idx := range orders {
		orders[idx].Items = itemsMap[orders[idx].ID]
	}

	return orders, nil
}

func (r *ShopRepo) GetAllOrders(ctx context.Context) ([]models.ShopOrder, error) {
	query := `
		SELECT 
			o.id, o.user_uuid, o.status, o.transaction_hash, o.total_coins_amount,
			o.created_on, o.created_by, o.updated_on, o.updated_by, o.idempotency_key,
			o.shipping_recipient_name, o.shipping_email, o.shipping_address_line1,
			o.shipping_address_line2, o.shipping_city, o.shipping_state,
			o.shipping_postal_code, o.shipping_country, o.event_id
		FROM shop_order o
		ORDER BY o.created_on DESC
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query all orders: %w", err)
	}
	defer rows.Close()

	var orders []models.ShopOrder
	for rows.Next() {
		var o models.ShopOrder
		if err := rows.Scan(
			&o.ID, &o.UserUUID, &o.Status, &o.TransactionHash, &o.TotalCoinsAmount,
			&o.CreatedOn, &o.CreatedBy, &o.UpdatedOn, &o.UpdatedBy, &o.IdempotencyKey,
			&o.ShippingRecipientName, &o.ShippingEmail, &o.ShippingAddressLine1,
			&o.ShippingAddressLine2, &o.ShippingCity, &o.ShippingState,
			&o.ShippingPostalCode, &o.ShippingCountry, &o.EventID,
		); err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orders, nil
}
