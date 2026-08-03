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

//go:build integration

package repository

import (
	"context"
	"testing"
)

func cleanupFavorites(t *testing.T, userUUID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), "DELETE FROM favorites WHERE user_uuid = $1", userUUID)
	})
}

func TestFavoritesRepo_AddThenListReturnsReference(t *testing.T) {
	ctx := context.Background()
	repo := NewFavoritesRepo(testDB)

	user := newUUID()
	session := newUUID()
	cleanupFavorites(t, user)

	if err := repo.Add(ctx, user, session); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	items, err := repo.List(ctx, user)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 1 || items[0].SessionID != session {
		t.Fatalf("List = %+v, want exactly session %q", items, session)
	}
	if items[0].CreatedAt.IsZero() {
		t.Errorf("createdAt is zero, want the DB default NOW()")
	}
}

func TestFavoritesRepo_AddIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := NewFavoritesRepo(testDB)

	user := newUUID()
	session := newUUID()
	cleanupFavorites(t, user)

	if err := repo.Add(ctx, user, session); err != nil {
		t.Fatalf("first Add returned error: %v", err)
	}
	if err := repo.Add(ctx, user, session); err != nil {
		t.Fatalf("second Add returned error: %v (must be a no-op, not a conflict)", err)
	}

	var count int
	if err := testDB.QueryRow(ctx,
		"SELECT COUNT(*) FROM favorites WHERE user_uuid = $1 AND session_id = $2", user, session,
	).Scan(&count); err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want exactly 1 (re-add must not duplicate)", count)
	}
}

func TestFavoritesRepo_RemoveNonExistentIsNoError(t *testing.T) {
	ctx := context.Background()
	repo := NewFavoritesRepo(testDB)

	user := newUUID()
	cleanupFavorites(t, user)

	// Removing a favorite that was never added must be idempotent, not an error.
	if err := repo.Remove(ctx, user, newUUID()); err != nil {
		t.Fatalf("Remove of a non-existent favorite returned error: %v", err)
	}
}

func TestFavoritesRepo_RemoveDeletesTheFavorite(t *testing.T) {
	ctx := context.Background()
	repo := NewFavoritesRepo(testDB)

	user := newUUID()
	session := newUUID()
	cleanupFavorites(t, user)

	if err := repo.Add(ctx, user, session); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if err := repo.Remove(ctx, user, session); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}

	items, err := repo.List(ctx, user)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("List = %+v, want empty after removal", items)
	}
}

func TestFavoritesRepo_ListEmptyReturnsNonNil(t *testing.T) {
	ctx := context.Background()
	repo := NewFavoritesRepo(testDB)

	items, err := repo.List(ctx, newUUID())
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if items == nil {
		t.Errorf("expected non-nil empty slice, got nil")
	}
}

func TestFavoritesRepo_ListOrderedByCreatedAt(t *testing.T) {
	ctx := context.Background()
	repo := NewFavoritesRepo(testDB)

	user := newUUID()
	cleanupFavorites(t, user)

	first := newUUID()
	second := newUUID()
	if err := repo.Add(ctx, user, first); err != nil {
		t.Fatalf("Add(first) returned error: %v", err)
	}
	if err := repo.Add(ctx, user, second); err != nil {
		t.Fatalf("Add(second) returned error: %v", err)
	}

	items, err := repo.List(ctx, user)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("List returned %d items, want 2", len(items))
	}
	if items[0].CreatedAt.After(items[1].CreatedAt) {
		t.Errorf("favorites not ordered oldest-first: %+v", items)
	}
}
