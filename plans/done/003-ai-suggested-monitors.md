# Plan 003: AI-Suggested Monitors via MCP

## Overview

Enable the AI (via MCP) to **deeply understand a user's database — schema, live activity, query patterns, and table health — then proactively suggest, create, and refine monitors**. This turns the MCP integration from passive (user asks for things) to intelligent (AI recommends actions based on real data).

**Effort**: Medium | **Impact**: High

---

## Current State

- `create_monitor` MCP tool exists — creates AI or rule monitors
- `preview_monitor` MCP tool exists — evaluates a rule config ad-hoc, returns `would_alert`, `summary`, `current_value`
- Connector's `db_schema` tool already introspects tables, columns, FKs, sample values, comments
- Connector's `db_search` tool runs read-only SQL queries
- `list_connectors` returns available data sources
- **No** `pg_stat_statements`, `pg_stat_activity`, or `pg_stat_user_tables` introspection
- **No** duplicate-awareness when suggesting monitors
- **No** schema change detection over time
- **No** monitor refinement based on alert history

---

## Goals

1. Postgres runtime introspection tools — query stats, live sessions, table health
2. AI-driven monitor suggestions based on real data, not just schema guessing
3. Duplicate-awareness — know what's already monitored before suggesting
4. Natural language monitor creation ("alert me if payments fail over $500")
5. Monitor refinement — suggest threshold adjustments for noisy/quiet monitors
6. Schema change detection — snapshot and diff over time
7. Multi-connector comparison — compare production vs staging
8. Preview → confirm → create flow for all suggestions

---

## Phase 1: Postgres Runtime Introspection Tools

The connector already has rich schema introspection (tables, columns, FKs, sample values, comments via `db_schema`). What's missing is **runtime data** — what the database is actually doing right now and historically.

### 1.1 New MCP Tool: `db_query_stats`

Exposes `pg_stat_statements` — the single most valuable view for understanding what to monitor.

```json
{
  "name": "db_query_stats",
  "description": "Get query performance statistics from pg_stat_statements. Shows the most expensive queries by total time, call count, or mean time. Use this to understand real workload patterns and suggest performance monitors.",
  "parameters": {
    "data_source_id": { "type": "string", "required": true },
    "order_by": { "type": "string", "enum": ["total_time", "calls", "mean_time", "rows"], "default": "total_time" },
    "limit": { "type": "number", "default": 20 }
  }
}
```

**Implementation** — `internal/mcp/tool_query_stats.go`:

```sql
SELECT
    queryid,
    LEFT(query, 200) as query,
    calls,
    ROUND(total_exec_time::numeric, 2) as total_time_ms,
    ROUND(mean_exec_time::numeric, 2) as mean_time_ms,
    ROUND(min_exec_time::numeric, 2) as min_time_ms,
    ROUND(max_exec_time::numeric, 2) as max_time_ms,
    ROUND(stddev_exec_time::numeric, 2) as stddev_time_ms,
    rows,
    shared_blks_hit,
    shared_blks_read
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT $1;
```

**What the AI can do with this:**

> "Your top query by total time is `SELECT * FROM orders WHERE user_id = ?` — it runs 12,000 times/hour and averages 45ms. That's 80% of your DB time. Want me to monitor if it exceeds 100ms?"

**Graceful fallback:** If `pg_stat_statements` extension isn't installed, return a clear message: "pg_stat_statements is not enabled. Run `CREATE EXTENSION pg_stat_statements;` to enable query performance tracking."

### 1.2 New MCP Tool: `db_activity`

Exposes `pg_stat_activity` — live snapshot of who's connected and what they're doing.

```json
{
  "name": "db_activity",
  "description": "Get a live snapshot of database connections and activity from pg_stat_activity. Shows active queries, idle connections, locked sessions, and long-running transactions. Use this to diagnose connection issues and suggest session monitors.",
  "parameters": {
    "data_source_id": { "type": "string", "required": true },
    "include_idle": { "type": "boolean", "default": false }
  }
}
```

**Implementation** — `internal/mcp/tool_activity.go`:

```sql
-- Summary
SELECT
    state,
    COUNT(*) as count,
    COALESCE(usename, '<system>') as usename,
    COALESCE(application_name, '') as application_name
FROM pg_stat_activity
WHERE pid != pg_backend_pid()
GROUP BY state, usename, application_name
ORDER BY count DESC;

-- Long-running queries (> 10s)
SELECT
    pid,
    usename,
    application_name,
    state,
    LEFT(query, 300) as query,
    EXTRACT(EPOCH FROM (now() - query_start))::int as running_seconds,
    wait_event_type,
    wait_event
FROM pg_stat_activity
WHERE state = 'active'
  AND query_start < now() - interval '10 seconds'
  AND pid != pg_backend_pid()
ORDER BY query_start ASC;

-- Idle in transaction (> 1min)
SELECT
    pid,
    usename,
    application_name,
    EXTRACT(EPOCH FROM (now() - xact_start))::int as idle_txn_seconds,
    LEFT(query, 200) as last_query
FROM pg_stat_activity
WHERE state = 'idle in transaction'
  AND xact_start < now() - interval '1 minute'
ORDER BY xact_start ASC;

-- Connection counts vs max
SHOW max_connections;
SELECT count(*) as current_connections FROM pg_stat_activity;
```

**Return format:**

```json
{
  "max_connections": 100,
  "current_connections": 87,
  "utilization_pct": 87,
  "by_state": {
    "active": 12,
    "idle": 60,
    "idle in transaction": 15
  },
  "by_application": {
    "web-api": 45,
    "worker": 30,
    "pgbouncer": 12
  },
  "long_running_queries": [...],
  "idle_in_transaction": [...],
  "warnings": [
    "Connection utilization at 87% (87/100)",
    "15 sessions idle in transaction",
    "1 query running for 47 minutes"
  ]
}
```

**What the AI can do with this:**

> "You have 87/100 connections in use. 15 are idle in transaction from `worker` — that's a connection leak. There's also a query running for 47 minutes. Want me to create monitors for connection saturation (> 80) and long-running queries (> 5 min)?"

### 1.3 New MCP Tool: `db_table_stats`

Exposes `pg_stat_user_tables` and `pg_statio_user_tables` — per-table health.

```json
{
  "name": "db_table_stats",
  "description": "Get table-level health statistics: dead tuples, last vacuum/analyze, sequential vs index scans, cache hit ratio. Use this to suggest maintenance and performance monitors.",
  "parameters": {
    "data_source_id": { "type": "string", "required": true },
    "table_name": { "type": "string", "description": "Optional: specific table. If omitted, returns stats for all tables sorted by size." }
  }
}
```

**Implementation** — `internal/mcp/tool_table_stats.go`:

```sql
SELECT
    s.relname as table_name,
    pg_total_relation_size(s.relid) as total_size_bytes,
    s.n_live_tup as live_tuples,
    s.n_dead_tup as dead_tuples,
    CASE WHEN s.n_live_tup > 0
         THEN ROUND(100.0 * s.n_dead_tup / s.n_live_tup, 1)
         ELSE 0 END as dead_pct,
    s.seq_scan,
    s.idx_scan,
    CASE WHEN (s.seq_scan + s.idx_scan) > 0
         THEN ROUND(100.0 * s.idx_scan / (s.seq_scan + s.idx_scan), 1)
         ELSE 0 END as idx_scan_pct,
    s.last_vacuum,
    s.last_autovacuum,
    s.last_analyze,
    s.last_autoanalyze,
    s.n_mod_since_analyze,
    -- Cache hit ratio
    COALESCE(
        ROUND(100.0 * io.heap_blks_hit / NULLIF(io.heap_blks_hit + io.heap_blks_read, 0), 1),
        100
    ) as cache_hit_pct
FROM pg_stat_user_tables s
LEFT JOIN pg_statio_user_tables io ON s.relid = io.relid
ORDER BY pg_total_relation_size(s.relid) DESC;
```

**Return format includes computed warnings:**

```json
{
  "tables": [...],
  "warnings": [
    "events: 230k dead tuples (15% of live rows) — needs vacuum",
    "events: last autovacuum was 12 days ago",
    "users: 95% sequential scans — likely missing an index",
    "sessions: cache hit ratio 78% — below 95% target"
  ]
}
```

**What the AI can do with this:**

> "Your `events` table hasn't been vacuumed in 12 days and has 230k dead tuples (15% of live). The `users` table has 95% sequential scans — it's probably missing an index on whatever column you filter by. Want me to set up monitors for both?"

### 1.4 Tests

For each tool (`tool_query_stats_test.go`, `tool_activity_test.go`, `tool_table_stats_test.go`):

- Mock connector returns expected pg_stat data
- Graceful handling when extension not installed (pg_stat_statements)
- Empty result sets → no errors, empty warnings
- Warning generation logic (thresholds for connection %, dead tuple %, etc.)
- `include_idle` flag correctly filters pg_stat_activity results

---

## Phase 2: Duplicate-Aware Suggestions

### 2.1 Problem

If the user already has a "Connection Saturation" monitor, the AI shouldn't suggest creating another one. Currently there's no mechanism for this — the AI would need to manually call `list_monitors` and cross-reference.

### 2.2 Solution: Inject Existing Monitors into Context

When the AI calls any introspection tool (`db_query_stats`, `db_activity`, `db_table_stats`), the response **automatically includes** a summary of existing monitors that overlap:

```json
{
  "table_stats": { ... },
  "warnings": [ ... ],
  "existing_monitors": [
    {
      "id": "abc-123",
      "title": "Connection Saturation",
      "monitor_type": "rule",
      "rule_summary": "pg_stat_activity count > 80",
      "status": "active",
      "last_alert": "2025-02-09T15:00:00Z"
    }
  ]
}
```

This way the AI naturally avoids duplicates — it sees "Connection Saturation is already monitored" and skips that suggestion.

### 2.3 Implementation

In each introspection tool handler, if `WatcherStore` is available in deps:

```go
monitors, _ := deps.WatcherStore.List(ctx, store.ListWatcherParams{})
// Include as "existing_monitors" in response
```

Lightweight — no new endpoints, just enriched responses.

---

## Phase 3: Natural Language Monitor Creation

### 3.1 Concept

Let the user describe what they want in plain English:

```
> Alert me if any payment fails for more than $500
> Tell me when the orders table grows past 10 million rows
> Watch for queries slower than 2 seconds on the users table
> Notify me if signups drop below 10 per hour
```

The AI translates this into a full rule config, previews it, and creates it.

### 3.2 Implementation: Better Tool Descriptions

This is mostly free — it's about guiding the LLM through tool descriptions. Update `create_monitor` description:

```
Create a new monitor. You can translate natural language requests into monitors:

1. Parse the user's intent into a SQL query + threshold
2. Call preview_monitor to validate the query works and show current values
3. Ask the user to confirm before creating
4. Set smart defaults for severity, interval, and notifications

Examples:
- "Alert me if payments fail" → query: SELECT count(*) FROM payments WHERE status='failed' AND created_at > now() - interval '15 min', operator: gt, threshold: 5
- "Watch for slow queries" → query: SELECT count(*) FROM pg_stat_activity WHERE state='active' AND now()-query_start > '30s', operator: gt, threshold: 3
```

### 3.3 Schema-Aware Translation

When translating natural language, the AI should:

1. Call `db_schema` (connector tool) to verify table/column names exist
2. Use column types to build correct SQL (e.g., timestamp comparisons)
3. Use sample values to understand enum columns (e.g., `status` can be 'pending', 'failed', 'completed')
4. Preview before creating to catch SQL errors

**Example flow:**

```
User: "alert me if any payment fails for more than $500"

AI thinks:
1. db_schema → payments table has: id, user_id, amount (numeric), status (text), created_at
2. Sample values for status: ['completed', 'failed', 'pending', 'refunded']
3. Build: SELECT count(*) FROM payments WHERE status='failed' AND amount > 500 AND created_at > now() - interval '15 min'
4. preview_monitor → would_alert: false, current_value: 0
5. Present to user: "I'll monitor for failed payments over $500 in the last 15 minutes. Currently none. Check every 5 minutes, warning severity. Create it?"
```

---

## Phase 4: Enhanced Preview → Confirm → Create Flow

### 4.1 Enhanced `preview_monitor` Response

Current response:
```json
{"would_alert": true, "summary": "...", "query_time_ms": 45, "current_value": 12}
```

Enhanced response:
```json
{
  "would_alert": true,
  "summary": "5 stuck orders detected (threshold: 0)",
  "current_value": 5,
  "threshold": 0,
  "operator": "gt",
  "query_time_ms": 45,
  "query_result_sample": [
    {"id": "abc-123", "status": "pending", "created_at": "2025-01-15T10:00:00Z"}
  ],
  "recommendation": "This monitor would fire immediately. Consider adjusting the threshold if this count is normal."
}
```

### 4.2 Batch Preview

New MCP tool: `preview_monitors` (plural) — preview multiple rule configs in one call:

```json
{
  "name": "preview_monitors",
  "parameters": {
    "monitors": [
      {"rule_config": "...", "data_source_id": "..."},
      {"rule_config": "...", "data_source_id": "..."}
    ]
  }
}
```

Returns array of preview results. Useful when suggesting 5+ monitors at once.

### 4.3 Create with Confirmation

The flow from the AI's perspective:

1. **Suggest** — present monitor ideas based on introspection data
2. **Preview** — user says "try it" → run preview_monitor, show current state + sample rows
3. **Confirm** — user says "looks good, create it" → run create_monitor
4. **Verify** — after creation, confirm monitor is active and next run time

---

## Phase 5: Monitor Refinement

### 5.1 Problem

A monitor fires 50 times in 24 hours — that's noisy. A monitor hasn't fired in 3 months — is it still useful? The user has no easy way to know without manually reviewing run history.

### 5.2 New MCP Tool: `monitor_health`

```json
{
  "name": "monitor_health",
  "description": "Analyze how well existing monitors are performing. Identifies noisy monitors (too many alerts), silent monitors (never fires), and suggests threshold adjustments based on observed values.",
  "parameters": {
    "monitor_id": { "type": "string", "description": "Optional: analyze a specific monitor. If omitted, analyzes all active monitors." },
    "period": { "type": "string", "default": "7d", "description": "Analysis period: 1d, 7d, 30d" }
  }
}
```

**Implementation** — `internal/mcp/tool_monitor_health.go`:

Query run history and alert history for the period, then compute:

```json
{
  "monitors": [
    {
      "id": "abc-123",
      "title": "Stuck Orders",
      "runs_in_period": 2016,
      "alerts_in_period": 487,
      "alert_rate_pct": 24.2,
      "assessment": "noisy",
      "current_threshold": 0,
      "observed_values": {
        "min": 0,
        "max": 47,
        "median": 3,
        "p95": 12,
        "p99": 28
      },
      "suggestion": "Threshold is 0 but median value is 3. Consider raising threshold to 10 (p95) to reduce noise by ~95%."
    },
    {
      "id": "def-456",
      "title": "Failed Auth Attempts",
      "runs_in_period": 2016,
      "alerts_in_period": 0,
      "alert_rate_pct": 0,
      "assessment": "silent",
      "observed_values": { "min": 0, "max": 0, "median": 0 },
      "suggestion": "This monitor has never fired. The condition may be too strict, or the scenario doesn't occur. Consider lowering the threshold or removing the monitor."
    }
  ],
  "summary": "1 noisy monitor (>20% alert rate), 1 silent monitor (0 alerts in 7d), 6 healthy monitors"
}
```

### 5.3 Assessment Categories

| Category | Criteria | AI Action |
|----------|----------|-----------|
| **healthy** | Alert rate 1-20%, fires meaningfully | No change needed |
| **noisy** | Alert rate > 20% | Suggest raising threshold (use p95 of observed values) |
| **silent** | 0 alerts in period | Suggest lowering threshold or reviewing relevance |
| **flapping** | Alternates alert/clean rapidly | Suggest adding cooldown or raising threshold |
| **erroring** | > 5% of runs fail | Suggest fixing the query or connection |

### 5.4 Store Additions

Add to `WatcherRunStore`:

```go
// Get observed values for a monitor over a time period
GetRunValues(ctx context.Context, watcherID uuid.UUID, since time.Time) ([]float64, error)
```

This queries the `details` JSON from run records to extract the numeric value that was evaluated. Used to compute min/max/median/p95/p99.

### 5.5 Conversation Flow

```
User: "Any of my monitors need tuning?"

AI calls monitor_health → gets analysis

AI: "Two things I'd suggest:

1. **Stuck Orders** is noisy — it fired 487 times this week (24% of runs).
   Your median stuck order count is 3, but threshold is 0.
   If I raise the threshold to 10, you'd only get alerted when it's actually
   unusual. Want me to update it?

2. **Failed Auth Attempts** hasn't fired once in 7 days.
   Either your threshold is too high, or this just doesn't happen.
   Want to lower the threshold, or remove the monitor?"

User: "Update stuck orders to 10, remove the auth one"

AI calls update on stuck orders, delete on auth monitor → done.
```

---

## Phase 6: Schema Change Detection

### 6.1 Concept

Take a schema snapshot now, compare it later. Detect new tables, dropped tables, new columns, type changes, new/dropped indexes.

### 6.2 Schema Snapshot Storage

New SQLite table:

```sql
-- Migration: 000006_schema_snapshots.sql
CREATE TABLE IF NOT EXISTS schema_snapshots (
    id TEXT PRIMARY KEY,
    data_source_id TEXT NOT NULL,
    schema_name TEXT NOT NULL DEFAULT 'public',
    snapshot TEXT NOT NULL,  -- JSON: full schema structure
    table_count INTEGER NOT NULL,
    total_size_bytes INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_schema_snapshots_ds ON schema_snapshots(data_source_id, created_at DESC);
```

### 6.3 New MCP Tool: `db_schema_diff`

```json
{
  "name": "db_schema_diff",
  "description": "Compare the current database schema against a previous snapshot. Detects new/dropped tables, new/dropped/modified columns, and index changes. Automatically takes a new snapshot for future comparisons. Use this to discover schema changes and suggest monitors for new tables.",
  "parameters": {
    "data_source_id": { "type": "string", "required": true },
    "schema": { "type": "string", "default": "public" }
  }
}
```

**Implementation** — `internal/mcp/tool_schema_diff.go`:

1. Fetch current schema via connector's `db_schema` tool
2. Load most recent snapshot for this data_source_id from `schema_snapshots`
3. If no previous snapshot exists, save current as first snapshot, return "First snapshot saved — I'll detect changes next time."
4. Diff the two snapshots:

```json
{
  "previous_snapshot": "2025-02-03T10:00:00Z",
  "changes": {
    "new_tables": [
      {
        "name": "referrals",
        "columns": ["id", "user_id", "referred_user_id", "status", "created_at"],
        "suggestion": "New table with 'status' column — consider a stuck referrals monitor"
      }
    ],
    "dropped_tables": [],
    "modified_tables": [
      {
        "name": "orders",
        "new_columns": ["discount_code", "shipping_method"],
        "dropped_columns": [],
        "type_changes": []
      }
    ],
    "new_indexes": [
      {"table": "orders", "name": "idx_orders_discount", "definition": "..."}
    ],
    "dropped_indexes": [],
    "size_changes": [
      {"table": "events", "previous_mb": 1800, "current_mb": 2100, "growth_pct": 16.7}
    ]
  },
  "summary": "1 new table (referrals), 2 new columns on orders, events grew 17%"
}
```

5. Save current schema as new snapshot

### 6.4 Auto-Snapshot on Connect

When a data source is first connected (or reconnected), automatically take a snapshot. This ensures there's always a baseline.

### 6.5 Conversation Flow

```
User: "Anything changed in my database since last week?"

AI calls db_schema_diff("prod-ds-id")

AI: "Since your last snapshot (Feb 3):

- **New table: `referrals`** — has `user_id`, `referred_user_id`, `status`, `created_at`.
  Want me to monitor for stuck referrals?
- **`orders` table** gained 2 columns: `discount_code` and `shipping_method`
- **`events` table** grew 17% (1.8GB → 2.1GB)

The events growth is worth watching — at this rate it'll double in ~6 weeks."
```

---

## Phase 7: Multi-Connector Comparison

### 7.1 Concept

If the user has multiple data sources connected (production, staging, dev), the AI can compare them to surface anomalies.

### 7.2 Implementation: No New Tools Needed

This is purely an AI reasoning pattern. The AI calls existing tools on multiple connectors and compares:

```
1. list_connectors → finds "production" and "staging"
2. db_activity("production") → 87 connections
3. db_activity("staging") → 12 connections
4. db_table_stats("production") → events table 2.1GB
5. db_table_stats("staging") → events table 200MB
```

### 7.3 System Prompt Guidance

Add to tool descriptions:

```
When the user has multiple data sources connected, compare key metrics across environments:
- Connection counts: production should be higher but proportional
- Table sizes: order-of-magnitude differences may indicate data retention issues
- Dead tuples: similar ratios expected across environments
- Schema: production and staging should match (use db_schema_diff on each)

Flag significant discrepancies as potential issues.
```

### 7.4 Conversation Flow

```
User: "Compare my production and staging databases"

AI: "Here's a comparison:

| Metric | Production | Staging | Notes |
|--------|-----------|---------|-------|
| Connections | 87/100 (87%) | 12/50 (24%) | Prod is high |
| Tables | 24 | 22 | Staging missing: referrals, audit_log |
| Total size | 512MB | 48MB | Expected |
| Dead tuples | 230k (events) | 1.2k (events) | Prod needs vacuum |
| Cache hit | 91% | 99% | Prod below 95% target |

Two concerns:
1. Production is missing 2 tables that exist in staging — `referrals` and `audit_log` may not have been migrated
2. Production cache hit ratio is low at 91%

Wait — I got that backwards. Staging is missing those tables, not production. That's normal if those features haven't been deployed to staging yet."
```

---

## Phase 8: Smart Defaults

### 8.1 Threshold Intelligence

When creating monitors from suggestions, the AI should set smart defaults based on actual data:

- **Connection count**: threshold = 80% of `max_connections` (query `SHOW max_connections`)
- **Table size**: threshold = 2x current size (gives room before alerting)
- **Replication lag**: 60 seconds for warning, 300 for critical
- **Dead tuples**: 0.1% of estimated live rows
- **Query time**: 2x the current p95 from `pg_stat_statements`
- **Stuck records**: p95 of observed values (from `db_activity`), not 0

### 8.2 Interval Intelligence

- System health monitors (connections, replication): 5m
- Performance monitors (slow queries, cache ratio): 15m
- Capacity monitors (table size, database size): 1h
- Application monitors (stuck records, failed payments): 5m

---

## File Changes Summary

| File | Change |
|------|--------|
| `internal/mcp/tool_query_stats.go` | New — pg_stat_statements introspection |
| `internal/mcp/tool_query_stats_test.go` | New — tests |
| `internal/mcp/tool_activity.go` | New — pg_stat_activity live snapshot |
| `internal/mcp/tool_activity_test.go` | New — tests |
| `internal/mcp/tool_table_stats.go` | New — pg_stat_user_tables health stats |
| `internal/mcp/tool_table_stats_test.go` | New — tests |
| `internal/mcp/tool_monitor_health.go` | New — monitor refinement analysis |
| `internal/mcp/tool_monitor_health_test.go` | New — tests |
| `internal/mcp/tool_schema_diff.go` | New — schema snapshot + diff |
| `internal/mcp/tool_schema_diff_test.go` | New — tests |
| `internal/mcp/server.go` | Register all new tools, update preview_monitor response, add preview_monitors, enrich responses with existing_monitors |
| `internal/mcp/server_test.go` | Update tests |
| `internal/store/store.go` | Add SchemaSnapshotStore interface, GetRunValues on WatcherRunStore |
| `internal/store/sqlite_schema_snapshot.go` | New — SQLite implementation |
| `internal/store/sqlite_schema_snapshot_test.go` | New — tests |
| `internal/store/sqlite_watcher_run.go` | Add GetRunValues method |
| `internal/store/sqlite_migrations/000006_schema_snapshots.sql` | New — migration |

---

## Implementation Order

| Phase | What | Depends On | Effort |
|-------|------|-----------|--------|
| 1.1 | `db_query_stats` | None | Low |
| 1.2 | `db_activity` | None | Low |
| 1.3 | `db_table_stats` | None | Low |
| 2 | Duplicate-aware suggestions | None (uses existing list_monitors) | Trivial |
| 3 | Natural language creation | Phase 1 (for schema-aware translation) | Low (tool descriptions only) |
| 4 | Enhanced preview flow | None | Low |
| 5 | Monitor refinement | Needs run history data | Medium |
| 6 | Schema change detection | Needs new migration | Medium |
| 7 | Multi-connector comparison | Phases 1.1-1.3 | Low (AI reasoning, no new code) |
| 8 | Smart defaults | Phases 1.1-1.3 | Low (AI reasoning, no new code) |

Phases 1.1, 1.2, 1.3 can be built in parallel. Phases 2, 3, 7, 8 are mostly free (tool description improvements + AI reasoning). Phases 5 and 6 require new storage.

---

## Out of Scope

- Web UI for "suggest monitors" (MCP-only for now)
- Auto-creating monitors without user confirmation
- MySQL / other database schema introspection (Postgres only for now)
- Machine learning-based anomaly detection
- Automatic remediation actions (kill queries, run VACUUM, etc.)
- Cost estimation for monitors (LLM token usage for AI monitors)
