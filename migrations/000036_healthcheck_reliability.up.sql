-- Add expected_body and retries columns to healthchecks for response body matching and retry support.
ALTER TABLE healthchecks ADD COLUMN expected_body TEXT NOT NULL DEFAULT '';
ALTER TABLE healthchecks ADD COLUMN retries INTEGER NOT NULL DEFAULT 0;
