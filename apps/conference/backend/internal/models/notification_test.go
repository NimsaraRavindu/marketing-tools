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
	"testing"
)

func TestUserNotificationRequest_Validate(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		description string
		want        string
	}{
		{"valid", "Keynote starting", "Hall A, in 10 minutes", ""},
		{"empty title", "", "body", "title is required"},
		{"whitespace-only title", "   \t\n ", "body", "title is required"},
		{"empty description is allowed", "title", "", ""},

		{"title at limit", strings.Repeat("a", NotificationTitleMaxLen), "", ""},
		{"title over limit", strings.Repeat("a", NotificationTitleMaxLen+1), "", "title exceeds the maximum length"},
		{"description at limit", "title", strings.Repeat("a", NotificationBodyMaxLen), ""},
		{"description over limit", "title", strings.Repeat("a", NotificationBodyMaxLen+1), "description exceeds the maximum length"},

		// Multi-byte input must be counted in runes: each of these is well
		// over the byte limit while sitting exactly on the rune limit.
		{"multi-byte title at limit", strings.Repeat("ම", NotificationTitleMaxLen), "", ""},
		{"multi-byte title over limit", strings.Repeat("ම", NotificationTitleMaxLen+1), "", "title exceeds the maximum length"},
		{"emoji title at limit", strings.Repeat("🎉", NotificationTitleMaxLen), "", ""},
		{"emoji title over limit", strings.Repeat("🎉", NotificationTitleMaxLen+1), "", "title exceeds the maximum length"},
		{"multi-byte description at limit", "title", strings.Repeat("ම", NotificationBodyMaxLen), ""},
		{"multi-byte description over limit", "title", strings.Repeat("ම", NotificationBodyMaxLen+1), "description exceeds the maximum length"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := UserNotificationRequest{Title: tt.title, Description: tt.description}
			if got := req.Validate(); got != tt.want {
				t.Errorf("Validate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUserNotificationRequest_ValidateTrimsInPlace(t *testing.T) {
	req := UserNotificationRequest{Title: "  Keynote  ", Description: "\n Hall A \t"}

	if problem := req.Validate(); problem != "" {
		t.Fatalf("Validate() = %q, want no problem", problem)
	}
	if req.Title != "Keynote" {
		t.Errorf("Title = %q, want %q", req.Title, "Keynote")
	}
	if req.Description != "Hall A" {
		t.Errorf("Description = %q, want %q", req.Description, "Hall A")
	}
}

// Surrounding whitespace must not push an otherwise-valid title over the
// limit, since trimming happens before the length check.
func TestUserNotificationRequest_ValidateTrimsBeforeLengthCheck(t *testing.T) {
	req := UserNotificationRequest{Title: "  " + strings.Repeat("a", NotificationTitleMaxLen) + "  "}

	if got := req.Validate(); got != "" {
		t.Errorf("Validate() = %q, want no problem", got)
	}
}
