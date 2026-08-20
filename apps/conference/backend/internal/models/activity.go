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

// ActivityLocation is where an activity happens, nested in the response to match
// the shape the client reads.
//
// The event's own venue is the granularity available. Activities are sourced
// from the upstream-owned con_activities / con_activity_hours tables (upstream
// migration 029), which model no location on either row -- an amenity's place is
// "at the venue", and the pair exists to record what is open when, not where.
// So this is filled from conference_config.venue_name and venue_address
// (upstream migration 018) -- the same venue for every activity in a conference,
// which is the truth about a single-venue event.
//
// FloorPlanURL is consequently never set: nothing upstream models a floor plan
// at any granularity. The field stays because the client already reads
// `location?.floorPlanUrl`, and because a later upstream column would land here
// without a shape change.
//
// A venue recorded with only a name omits the other two fields entirely rather
// than emitting "", per the API-wide "optional scalars are omitted when empty,
// never empty strings" convention (openapi.yaml header). Name itself is not
// omitempty, which is why an unnamed venue produces no ActivityLocation at all
// rather than one with an empty name -- see repository.venueLocation.
type ActivityLocation struct {
	Name         string `json:"name"`
	Address      string `json:"address,omitempty"`
	FloorPlanURL string `json:"floorPlanUrl,omitempty"`
}

// Activity is one *sitting* of a venue amenity on the General page -- the O2 Bar
// open 09:00-14:00, an FDE booth, registration desk hours. Several entries
// commonly share a name and differ only by time, because upstream stores one
// activity with an opening window per day and this flattens the pair; the client
// groups them back by name itself, which is why the split upstream 029 made is
// invisible here.
//
// ID is the opening-window's id, not the activity's, so it stays unique across
// the sittings that share a name.
//
// Location carries the conference's venue and is omitted entirely when that
// venue has no name recorded upstream, rather than serialized as an object of
// empty strings, so the client's `location?.name` renders nothing instead of a
// blank line.
type Activity struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	StartTime time.Time         `json:"startTime"`
	EndTime   time.Time         `json:"endTime"`
	Location  *ActivityLocation `json:"location,omitempty"`

	// Description is optional in practice; an activity with nothing to say
	// beyond its name and time is normal, and is omitted rather than sent
	// as an empty string.
	//
	// Like Session.Title and Session.Description it is sanitized rich-text
	// HTML rather than plain text, on the same allowlist and for the stated
	// reason that these amenities were sessions of kind='activity' before
	// upstream 029 split them out, so their copy carries the same links and
	// emphasis. Every value is in fact plain text today, because the admin
	// Activities page 029 shipped edits this in a plain text field rather than
	// the Quill editor sessions use -- but that is a property of the current
	// admin UI, not of the column or the write path, both of which accept the
	// markup. Documented rather than relied on: a reader that assumes plain
	// text here breaks on the day upstream swaps the control, and nothing in
	// this repo's tests would catch it.
	Description string `json:"description,omitempty"`
}
