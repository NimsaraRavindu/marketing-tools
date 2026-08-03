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
	"testing"
	"time"
)

func TestFavoritesList_ItemsAlwaysArray(t *testing.T) {
	// An empty, non-nil slice must serialize as [], never null.
	b, err := json.Marshal(FavoritesList{Items: []Favorite{}})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if got := string(b); got != `{"items":[]}` {
		t.Errorf("got %s, want {\"items\":[]}", got)
	}
}

func TestFavorite_JSONShape(t *testing.T) {
	created := time.Date(2026, 7, 23, 10, 30, 0, 0, time.UTC)
	b, err := json.Marshal(Favorite{SessionID: "11111111-1111-1111-1111-111111111111", CreatedAt: created})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got["sessionId"] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("sessionId = %v", got["sessionId"])
	}
	if got["createdAt"] != "2026-07-23T10:30:00Z" {
		t.Errorf("createdAt = %v, want ISO instant", got["createdAt"])
	}
}
