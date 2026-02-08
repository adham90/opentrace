ALTER TABLE logs ADD COLUMN environment TEXT;
CREATE INDEX idx_logs_environment ON logs (environment);
