ALTER TABLE logs ADD COLUMN event_type TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_logs_event_type ON logs(event_type) WHERE event_type != '';
