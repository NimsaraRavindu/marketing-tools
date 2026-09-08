-- Drop the old session foreign key
ALTER TABLE attendee_registration DROP CONSTRAINT attendee_registration_session_id_fkey;

-- Rename session_id to day_label and change it to TEXT
ALTER TABLE attendee_registration RENAME COLUMN session_id TO day_label;
ALTER TABLE attendee_registration ALTER COLUMN day_label TYPE TEXT;

-- Drop the old primary key and index
ALTER TABLE attendee_registration DROP CONSTRAINT attendee_registration_pkey;
DROP INDEX IF EXISTS attendee_registration_session_id_idx;

-- Add the new primary key and index
ALTER TABLE attendee_registration ADD PRIMARY KEY (attendee_id, day_label);
CREATE INDEX attendee_registration_day_label_idx ON attendee_registration (day_label);

-- Add scan_enabled to upstream con_activities
ALTER TABLE con_activities ADD COLUMN scan_enabled BOOLEAN DEFAULT false;
