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

import "time"

// Favorite is a reference to a favorited session, never a snapshot of its
// fields (see .claude/PLAN.md Phase F): only the session id and when it was
// added are stored, so the client resolves the reference against live session
// data and a session edit is reflected automatically.
type Favorite struct {
	SessionID string    `json:"sessionId"`
	CreatedAt time.Time `json:"createdAt"`
}

// FavoritesList is the response shape for GET /users/me/favorites. Items is
// always a non-nil array.
type FavoritesList struct {
	Items []Favorite `json:"items"`
}
