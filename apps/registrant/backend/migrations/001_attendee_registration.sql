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

ALTER TABLE agenda_attendee RENAME TO attendee_registration;

ALTER TABLE attendee_registration
  ADD COLUMN session_id UUID REFERENCES sessions (id),
  ADD COLUMN updated_by TEXT;

ALTER TABLE attendee_registration
  ALTER COLUMN session_id SET NOT NULL,
  ALTER COLUMN updated_by SET NOT NULL;

ALTER TABLE attendee_registration DROP CONSTRAINT agenda_attendee_pkey;
ALTER TABLE attendee_registration ADD PRIMARY KEY (attendee_id, session_id);

CREATE INDEX attendee_registration_session_id_idx ON attendee_registration (session_id);
