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
	"strings"
	"unicode/utf8"
)

// Notification title/body length bounds, carried over from the old
// Ballerina @constraint:String annotations on UserNotificationPayload. The
// frontend caps tighter (50/200) in its own form; these stay at the old
// server-side limits so a stricter client is never the thing that makes a
// valid request fail.
const (
	NotificationTitleMaxLen = 200
	NotificationBodyMaxLen  = 1024
)

// UserNotificationRequest is the payload for POST /users/notifications.
// Field names match what the frontend already sends verbatim.
type UserNotificationRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Validate trims the request in place and reports the first problem with it,
// or an empty string when it is valid. Trimming here rather than in the
// handler keeps "what counts as empty" in one place: a title of only spaces
// is a missing title, not a 200-char-valid one.
//
// Lengths count runes, not bytes, matching the Ballerina constraint these
// bounds came from -- otherwise a non-ASCII title (Sinhala, Tamil, an emoji)
// would be rejected at a third of the advertised limit.
func (r *UserNotificationRequest) Validate() string {
	r.Title = strings.TrimSpace(r.Title)
	r.Description = strings.TrimSpace(r.Description)

	switch {
	case r.Title == "":
		return "title is required"
	case utf8.RuneCountInString(r.Title) > NotificationTitleMaxLen:
		return "title exceeds the maximum length"
	case utf8.RuneCountInString(r.Description) > NotificationBodyMaxLen:
		return "description exceeds the maximum length"
	default:
		return ""
	}
}
