-- Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
--
-- WSO2 LLC. licenses this file to you under the Apache License,
-- Version 2.0 (the "License"); you may not use this file except
-- in compliance with the License.
-- You may obtain a copy of the License at
--
-- http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing,
-- software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
-- KIND, either express or implied.  See the License for the
-- specific language governing permissions and limitations
-- under the License.

-- Owned table backing GET /activities: the non-session happenings on the
-- General page (registration desk hours, meals, parties). Nothing upstream
-- models these -- conference_days/sessions cover the agenda proper, and an
-- activity is deliberately not a session (no track, no room, no speakers, and
-- it repeats across days under one name).
--
-- The old MySQL schema spread this over three tables (activity -> location ->
-- venue, both joins INNER). That shape is collapsed here into one row for two
-- reasons: the only consumer reads location.{name,address,floorPlanUrl} and
-- never touches venue at all (General.tsx), and the INNER joins meant an
-- activity with a dangling locationId/venueId silently vanished from the list.
-- Inlining the location removes that failure mode -- an activity with no
-- location still lists, just without the venue line. The response still nests
-- a `location` object so the client shape is unchanged; if activities ever
-- need multiple venues or a real venue entity, this splits back out.
--
-- start_time/end_time are TIMESTAMPTZ, not the old varchar. The frontend's
-- parseConferenceTime already takes the offset-bearing branch for an RFC3339
-- string, so a real instant renders correctly and no longer depends on the
-- client guessing the venue zone.
--
-- No FK to any shared table (none is relevant here anyway) -- same
-- no-FK-to-shared-tables precedent as favorites/feedback/presentation_overlay.
--
-- NOTE: there is no write path for this data in either the old or the new
-- service; the old MySQL rows were populated out of band. Until rows are
-- seeded, GET /activities correctly returns an empty list.
CREATE TABLE activities (
  id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name                    TEXT NOT NULL,
  description             TEXT NOT NULL DEFAULT '',
  start_time              TIMESTAMPTZ NOT NULL,
  end_time                TIMESTAMPTZ NOT NULL,
  location_name           TEXT NOT NULL DEFAULT '',
  location_address        TEXT NOT NULL DEFAULT '',
  location_floor_plan_url TEXT NOT NULL DEFAULT '',
  created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT activities_time_order CHECK (end_time >= start_time)
);

-- The list endpoint's only ordering: activities are grouped by name on the
-- client, then each group's occurrences shown in chronological order.
CREATE INDEX activities_name_start_time_idx ON activities (name, start_time);

-- Required: migrations run under a developer's own role, while the deployed
-- service connects as a separate role that would otherwise get
-- "permission denied for table activities" on its first read (which is exactly
-- what happened to room_colors in migration 010). Set DB_USER-of-the-service
-- here; adjust the role name per environment.
-- GRANT SELECT ON activities TO eventdashboard_stg_user;
