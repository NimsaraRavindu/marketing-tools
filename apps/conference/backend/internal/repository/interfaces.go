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
	"time"

	"wso2-coin-backend/internal/models"
)

// AttendeeRepository is satisfied by *AttendeeRepo. It lets the service layer
// depend on an interface it can mock in unit tests without hitting a real DB.
type AttendeeRepository interface {
	IsRegistered(ctx context.Context, email string) (bool, error)
}

// CoinAllocationRepository is satisfied by *CoinAllocationRepo.
type CoinAllocationRepository interface {
	Exists(ctx context.Context, qrID, userUUID string) (bool, error)
	Insert(ctx context.Context, alloc models.CoinAllocation) (models.CoinAllocation, error)
	UpdateStatus(ctx context.Context, qrID, userUUID string, status models.TransactionStatus) error
	History(ctx context.Context, userUUID string) ([]models.CoinAllocationHistory, error)
	Summary(ctx context.Context, userUUID string) (models.CoinAllocationSummary, error)
}

// SessionRepository is satisfied by *SessionRepo.
type SessionRepository interface {
	GetTimeWindow(ctx context.Context, sessionID string) (start, end time.Time, err error)
}

// SessionReader is satisfied by *SessionRepo. Kept separate from
// SessionRepository (consumed by CoinService for the O2C scan flow) per the
// same interface-segregation pattern as SpeakerRepository.
type SessionReader interface {
	GetSession(ctx context.Context, id string) (models.Session, error)
	GetCurrentSessions(ctx context.Context) ([]models.Session, error)
}

// SpeakerRepository is satisfied by *SpeakerRepo.
type SpeakerRepository interface {
	GetSpeaker(ctx context.Context, id string) (models.Speaker, error)
	GetSpeakerSummary(ctx context.Context, filter models.SpeakerFilter) ([]models.SpeakerSummary, error)
}

// EventReader is satisfied by *EventRepo. Kept as its own interface per the
// existing interface-segregation pattern.
type EventReader interface {
	GetEvents(ctx context.Context) ([]models.Event, error)
	GetEventAgendas(ctx context.Context, eventID string) ([]models.EventAgenda, error)
}

// AttendeeProfileReader is satisfied by *AttendeeProfileRepo (the new
// attendees profile table -- kept separate from AttendeeRepository above,
// which owns the unrelated agenda_attendee registration marker).
type AttendeeProfileReader interface {
	Insert(ctx context.Context, payload models.AttendeeInsert, idpUUID string) error
	GetByEmail(ctx context.Context, email string) (models.Attendee, error)
	GetByUUID(ctx context.Context, idpUUID string) (models.Attendee, error)
	PatchByEmail(ctx context.Context, email string, patch models.AttendeePatch, updatedBy string) error
	Search(ctx context.Context, filter models.AttendeeSearchFilter, excludedUUID string) (models.AttendeeSearchResult, error)
}

// ConnectionReader is satisfied by *ConnectionRepo.
type ConnectionReader interface {
	Get(ctx context.Context, userUUID string) (models.UserConnectionsInfo, error)
	Upsert(ctx context.Context, initiatorUUID, recipientUUID string, status models.ConnectionStatus) error
}

// FeedbackReader is satisfied by *FeedbackRepo.
type FeedbackReader interface {
	Insert(ctx context.Context, in models.FeedbackInsert) error
}

// AppConfigReader is satisfied by *AppConfigRepo.
type AppConfigReader interface {
	List(ctx context.Context) ([]models.AppConfig, error)
}

// FavoritesReader is satisfied by *FavoritesRepo.
type FavoritesReader interface {
	List(ctx context.Context, userUUID string) ([]models.Favorite, error)
	Add(ctx context.Context, userUUID, sessionID string) error
	Remove(ctx context.Context, userUUID, sessionID string) error
}

type ShopRepository interface {
	GetActiveEventID(ctx context.Context) (*string, error)
	GetShopClosingTime(ctx context.Context, activeEventID *string) (*time.Time, error)
	GetVisibleItems(ctx context.Context, activeEventID *string) ([]models.ShopItem, error)
	GetPendingOrderByIdempotencyKey(ctx context.Context, userUUID, idempotencyKey string) (*models.ShopOrder, error)
	UpdateShopOrderShippingDetails(ctx context.Context, orderID string, req models.ShopOrder) error
	GetPastPurchasedQuantities(ctx context.Context, userUUID string, activeEventID *string) (map[string]int, error)
	GetUserPendingOrdersCount(ctx context.Context, userUUID string, activeEventID *string) (int, error)
	CreateOrder(ctx context.Context, order models.ShopOrder) error
	UpdateOrderStatus(ctx context.Context, orderID, status, updatedBy string) error
	ConfirmOrder(ctx context.Context, orderID, updatedBy string, txHash *string) error
	MarkStaleOrders(ctx context.Context, timeoutMinutes int) (int, error)
	GetOrderByTransactionHash(ctx context.Context, txHash string) (*models.ShopOrder, error)
	GetOrderById(ctx context.Context, orderID string) (*models.ShopOrder, error)
	CancelOrderAndRestoreStock(ctx context.Context, orderID string) error
	GetOrderWithItemsById(ctx context.Context, orderID string) (*models.ShopOrder, error)
	GetEventName(ctx context.Context, eventID *string) (string, error)
	GetUserOrders(ctx context.Context, userUUID string, activeEventID *string) ([]models.ShopOrder, error)
	GetAllOrders(ctx context.Context) ([]models.ShopOrder, error)
}

// Compile-time assertions that the concrete repos satisfy their interfaces.
var (
	_ AttendeeRepository       = (*AttendeeRepo)(nil)
	_ CoinAllocationRepository = (*CoinAllocationRepo)(nil)
	_ SessionRepository        = (*SessionRepo)(nil)
	_ SessionReader            = (*SessionRepo)(nil)
	_ SpeakerRepository        = (*SpeakerRepo)(nil)
	_ EventReader              = (*EventRepo)(nil)
	_ AttendeeProfileReader    = (*AttendeeProfileRepo)(nil)
	_ ConnectionReader         = (*ConnectionRepo)(nil)
	_ FeedbackReader           = (*FeedbackRepo)(nil)
	_ AppConfigReader          = (*AppConfigRepo)(nil)
	_ FavoritesReader          = (*FavoritesRepo)(nil)
	_ ShopRepository           = (*ShopRepo)(nil)
)
