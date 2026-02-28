# SQL Migrations

All SQL migrations extracted from `012-mcp-workflow-analytics.md`, consolidated and organized into two parts.

---

## Part 1: Investigation Memory Tables

### Investigation Sessions

```sql
CREATE TABLE IF NOT EXISTS investigation_sessions (
    id TEXT PRIMARY KEY,                              -- UUID

    -- Identity (from auth token)
    user_id TEXT NOT NULL DEFAULT '',                  -- links to users.id
    user_email TEXT NOT NULL DEFAULT '',               -- denormalized for quick display
    user_role TEXT NOT NULL DEFAULT '',                -- "admin" or "member"

    -- Client Info (from MCP initialize)
    client_name TEXT NOT NULL DEFAULT '',              -- "claude-code", "cursor", "continue"
    client_version TEXT NOT NULL DEFAULT '',           -- "1.2.3"
    workspace TEXT NOT NULL DEFAULT '',                -- project path / repo name
    transport TEXT NOT NULL DEFAULT '',                -- "stdio" or "sse"
    connection_id TEXT NOT NULL DEFAULT '',            -- unique per MCP connection

    -- Session Classification
    intent TEXT NOT NULL DEFAULT '',                   -- "investigation", "query", "configuration", "exploration"
    intent_detail TEXT NOT NULL DEFAULT '',            -- "payment timeout errors", "weekly report", etc.
    primary_service TEXT NOT NULL DEFAULT '',          -- dominant service being investigated
    primary_datasource_id INTEGER DEFAULT NULL,       -- dominant data source used

    -- Outcome
    status TEXT NOT NULL DEFAULT 'open'
        CHECK(status IN ('open', 'resolved', 'unresolved', 'abandoned')),
    summary TEXT NOT NULL DEFAULT '',                  -- Claude Code-generated summary
    root_cause TEXT NOT NULL DEFAULT '',               -- Claude Code-generated root cause
    fix_description TEXT NOT NULL DEFAULT '',          -- what was done to fix it

    -- Watcher Links
    created_watcher_ids TEXT NOT NULL DEFAULT '[]',    -- JSON array of watcher IDs created during session
    triggered_by_alert_id TEXT DEFAULT NULL,           -- alert that triggered this investigation
    triggered_by_watcher_id TEXT DEFAULT NULL,         -- watcher that triggered the alert

    -- Error Group Links
    resolved_error_group_ids TEXT NOT NULL DEFAULT '[]',  -- JSON array of error group fingerprints resolved
    investigated_error_fingerprints TEXT NOT NULL DEFAULT '[]', -- error fingerprints viewed via error_detail

    -- Health Check Links
    created_healthcheck_ids TEXT NOT NULL DEFAULT '[]',  -- JSON array of health check IDs created
    triggered_by_healthcheck_id TEXT DEFAULT NULL,       -- health check that triggered investigation

    -- Agent Note Links
    created_note_ids TEXT NOT NULL DEFAULT '[]',         -- JSON array of note IDs created during session
    auto_note_ids TEXT NOT NULL DEFAULT '[]',            -- JSON array of auto-generated note IDs on session close

    -- Runbook Links
    runbooks_executed TEXT NOT NULL DEFAULT '[]',        -- JSON array: [{"name": "slow_database", "step": 2}]

    -- Database Diagnostic Links
    explained_queries TEXT NOT NULL DEFAULT '[]',        -- JSON array of query fingerprints from explain_query
    killed_queries TEXT NOT NULL DEFAULT '[]',           -- JSON array of PIDs from kill_query

    -- Trace Links
    trace_ids TEXT NOT NULL DEFAULT '[]',                -- JSON array of distributed trace IDs followed

    -- Deploy Correlation
    correlated_deploy TEXT NOT NULL DEFAULT '',          -- deploy marker hash if deploy happened near investigation start

    -- Metrics Snapshots
    pre_investigation_snapshot TEXT NOT NULL DEFAULT '{}',   -- JSON: metric values at session start
    post_investigation_snapshot TEXT NOT NULL DEFAULT '{}',  -- JSON: metric values at session end

    -- Metrics
    total_steps INTEGER NOT NULL DEFAULT 0,
    total_errors INTEGER NOT NULL DEFAULT 0,
    tool_sequence TEXT NOT NULL DEFAULT '[]',          -- JSON array: ["list_logs", "query_datasource", ...]
    tool_fingerprint TEXT NOT NULL DEFAULT '',         -- pipe-separated: "list_logs|query_datasource|..."
    arg_signature TEXT NOT NULL DEFAULT '',            -- normalized key patterns for similarity matching

    -- Timing
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_activity_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at TEXT DEFAULT NULL,
    duration_seconds INTEGER NOT NULL DEFAULT 0,

    -- Recurrence Tracking
    recurrence_group TEXT DEFAULT NULL,                -- groups sessions investigating the same recurring issue
    recurrence_count INTEGER NOT NULL DEFAULT 0,      -- which occurrence this is (1st, 2nd, 3rd...)
    previous_session_id TEXT DEFAULT NULL,             -- links to the previous investigation of same issue
    fix_durability_seconds INTEGER DEFAULT NULL        -- how long the previous fix lasted before recurrence
);

CREATE INDEX idx_inv_sessions_user ON investigation_sessions(user_id, started_at DESC);
CREATE INDEX idx_inv_sessions_status ON investigation_sessions(status, started_at DESC);
CREATE INDEX idx_inv_sessions_intent ON investigation_sessions(intent, intent_detail);
CREATE INDEX idx_inv_sessions_service ON investigation_sessions(primary_service);
CREATE INDEX idx_inv_sessions_connection ON investigation_sessions(connection_id);
CREATE INDEX idx_inv_sessions_watcher ON investigation_sessions(triggered_by_watcher_id);
CREATE INDEX idx_inv_sessions_healthcheck ON investigation_sessions(triggered_by_healthcheck_id);
CREATE INDEX idx_inv_sessions_recurrence ON investigation_sessions(recurrence_group);
CREATE INDEX idx_inv_sessions_fingerprint ON investigation_sessions(tool_fingerprint);
CREATE INDEX idx_inv_sessions_deploy ON investigation_sessions(correlated_deploy);
```

### Enhanced MCP Activity (extends existing mcp_activity table)

```sql
ALTER TABLE mcp_activity ADD COLUMN investigation_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_activity ADD COLUMN step_index INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mcp_activity ADD COLUMN context TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_activity ADD COLUMN was_suggested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mcp_activity ADD COLUMN suggestion_rank INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mcp_activity ADD COLUMN followed_by TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_mcp_activity_inv_session
    ON mcp_activity(investigation_session_id, step_index);
```

### Tool Transitions (pre-computed for fast ranking)

```sql
CREATE TABLE IF NOT EXISTS tool_transitions (
    from_tool TEXT NOT NULL,
    to_tool TEXT NOT NULL,
    intent TEXT NOT NULL DEFAULT '',
    total_count INTEGER NOT NULL DEFAULT 0,
    resolved_count INTEGER NOT NULL DEFAULT 0,          -- transitions in resolved sessions
    abandoned_count INTEGER NOT NULL DEFAULT 0,         -- transitions in abandoned sessions
    avg_duration_ms INTEGER NOT NULL DEFAULT 0,
    last_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (from_tool, to_tool, intent)
);
```

### Workflow Templates (golden paths for cold start)

```sql
CREATE TABLE IF NOT EXISTS workflow_templates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    intent TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',                      -- "Connection Pool Investigation"
    step_order INTEGER NOT NULL,
    tool_name TEXT NOT NULL,
    args_hint TEXT NOT NULL DEFAULT '{}',               -- JSON: suggested args
    source TEXT NOT NULL DEFAULT 'curated'
        CHECK(source IN ('curated', 'learned')),
    resolved_session_count INTEGER NOT NULL DEFAULT 0,  -- how many resolved sessions match this path
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_workflow_templates_intent ON workflow_templates(intent, step_order);
```

### Query Memory (fingerprints from explain_query investigations)

```sql
CREATE TABLE IF NOT EXISTS query_memory (
    fingerprint TEXT PRIMARY KEY,                       -- normalized query fingerprint
    last_investigation_session_id TEXT NOT NULL DEFAULT '',
    investigation_count INTEGER NOT NULL DEFAULT 0,
    last_root_cause TEXT NOT NULL DEFAULT '',           -- "missing index on users.email"
    last_fix TEXT NOT NULL DEFAULT '',                  -- "CREATE INDEX idx_users_email ON users(email)"
    avg_duration_before_ms INTEGER DEFAULT NULL,
    avg_duration_after_ms INTEGER DEFAULT NULL,         -- if post-fix snapshot shows improvement
    first_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

### Runbook Effectiveness

```sql
CREATE TABLE IF NOT EXISTS runbook_effectiveness (
    runbook_name TEXT PRIMARY KEY,
    total_executions INTEGER NOT NULL DEFAULT 0,
    resolved_sessions INTEGER NOT NULL DEFAULT 0,       -- sessions that resolved after running this runbook
    abandoned_sessions INTEGER NOT NULL DEFAULT 0,
    avg_steps_after INTEGER NOT NULL DEFAULT 0,         -- average additional steps after runbook
    avg_session_duration_seconds INTEGER NOT NULL DEFAULT 0,
    last_executed_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

---

## Part 2: AI Agent Assistant Tables

### Code Entities (New Capability 1)

```sql
CREATE TABLE IF NOT EXISTS code_entities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL
        CHECK(entity_type IN ('file', 'function', 'class', 'endpoint', 'query')),
    entity_path TEXT NOT NULL,             -- "app/controllers/orders_controller.rb"
    entity_name TEXT NOT NULL DEFAULT '',   -- "OrdersController#index"
    service TEXT NOT NULL DEFAULT '',

    -- Production stats (rolled up from investigations, errors, request summaries)
    error_count_30d INTEGER NOT NULL DEFAULT 0,
    investigation_count INTEGER NOT NULL DEFAULT 0,
    last_incident_at TEXT DEFAULT NULL,
    last_incident_summary TEXT NOT NULL DEFAULT '',
    last_investigation_session_id TEXT DEFAULT NULL,
    avg_response_ms REAL DEFAULT NULL,
    p95_response_ms REAL DEFAULT NULL,
    has_n_plus_one INTEGER NOT NULL DEFAULT 0,

    -- Risk score
    risk_score REAL NOT NULL DEFAULT 0.0,      -- 0.0-1.0
    risk_factors TEXT NOT NULL DEFAULT '[]',    -- JSON: ["3 incidents in 30 days", "N+1 query"]

    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX idx_code_entities_lookup ON code_entities(entity_type, entity_path, entity_name);
CREATE INDEX idx_code_entities_service ON code_entities(service);
CREATE INDEX idx_code_entities_risk ON code_entities(risk_score DESC);
```

### Deploys (New Capability 2)

```sql
CREATE TABLE IF NOT EXISTS deploys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    commit_hash TEXT NOT NULL,
    branch TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    files_changed TEXT NOT NULL DEFAULT '[]',     -- JSON array of file paths
    service TEXT NOT NULL DEFAULT '',
    environment TEXT NOT NULL DEFAULT 'production',

    -- Impact tracking (filled in asynchronously after deploy)
    error_rate_before REAL DEFAULT NULL,
    error_rate_after REAL DEFAULT NULL,
    error_rate_delta REAL DEFAULT NULL,
    response_time_before_ms REAL DEFAULT NULL,
    response_time_after_ms REAL DEFAULT NULL,
    response_time_delta_ms REAL DEFAULT NULL,
    caused_incident INTEGER NOT NULL DEFAULT 0,
    incident_session_id TEXT DEFAULT NULL,

    deployed_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_deploys_service ON deploys(service, deployed_at DESC);
CREATE INDEX idx_deploys_commit ON deploys(commit_hash);
CREATE INDEX idx_deploys_impact ON deploys(caused_incident, deployed_at DESC);
```

### Development Session Columns (New Capability 3)

```sql
-- Additional columns on investigation_sessions for development sessions
ALTER TABLE investigation_sessions ADD COLUMN files_modified TEXT NOT NULL DEFAULT '[]';
ALTER TABLE investigation_sessions ADD COLUMN files_read TEXT NOT NULL DEFAULT '[]';
ALTER TABLE investigation_sessions ADD COLUMN linked_deploy_id INTEGER DEFAULT NULL;
```

### Test-Production Links (New Capability 5)

```sql
CREATE TABLE IF NOT EXISTS test_production_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    test_file TEXT NOT NULL,                    -- "spec/controllers/orders_controller_spec.rb"
    production_file TEXT NOT NULL,              -- "app/controllers/orders_controller.rb"
    production_endpoint TEXT DEFAULT NULL,      -- "/api/orders"
    production_function TEXT DEFAULT NULL,      -- "OrdersController#index"

    -- Production impact of the code path this test covers
    error_fingerprints TEXT NOT NULL DEFAULT '[]',  -- errors in this code path
    error_count_30d INTEGER NOT NULL DEFAULT 0,
    investigation_count INTEGER NOT NULL DEFAULT 0,

    -- Coverage assessment
    has_error_case_coverage INTEGER NOT NULL DEFAULT 0, -- does the test cover error scenarios?
    has_performance_coverage INTEGER NOT NULL DEFAULT 0, -- does the test assert on performance?

    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_test_prod_links_prod ON test_production_links(production_file);
```

### Uncovered Error Paths (New Capability 5)

```sql
CREATE TABLE IF NOT EXISTS uncovered_error_paths (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_path TEXT NOT NULL,
    function_name TEXT NOT NULL DEFAULT '',
    error_fingerprint TEXT NOT NULL,
    error_count_30d INTEGER NOT NULL DEFAULT 0,
    investigation_count INTEGER NOT NULL DEFAULT 0,
    impact_score REAL NOT NULL DEFAULT 0.0,     -- from ErrorImpact (users affected)
    suggested_test_description TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_uncovered_impact ON uncovered_error_paths(impact_score DESC);
```
