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

// LeaderboardEntry represents a single entry on the leaderboard.
// Field names match the frontend's expected JSON shape.
type LeaderboardEntry struct {
	UserID       string  `json:"userId"`
	FirstName    string  `json:"firstName"`
	LastName     string  `json:"lastName"`
	ShowFullName bool    `json:"showFullName"`
	TotalCoins   float64 `json:"totalCoins"`
}
