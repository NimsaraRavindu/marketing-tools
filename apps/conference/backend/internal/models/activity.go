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

// ActivityLocation is where an activity happens. Stored inline on the
// activities row (migration 011) but kept nested in the response, matching
// the shape the client already reads.
type ActivityLocation struct {
	Name         string `json:"name"`
	Address      string `json:"address"`
	FloorPlanURL string `json:"floorPlanUrl"`
}

// Activity is one non-session happening on the General page -- meals,
// registration desk hours, parties. Several activities commonly share a name
// and differ only by time; the client groups them by name itself.
//
// Location is omitted entirely when the activity has no location recorded,
// rather than serialized as an object of empty strings, so the client's
// `location?.name` renders nothing instead of a blank line.
type Activity struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	StartTime time.Time         `json:"startTime"`
	EndTime   time.Time         `json:"endTime"`
	Location  *ActivityLocation `json:"location,omitempty"`

	// Description is optional in practice; an activity with nothing to say
	// beyond its name and time is normal.
	Description string `json:"description"`
}
