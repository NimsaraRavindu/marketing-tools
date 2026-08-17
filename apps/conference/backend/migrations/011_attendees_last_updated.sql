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

-- Freshness beacon: attendees_last_updated_at.
--
-- An app_config row carrying the timestamp of the last successful change to
-- the attendee directory, so a client can poll one tiny row instead of
-- refetching the directory on a timer. The agenda and speakers equivalents
-- (agenda_last_updated_at, speakers_last_updated_at) are defined by the
-- agenda-organizer repo's migrations/026_app_config_last_updated.sql, which
-- owns those tables; this file covers only attendees, which is ours. Neither
-- migration touches the other's tables.
--
-- This is the first trigger and the first plpgsql function in this repo, so
-- it is worth saying why the rule is bent here. app_config deliberately has
-- no write route (006_app_config.sql; AppConfigReader exposes List only), and
-- attendees is written from three places that no single Go hook can cover:
-- AttendeeProfileRepo.Insert, AttendeeProfileRepo.PatchByEmail, and direct
-- psql seeding, which is how the directory is actually populated today.
-- Both Go writers run as a bare pool.Exec with no transaction, so a paired
-- Go-side bump would be a second statement that can fail on its own and leave
-- the beacon lying in either direction. A trigger fires inside the writing
-- statement's own transaction, so the bump commits or rolls back atomically
-- with the write that caused it — which is the entire requirement.
--
-- Scope is the attendees table only. favorites, feedback and user_connection
-- are per-user rows, not directory state, and bumping a directory-wide beacon
-- on every favourite toggle would defeat the point of having one.
-- agenda_attendee is deliberately excluded too: it is a registration marker
-- populated by the external agenda-organizer sync, not attendee profile data.

-- Defined with CREATE OR REPLACE and an identical body in the agenda-organizer
-- beacon migration, because migrations across the two repos are applied by
-- hand with no ordering guarantee and neither may assume the other ran first.
-- Keep the two definitions byte-identical if either is edited.
CREATE OR REPLACE FUNCTION touch_app_config()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  -- Statement-level triggers fire even when the statement matched no rows.
  -- The transition table keeps a no-op UPDATE from advertising a change that
  -- did not happen. TRUNCATE cannot carry a transition table and is
  -- deliberately not wired up.
  IF NOT EXISTS (SELECT 1 FROM changed_rows) THEN
    RETURN NULL;
  END IF;

  INSERT INTO app_config (config_key, value, created_by, updated_by)
  VALUES (
    TG_ARGV[0],
    to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
    'SYSTEM',
    'SYSTEM'
  )
  ON CONFLICT (config_key) DO UPDATE
    SET value      = EXCLUDED.value,
        updated_at = now(),
        updated_by = EXCLUDED.updated_by;

  RETURN NULL;
END;
$$;

-- Statement-level, not FOR EACH ROW: a bulk directory seed should cost one
-- upsert, not one per attendee. Transition tables cannot be attached to a
-- trigger covering several events, hence three triggers rather than one.
DROP TRIGGER IF EXISTS touch_app_config_ins ON attendees;
CREATE TRIGGER touch_app_config_ins
  AFTER INSERT ON attendees
  REFERENCING NEW TABLE AS changed_rows
  FOR EACH STATEMENT
  EXECUTE FUNCTION touch_app_config('attendees_last_updated_at');

DROP TRIGGER IF EXISTS touch_app_config_upd ON attendees;
CREATE TRIGGER touch_app_config_upd
  AFTER UPDATE ON attendees
  REFERENCING NEW TABLE AS changed_rows
  FOR EACH STATEMENT
  EXECUTE FUNCTION touch_app_config('attendees_last_updated_at');

DROP TRIGGER IF EXISTS touch_app_config_del ON attendees;
CREATE TRIGGER touch_app_config_del
  AFTER DELETE ON attendees
  REFERENCING OLD TABLE AS changed_rows
  FOR EACH STATEMENT
  EXECUTE FUNCTION touch_app_config('attendees_last_updated_at');

-- Seed the key so a client polling before the first write reads a value
-- rather than an absent row. DO NOTHING keeps this re-runnable.
INSERT INTO app_config (config_key, value, created_by, updated_by)
VALUES (
  'attendees_last_updated_at',
  to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
  'SYSTEM',
  'SYSTEM'
)
ON CONFLICT (config_key) DO NOTHING;
