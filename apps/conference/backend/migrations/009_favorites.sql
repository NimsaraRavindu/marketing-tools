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

-- Owned table backing a user's session favorites (see .claude/PLAN.md Phase F).
-- Favorites are stored as references, never a snapshot of session fields: only
-- the session_id is kept, so the client resolves it against live session data
-- and a session time/room change is reflected automatically.
--
-- user_uuid is the caller's JWT sub. session_id has no FK to the shared,
-- read-only sessions table -- same no-FK-to-read-only-tables precedent as
-- feedback, user_connection, and presentation_overlay. The composite primary
-- key makes a favorite idempotent by construction: re-adding the same
-- (user, session) pair conflicts on the PK instead of inserting a duplicate.
CREATE TABLE favorites (
  user_uuid  TEXT NOT NULL,
  session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (user_uuid, session_id)
);
