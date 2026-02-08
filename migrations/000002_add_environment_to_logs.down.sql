DROP INDEX IF EXISTS idx_logs_environment;
ALTER TABLE logs DROP COLUMN IF EXISTS environment;
