-- ============================================================================
-- 000004 — deploys as stored rows rather than a derived scan.
--
-- Deploys were previously reconstructed at query time by scanning logs for the
-- first sighting of each commit hash (overview.timeline, analytics.trends).
-- That is fine for a rendered timeline and far too expensive for
-- `since: "last_deploy"`, which needs one lookup rather than a window scan.
--
-- Recorded at ingest: the first time a (commit_hash, service, environment)
-- triple is seen, it lands here and never changes. The UNIQUE constraint makes
-- the ingest-side INSERT OR IGNORE idempotent, so replays and duplicate
-- batches cost nothing.
-- ============================================================================

CREATE TABLE IF NOT EXISTS deploys (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    commit_hash   TEXT NOT NULL,
    service       TEXT NOT NULL DEFAULT '',
    environment   TEXT NOT NULL DEFAULT '',
    first_seen_at TEXT NOT NULL,
    UNIQUE(commit_hash, service, environment)
);

-- Scoped "most recent deploy" is the only read pattern that matters, and it is
-- on the hot path for every `since: "last_deploy"` call.
CREATE INDEX IF NOT EXISTS idx_deploys_scope_time
    ON deploys(environment, service, first_seen_at DESC);
