-- ============================================================================
-- 000002 — bring an EXISTING install up to the schema a fresh install gets
-- from the corrected 000001.
--
-- Why this file exists: RunSQLiteMigrations skips any file whose version is
-- <= the recorded schema_version. 000001 is recorded as applied on every
-- deployed instance, so edits made to it in place never reach them. Everything
-- below is therefore a forward-only restatement of those edits, written so it
-- is a harmless no-op on a fresh database (where 000001 has just produced the
-- correct schema moments earlier) and safe to re-run.
--
-- The data_sources rebuild recreates the table, so foreign key enforcement has
-- to be off for the duration. PRAGMA foreign_keys cannot be changed inside a
-- transaction, and applyMigration wraps the whole file in one, so we ask the
-- runner to disable it on a dedicated connection first:
-- pragma:foreign_keys_off
-- ============================================================================

-- ----------------------------------------------------------------------------
-- 1. Redundant indexes.
--
-- users.email and sessions.token are both UNIQUE, and SQLite already backs a
-- UNIQUE constraint with an implicit index. The explicit duplicates were pure
-- write amplification on signup and on every login, so 000001 stopped creating
-- them; drop the ones existing installs already have.
-- ----------------------------------------------------------------------------

DROP INDEX IF EXISTS idx_users_email;
DROP INDEX IF EXISTS idx_sessions_token;

-- ----------------------------------------------------------------------------
-- 2. data_sources.type CHECK constraint.
--
-- The original constraint only allowed ('logs', 'database', 'monitoring'),
-- which made it impossible to create a mysql, redis, turso or server_metrics
-- connector at all — the INSERT failed with "CHECK constraint failed". SQLite
-- cannot ALTER a CHECK constraint, so this is the documented table rebuild:
-- create the corrected table, copy every row, drop the old one, rename, then
-- recreate every index that existed on it.
--
-- The corrected column DEFAULTs (RFC3339 via strftime instead of datetime(),
-- which emits "YYYY-MM-DD HH:MM:SS" and breaks string comparison against
-- RFC3339 cutoffs) are carried over as part of the same rebuild.
--
-- Keep the type list in sync with the ConnectorType constants in
-- pkg/store/models_connectors.go.
--
-- Nothing references data_sources by foreign key, so no child rows can be
-- orphaned by the swap; the FK pragma is off only because SQLite would
-- otherwise rewrite/refuse the rename.
-- ----------------------------------------------------------------------------

DROP TABLE IF EXISTS data_sources__new;

CREATE TABLE data_sources__new (
    id             TEXT PRIMARY KEY,
    -- Keep in sync with the ConnectorType constants in pkg/store/models_connectors.go.
    type           TEXT NOT NULL CHECK(type IN ('logs', 'database', 'mysql', 'redis', 'turso', 'monitoring', 'server_metrics')),
    name           TEXT NOT NULL DEFAULT '',
    config         TEXT NOT NULL DEFAULT '{}',
    status         TEXT NOT NULL DEFAULT 'disconnected' CHECK(status IN ('connected', 'disconnected', 'error')),
    status_message TEXT,
    last_tested_at TEXT,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    environment    TEXT NOT NULL DEFAULT ''
);

INSERT INTO data_sources__new
    (id, type, name, config, status, status_message, last_tested_at, created_at, updated_at, environment)
SELECT
    id, type, name, config, status, status_message, last_tested_at, created_at, updated_at, environment
FROM data_sources;

DROP TABLE data_sources;

ALTER TABLE data_sources__new RENAME TO data_sources;

CREATE INDEX IF NOT EXISTS idx_data_sources_env ON data_sources(environment) WHERE environment != '';
CREATE INDEX IF NOT EXISTS idx_data_sources_type ON data_sources(type);

-- ----------------------------------------------------------------------------
-- 3. Timestamp format backfill.
--
-- Timestamps are RFC3339 TEXT ("2006-01-02T15:04:05Z") and are compared as
-- strings, including against indexed columns. Rows written before the store
-- layer normalised on write went through bun's sqlite dialect instead, which
-- formats time.Time as "2006-01-02 15:04:05.999999-07:00" — a space where
-- RFC3339 has 'T'. Because ' ' (0x20) sorts below 'T' (0x54), every such row
-- compares less than any same-day RFC3339 cutoff, which is what made a server
-- that heartbeated one second ago sweep as stale and a live session read as
-- expired.
--
-- These statements lived at the end of 000001, where they could only ever run
-- on a fresh install that has nothing to convert. They belong here.
--
-- Two guards, both required:
--   * LIKE '____-__-__ %'  — only the space-separated datetime shape is a
--     candidate. A plain "% %" match would also catch free text.
--   * strftime(...) IS NOT NULL — strftime returns NULL for anything it cannot
--     parse (including date-shaped but invalid values like '2026-13-45 99:99:99').
--     Writing that NULL into a NOT NULL column aborts the entire migration, so
--     malformed values are left exactly as they are instead.
--
-- Both guards are false for already-RFC3339 values, so this is a no-op on a
-- fresh install and safe to re-run.
-- ----------------------------------------------------------------------------

UPDATE servers       SET last_seen_at = strftime('%Y-%m-%dT%H:%M:%SZ', last_seen_at) WHERE last_seen_at LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', last_seen_at) IS NOT NULL;
UPDATE servers       SET created_at   = strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   WHERE created_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   IS NOT NULL;
UPDATE servers       SET updated_at   = strftime('%Y-%m-%dT%H:%M:%SZ', updated_at)   WHERE updated_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', updated_at)   IS NOT NULL;
UPDATE sessions      SET expires_at   = strftime('%Y-%m-%dT%H:%M:%SZ', expires_at)   WHERE expires_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', expires_at)   IS NOT NULL;
UPDATE sessions      SET created_at   = strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   WHERE created_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   IS NOT NULL;
UPDATE audit_log     SET created_at   = strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   WHERE created_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   IS NOT NULL;
UPDATE mcp_activity  SET created_at   = strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   WHERE created_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   IS NOT NULL;
UPDATE agent_notes   SET created_at   = strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   WHERE created_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   IS NOT NULL;
UPDATE agent_notes   SET updated_at   = strftime('%Y-%m-%dT%H:%M:%SZ', updated_at)   WHERE updated_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', updated_at)   IS NOT NULL;
UPDATE code_entities SET created_at   = strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   WHERE created_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   IS NOT NULL;
UPDATE code_entities SET updated_at   = strftime('%Y-%m-%dT%H:%M:%SZ', updated_at)   WHERE updated_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', updated_at)   IS NOT NULL;
UPDATE jobs          SET run_at       = strftime('%Y-%m-%dT%H:%M:%SZ', run_at)       WHERE run_at       LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', run_at)       IS NOT NULL;
UPDATE jobs          SET created_at   = strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   WHERE created_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   IS NOT NULL;
UPDATE data_sources  SET created_at   = strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   WHERE created_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at)   IS NOT NULL;
UPDATE data_sources  SET updated_at   = strftime('%Y-%m-%dT%H:%M:%SZ', updated_at)   WHERE updated_at   LIKE '____-__-__ %' AND strftime('%Y-%m-%dT%H:%M:%SZ', updated_at)   IS NOT NULL;
