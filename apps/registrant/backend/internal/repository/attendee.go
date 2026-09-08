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
	"database/sql"
	"strings"

	"attendee-registration/internal/crypto"
)

// GetAttendeeSummary returns one row per agenda (session) registration for
// the current event, grouped by its conference day or activity label. 
// attendee_id is encrypted at rest, so this decrypts each row here to produce 
// Username and to classify UserType, since neither can be done in SQL against
// ciphertext.
func (r *Repository) GetAttendeeSummary(ctx context.Context) ([]AttendeeSummary, error) {
	const q = `
		SELECT
			ar.day_label AS agenda,
			ar.attendee_id AS username,
			ar.updated_by AS scannedBy
		FROM
			attendee_registration ar
			LEFT JOIN conference_days d ON ar.day_label = d.label
			LEFT JOIN con_activities a ON ar.day_label = a.name
		WHERE
			d.config_id = (SELECT id FROM conference_config ORDER BY start_date DESC LIMIT 1)
			OR a.config_id = (SELECT id FROM conference_config ORDER BY start_date DESC LIMIT 1)
		ORDER BY
			ar.day_label ASC`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := []AttendeeSummary{}
	for rows.Next() {
		var s AttendeeSummary
		var encrypted string
		var scannedBy sql.NullString
		if err := rows.Scan(&s.Agenda, &encrypted, &scannedBy); err != nil {
			return nil, err
		}
		email, err := crypto.Decrypt(encrypted)
		if err != nil {
			return nil, err
		}
		s.Username = email
		if strings.HasSuffix(strings.ToLower(email), wso2Domain) {
			s.UserType = "Internal"
		} else {
			s.UserType = "External"
		}
		if scannedBy.Valid {
			s.ScannedBy = &scannedBy.String
		}
		summaries = append(summaries, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return summaries, nil
}
