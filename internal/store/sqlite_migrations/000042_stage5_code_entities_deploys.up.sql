-- Stage 5: Code Entity Registry + Deploy Intelligence

CREATE TABLE IF NOT EXISTS code_entities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL DEFAULT '' CHECK(entity_type IN ('file', 'controller', 'endpoint')),
    entity_name TEXT NOT NULL DEFAULT '',
    service TEXT NOT NULL DEFAULT '',
    risk_score REAL NOT NULL DEFAULT 0.0,
    error_count INTEGER NOT NULL DEFAULT 0,
    investigation_count INTEGER NOT NULL DEFAULT 0,
    avg_duration_ms REAL DEFAULT NULL,
    last_error_at TEXT DEFAULT NULL,
    last_investigation_at TEXT DEFAULT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_code_entities_type_name_service
    ON code_entities(entity_type, entity_name, service);
CREATE INDEX IF NOT EXISTS idx_code_entities_risk
    ON code_entities(service, risk_score DESC);

CREATE TABLE IF NOT EXISTS deploys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    service TEXT NOT NULL DEFAULT '',
    environment TEXT NOT NULL DEFAULT '',
    commit_hash TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    files_changed_json TEXT NOT NULL DEFAULT '[]',
    deploy_source TEXT NOT NULL DEFAULT 'webhook'
        CHECK(deploy_source IN ('webhook', 'auto-detected', 'manual')),
    pre_error_rate REAL DEFAULT NULL,
    post_error_rate REAL DEFAULT NULL,
    pre_avg_duration_ms REAL DEFAULT NULL,
    post_avg_duration_ms REAL DEFAULT NULL,
    impact_measured_at TEXT DEFAULT NULL,
    linked_investigation_ids_json TEXT NOT NULL DEFAULT '[]',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending', 'measured', 'incident')),
    deployed_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_deploys_service ON deploys(service, deployed_at DESC);
CREATE INDEX IF NOT EXISTS idx_deploys_commit ON deploys(commit_hash);
CREATE INDEX IF NOT EXISTS idx_deploys_status ON deploys(status, deployed_at DESC);
