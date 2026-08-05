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

-- Owned overlay giving every room a hex colour. tracks.color (upstream) only
-- covers rooms that have a track: for WSO2Con NA that is Red/Yellow/Green, so
-- the nine Blue Room keynotes and every break came back with no colour at all
-- and the frontend fell through to its ROOM_COLOR_MAP name-sniffing (FE.md
-- 3.5). rooms is in the shared read-only marketingops schema, so this service
-- can't add rooms.color itself -- it owns the mapping here instead, same
-- no-FK-to-shared-tables precedent as presentation_overlay/feedback.
--
-- The backend LEFT JOINs this at serve time as Session.roomColor; a missing
-- row means the room has no colour and the field is omitted. trackColor stays
-- exactly as it was -- upstream tracks.color, untouched.
--
-- An upstream request for rooms.color is filed in parallel; if it lands, this
-- table collapses to a plain join and can be dropped.
CREATE TABLE room_colors (
  room_id    UUID PRIMARY KEY,
  color      TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One-time seed of the rooms that exist today. A room that already has a track
-- reuses that track's hex so roomColor and trackColor never disagree for the
-- same session. The remaining rooms have no colour anywhere upstream, so they
-- take the hex the frontend renders for them today: "Blue Room" hits the
-- ROOM_COLOR_MAP `blue` entry (#08BAF6), anything else hits the gray fallback
-- (#9297AF). Matching room names here is the same string-sniffing this change
-- removes, which is fine for a one-shot seed of known rows and is exactly why
-- it lives in a migration instead of in the serving path.
--
-- Rooms created after this runs get no row, and their sessions come back
-- without roomColor until someone inserts one.
INSERT INTO room_colors (room_id, color)
SELECT r.id,
       COALESCE(
         (SELECT t.color FROM tracks t WHERE t.room_id = r.id LIMIT 1),
         CASE WHEN lower(r.name) LIKE 'blue %' THEN '#08BAF6' ELSE '#9297AF' END
       )
FROM rooms r
ON CONFLICT (room_id) DO NOTHING;
