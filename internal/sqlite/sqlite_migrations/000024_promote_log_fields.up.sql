-- Promote key fields from metadata JSON to indexed columns for fast AI-driven search.

-- New columns (environment already exists from 000001_init)
ALTER TABLE logs ADD COLUMN commit_hash TEXT;
ALTER TABLE logs ADD COLUMN request_id TEXT;
ALTER TABLE logs ADD COLUMN exception_class TEXT;
ALTER TABLE logs ADD COLUMN error_fingerprint TEXT;
ALTER TABLE logs ADD COLUMN source_file TEXT;
ALTER TABLE logs ADD COLUMN source_line INTEGER;

-- Indexes for Claude Code's most common queries
CREATE INDEX idx_logs_commit_hash       ON logs(commit_hash)       WHERE commit_hash IS NOT NULL;
CREATE INDEX idx_logs_request_id        ON logs(request_id)        WHERE request_id IS NOT NULL;
-- Replace the existing full index with a partial index (old one from 000011)
DROP INDEX IF EXISTS idx_logs_environment;
CREATE INDEX idx_logs_environment       ON logs(environment)        WHERE environment != '';
CREATE INDEX idx_logs_exception_class   ON logs(exception_class)   WHERE exception_class IS NOT NULL;
CREATE INDEX idx_logs_error_fingerprint ON logs(error_fingerprint) WHERE error_fingerprint IS NOT NULL;
CREATE INDEX idx_logs_source_file       ON logs(source_file)       WHERE source_file IS NOT NULL;

-- Backfill from existing metadata JSON
UPDATE logs SET commit_hash = json_extract(metadata, '$.git_sha')
WHERE json_extract(metadata, '$.git_sha') IS NOT NULL AND commit_hash IS NULL;

UPDATE logs SET request_id = json_extract(metadata, '$.request_id')
WHERE json_extract(metadata, '$.request_id') IS NOT NULL AND request_id IS NULL;

UPDATE logs SET exception_class = json_extract(metadata, '$.exception_class')
WHERE json_extract(metadata, '$.exception_class') IS NOT NULL AND exception_class IS NULL;

UPDATE logs SET error_fingerprint = json_extract(metadata, '$.error_fingerprint')
WHERE json_extract(metadata, '$.error_fingerprint') IS NOT NULL AND error_fingerprint IS NULL;
