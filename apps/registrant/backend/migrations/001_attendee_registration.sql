-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

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
