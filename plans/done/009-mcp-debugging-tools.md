# Plan 009: MCP Debugging & Diagnostic Tools

## Overview

Expand the OpenTrace MCP tool surface to give AI agents (Claude Code, Claude Desktop, etc.) the capabilities needed to **deeply debug, investigate, and proactively assist** users with their database and infrastructure issues.

Currently the MCP server exposes discovery tools (`db_query_stats`, `db_table_stats`, `db_activity`), monitoring tools (`list_monitors`, `list_alerts`, `get_digest`), and basic data access (`db_search`, `log_search`, `db_schema`). These cover the "what" but leave gaps in the "why" and "what changed?" — the two most important questions during debugging.

**Effort**: Large (10 tools across 6 phases) | **Impact**: High

---

## Current MCP Tool Inventory

| Tool | Category | Purpose |
|------|----------|---------|
| `list_connectors` | Discovery | List available data sources |
| `db_search` | Data Access | Run read-only SQL |
| `db_schema` | Data Access | Inspect table structures |
| `log_search` | Data Access | Search ingested logs |
| `db_query_stats` | Diagnostics | Top queries from pg_stat_statements |
| `db_table_stats` | Diagnostics | Table-level stats (tuples, scans, vacuum) |
| `db_activity` | Diagnostics | Current connections and running queries |
| `list_monitors` | Monitoring | List configured monitors |
| `list_alerts` | Monitoring | List recent alerts |
| `get_digest` | Monitoring | Health digest summary |
| `create_monitor` | Management | Create new monitors |
| `preview_monitor` | Management | Test rule configs before saving |
| `list_servers` | Infrastructure | List monitored servers |
| `query_metrics` | Infrastructure | Server time-series metrics |
| `server_health` | Infrastructure | Latest server health snapshot |

### Gaps Identified

1. **No query plan analysis** — can't answer "why is this query slow?"
2. **No log aggregation** — can't answer "what are the most common errors?"
3. **No alert investigation** — can't see triggering data or correlate alerts
4. **No monitor run history** — can't debug why a monitor is misbehaving
5. **No index analysis** — can't recommend index improvements
6. **No lock debugging** — can't diagnose deadlocks or blocked queries
7. **No trace correlation** — can't follow a request across services
8. **No before/after comparison** — can't answer "what changed?"
9. **No proactive recommendations** — introspection hints exist but no dedicated tool
10. **No connection pool visibility** — can't diagnose pool exhaustion

---

## Implementation Phases

### Phase 1: Query Debugging (P0)
- `explain_query` — Query plan analysis
- `db_locks` — Lock contention debugging

### Phase 2: Monitor Investigation (P0)
- `monitor_run_history` — Run details & failure analysis
- `alert_details` — Deep alert investigation

### Phase 3: Log Intelligence (P0)
- `log_stats` — Log aggregation & pattern detection

### Phase 4: Performance Analysis (P1)
- `db_index_analysis` — Index health & recommendations
- `compare_periods` — Before/after metric comparison

### Phase 5: Distributed Debugging (P2)
- `trace_lookup` — Distributed trace correlation

### Phase 6: Proactive Intelligence (P2)
- `suggest_monitors` — AI-powered monitor recommendations
- `connection_pool_stats` — Connection pool health

---

## Phase 1: Query Debugging

### 1.1 `explain_query` — Query Plan Analysis

**Why**: When a user reports a slow query, the AI currently has no way to inspect *why*. `EXPLAIN ANALYZE` output is essential for identifying missing indexes, bad join strategies, and inaccurate row estimates.

**File**: `internal/mcp/tool_explain_query.go`

#### Tool Definition

```json
{
  "name": "explain_query",
  "description": "Run EXPLAIN ANALYZE on a SQL query to show the execution plan, actual vs estimated rows, and timing. Use this when investigating slow queries identified by db_query_stats or reported by the user. The query runs in a read-only transaction and is automatically rolled back.",
  "parameters": {
    "query": {
      "type": "string",
      "description": "The SQL SELECT query to analyze",
      "required": true
    },
    "connector_id": {
      "type": "string",
      "description": "Data source connector ID (omit to use the first available connector)"
    },
    "format": {
      "type": "string",
      "description": "Output format: 'text' (default, human-readable tree) or 'json' (structured, includes all node details)",
      "default": "text"
    },
    "analyze": {
      "type": "boolean",
      "description": "Actually execute the query to get real timing and row counts (default: true). Set to false for EXPLAIN without ANALYZE on potentially expensive queries.",
      "default": true
    },
    "buffers": {
      "type": "boolean",
      "description": "Include buffer usage statistics (shared/local hit/read/write). Requires analyze=true.",
      "default": true
    }
  }
}
```

#### Response Format

```json
{
  "query": "SELECT * FROM orders WHERE customer_id = 123",
  "plan": "... EXPLAIN output (text or JSON) ...",
  "execution_time_ms": 45.2,
  "planning_time_ms": 0.8,
  "warnings": [
    "Sequential scan on 'orders' (1.2M rows) — consider adding an index on customer_id",
    "Estimated rows (1) vs actual rows (847) — statistics may be stale, consider running ANALYZE"
  ],
  "suggestions": [
    "CREATE INDEX CONCURRENTLY idx_orders_customer_id ON orders(customer_id)"
  ]
}
```

#### Implementation Notes

- **Read-only safety**: Wrap in `BEGIN READ ONLY; EXPLAIN ANALYZE ...; ROLLBACK;` to prevent side effects even if guardrail is bypassed.
- **Guardrail bypass**: `explain_query` must accept SELECT statements only (reuse `guardrail.ValidateReadOnly()`), but needs to prepend `EXPLAIN ANALYZE` before sending to Postgres.
- **Warning extraction**: Parse the JSON plan output to detect:
  - Sequential scans on large tables (>10k rows)
  - Large row estimate mismatches (actual/estimated ratio > 10x)
  - Nested loops with high row counts
  - Sort operations spilling to disk
- **Timeout**: Use the connector's existing `statement_timeout` — no special handling needed.
- **Admin only**: This is a write-category tool (it executes queries).

#### Store Changes

None — uses existing `DatabaseConnector` pool directly.

#### Tests — `internal/mcp/tool_explain_query_test.go`

- Valid SELECT → returns plan text
- Non-SELECT rejected → error
- `analyze=false` → EXPLAIN without ANALYZE (no timing)
- `format=json` → returns structured JSON plan
- Warning detection: seq scan on large table triggers warning
- Warning detection: row estimate mismatch triggers warning
- Invalid SQL → returns Postgres error message

---

### 1.2 `db_locks` — Lock Contention Debugging

**Why**: `db_activity` shows connections but not *why* queries are stuck. Lock inspection reveals blocking chains, lock types, and waiting queries — critical for debugging production deadlocks and contention.

**File**: `internal/mcp/tool_locks.go`

#### Tool Definition

```json
{
  "name": "db_locks",
  "description": "Show current lock contention in the database: blocking chains, lock types, and waiting queries. Use when db_activity shows long-running or idle-in-transaction sessions, or when users report 'the database is stuck'. Identifies which queries are blocking which.",
  "parameters": {
    "connector_id": {
      "type": "string",
      "description": "Data source connector ID (omit to use the first available)"
    },
    "blocking_only": {
      "type": "boolean",
      "description": "Only show lock chains where one query is blocking another (default: true). Set to false to see all held locks.",
      "default": true
    }
  }
}
```

#### Response Format

```json
{
  "blocking_chains": [
    {
      "blocker": {
        "pid": 1234,
        "query": "UPDATE accounts SET balance = ...",
        "state": "idle in transaction",
        "duration_seconds": 120,
        "application_name": "web-api",
        "lock_type": "RowExclusiveLock",
        "relation": "accounts"
      },
      "waiting": [
        {
          "pid": 5678,
          "query": "UPDATE accounts SET balance = ...",
          "state": "active",
          "wait_duration_seconds": 45,
          "application_name": "worker",
          "lock_type": "RowExclusiveLock",
          "relation": "accounts"
        }
      ]
    }
  ],
  "summary": {
    "total_blocking_chains": 1,
    "total_waiting_queries": 1,
    "longest_wait_seconds": 45
  },
  "warnings": [
    "PID 1234 has been idle in transaction for 2 minutes while blocking 1 query"
  ]
}
```

#### Implementation Notes

- **Query**: Use `pg_locks` joined with `pg_stat_activity` to find blocking/waiting pairs:
  ```sql
  SELECT
    blocked_locks.pid AS blocked_pid,
    blocked_activity.usename AS blocked_user,
    blocking_locks.pid AS blocking_pid,
    blocking_activity.usename AS blocking_user,
    blocked_activity.query AS blocked_query,
    blocking_activity.query AS blocking_query,
    blocked_locks.locktype,
    blocked_activity.wait_event_type,
    blocked_activity.state AS blocked_state,
    blocking_activity.state AS blocking_state,
    now() - blocked_activity.query_start AS blocked_duration,
    now() - blocking_activity.query_start AS blocking_duration
  FROM pg_catalog.pg_locks blocked_locks
  JOIN pg_catalog.pg_stat_activity blocked_activity
    ON blocked_activity.pid = blocked_locks.pid
  JOIN pg_catalog.pg_locks blocking_locks
    ON blocking_locks.locktype = blocked_locks.locktype
    AND blocking_locks.database IS NOT DISTINCT FROM blocked_locks.database
    AND blocking_locks.relation IS NOT DISTINCT FROM blocked_locks.relation
    AND blocking_locks.page IS NOT DISTINCT FROM blocked_locks.page
    AND blocking_locks.tuple IS NOT DISTINCT FROM blocked_locks.tuple
    AND blocking_locks.virtualxid IS NOT DISTINCT FROM blocked_locks.virtualxid
    AND blocking_locks.transactionid IS NOT DISTINCT FROM blocked_locks.transactionid
    AND blocking_locks.classid IS NOT DISTINCT FROM blocked_locks.classid
    AND blocking_locks.objid IS NOT DISTINCT FROM blocked_locks.objid
    AND blocking_locks.objsubid IS NOT DISTINCT FROM blocked_locks.objsubid
    AND blocking_locks.pid != blocked_locks.pid
  JOIN pg_catalog.pg_stat_activity blocking_activity
    ON blocking_activity.pid = blocking_locks.pid
  WHERE NOT blocked_locks.granted;
  ```
- **`blocking_only=false`**: Also query all held locks grouped by relation and lock mode.
- **Warning generation**: Flag idle-in-transaction blockers, long wait chains (>3 deep), and AccessExclusiveLock on large tables.
- **Read-only**: This is a read-only query against system catalogs.

#### Tests — `internal/mcp/tool_locks_test.go`

- No locks → empty chains, summary zeros
- `blocking_only=true` filters non-blocking locks
- `blocking_only=false` returns all held locks
- Mock: Use mock connector that returns predefined `pg_locks` data

---

## Phase 2: Monitor Investigation

### 2.1 `monitor_run_history` — Run Details & Failure Analysis

**Why**: When a monitor is misbehaving (false positives, missed alerts, errors), the AI needs to see recent run results — what data was analyzed, what the AI/rule concluded, timing, and error messages.

**File**: `internal/mcp/tool_run_history.go`

#### Tool Definition

```json
{
  "name": "monitor_run_history",
  "description": "Show recent execution history for a monitor: run status, duration, summary, errors, and whether each run triggered an alert. Use when investigating why a monitor is firing too often, missing issues, or showing errors.",
  "parameters": {
    "monitor_id": {
      "type": "string",
      "description": "Monitor UUID (from list_monitors)",
      "required": true
    },
    "limit": {
      "type": "number",
      "description": "Maximum runs to return (default: 20, max: 100)",
      "default": 20
    },
    "status_filter": {
      "type": "string",
      "description": "Filter by run status: 'all' (default), 'completed', 'failed', 'error', 'alerted' (completed runs that triggered alerts)"
    }
  }
}
```

#### Response Format

```json
{
  "monitor": {
    "id": "uuid",
    "title": "Connection Saturation",
    "type": "rule",
    "status": "active",
    "schedule": "*/5 * * * *",
    "time_range": "5m"
  },
  "runs": [
    {
      "id": "uuid",
      "started_at": "2025-02-10T08:00:00Z",
      "completed_at": "2025-02-10T08:00:02Z",
      "duration_ms": 2100,
      "status": "completed",
      "has_alert": true,
      "summary": "92 of 100 connections in use — threshold exceeded (>80)",
      "details": { "current_value": 92, "threshold": 80, "operator": "gt" },
      "error": null
    },
    {
      "id": "uuid",
      "started_at": "2025-02-10T07:55:00Z",
      "completed_at": "2025-02-10T07:55:01Z",
      "duration_ms": 1200,
      "status": "completed",
      "has_alert": false,
      "summary": "65 of 100 connections in use — within threshold",
      "details": { "current_value": 65, "threshold": 80, "operator": "gt" },
      "error": null
    }
  ],
  "stats": {
    "total_runs": 288,
    "completed": 280,
    "failed": 5,
    "errors": 3,
    "alert_rate_pct": 12.5,
    "avg_duration_ms": 1800,
    "period_covered": "last 24 hours"
  }
}
```

#### Implementation Notes

- Uses existing `WatcherRunStore.List()` for run data.
- Uses `WatcherStore.GetByID()` for monitor metadata.
- `status_filter=alerted`: Filter completed runs where `has_alert=true`.
- `stats`: Aggregate from `WatcherRunStore.CountRuns()` with different status filters.
- **Read-only tool** — available to all users.

#### Store Changes

Add to `WatcherRunStore`:

```go
// ListWithFilter extends List with status filtering
ListWithFilter(ctx context.Context, watcherID uuid.UUID, limit int, status string) ([]WatcherRun, error)
```

The `status` parameter accepts: `""` (all), `"completed"`, `"failed"`, `"error"`.
For `"alerted"`, the handler filters completed runs where `has_alert=true` in application code (or adds `AND has_alert = 1` to the query).

#### Tests — `internal/mcp/tool_run_history_test.go`

- Valid monitor ID → returns runs + stats
- Invalid monitor ID → `store.ErrNotFound` → MCP error
- `status_filter=failed` → only failed runs returned
- `status_filter=alerted` → only runs with alerts
- Empty run history → empty runs array, zero stats
- Stats calculation: alert_rate_pct = (alerted / completed) * 100

---

### 2.2 `alert_details` — Deep Alert Investigation

**Why**: `list_alerts` gives summaries, but debugging requires the full picture: the monitor config that triggered it, the actual log/query data that matched, the run that produced it, and temporally correlated alerts.

**File**: `internal/mcp/tool_alert_details.go`

#### Tool Definition

```json
{
  "name": "alert_details",
  "description": "Get full details for a specific alert: the triggering monitor configuration, the run that produced it (including raw data analyzed), and correlated alerts from the same time window. Use when investigating a specific alert from list_alerts.",
  "parameters": {
    "alert_id": {
      "type": "string",
      "description": "Alert UUID (from list_alerts)",
      "required": true
    },
    "include_correlated": {
      "type": "boolean",
      "description": "Include other alerts that fired within +/- 5 minutes (default: true)",
      "default": true
    }
  }
}
```

#### Response Format

```json
{
  "alert": {
    "id": "uuid",
    "severity": "critical",
    "summary": "92 of 100 connections in use",
    "created_at": "2025-02-10T03:15:00Z",
    "read": false,
    "dismissed": false
  },
  "monitor": {
    "id": "uuid",
    "title": "Connection Saturation",
    "type": "rule",
    "rule_config": { "source": "query", "query": "SELECT count(*) FROM pg_stat_activity", "operator": "gt", "threshold": 80 },
    "description": null,
    "environment": "production",
    "schedule": "*/5 * * * *",
    "time_range": "5m"
  },
  "triggering_run": {
    "id": "uuid",
    "started_at": "2025-02-10T03:14:58Z",
    "completed_at": "2025-02-10T03:15:00Z",
    "duration_ms": 2100,
    "summary": "92 of 100 connections in use — threshold exceeded",
    "details": { "current_value": 92, "threshold": 80, "rows": [...] }
  },
  "correlated_alerts": [
    {
      "id": "uuid2",
      "monitor_title": "Slow Query Spike",
      "severity": "warning",
      "summary": "15 queries running > 30s",
      "created_at": "2025-02-10T03:14:30Z",
      "time_offset_seconds": -30
    }
  ],
  "context": {
    "alert_count_last_hour": 5,
    "same_monitor_alerts_24h": 12,
    "first_occurrence": "2025-02-10T01:15:00Z"
  }
}
```

#### Implementation Notes

- **Alert lookup**: `AlertStore` needs a `GetByID` method (add if missing).
- **Run lookup**: Alerts should store `run_id` — check if the `alerts` table has this column. If not, add migration.
- **Correlated alerts**: Query `AlertStore.List()` with time window `[alert.created_at - 5min, alert.created_at + 5min]`, exclude current alert.
- **Context**: Count alerts from same monitor in last 24h, find earliest alert in the current "burst" (consecutive alerts with <30min gaps).
- **Read-only tool** — available to all users.

#### Store Changes

Add to `AlertStore` (if not already present):

```go
GetByID(ctx context.Context, id uuid.UUID) (*Alert, error)
```

Add `run_id` to alerts table if not present:

```sql
-- 000011_alert_run_id.up.sql
ALTER TABLE alerts ADD COLUMN run_id TEXT;
CREATE INDEX idx_alerts_run_id ON alerts(run_id);
```

#### Tests — `internal/mcp/tool_alert_details_test.go`

- Valid alert ID → full response with monitor + run + correlated
- Invalid alert ID → `store.ErrNotFound` → MCP error
- Alert with no run_id → `triggering_run` is null (backward compat)
- `include_correlated=false` → no correlated alerts in response
- Correlated alerts from different monitors appear
- Context counts are accurate

---

## Phase 3: Log Intelligence

### 3.1 `log_stats` — Log Aggregation & Pattern Detection

**Why**: `log_search` returns individual log lines, but the AI can't answer "what are the most common errors?" or "show error rate trends." An aggregation tool transforms log search from "find a needle" to "understand the haystack."

**File**: `internal/mcp/tool_log_stats.go`

#### Tool Definition

```json
{
  "name": "log_stats",
  "description": "Aggregate log statistics: volume by level/service, error rate trends, and most common error patterns. Use when investigating 'what's going wrong?', 'are errors increasing?', or 'which service has the most issues?'. Unlike log_search which returns individual entries, this returns aggregated counts and patterns.",
  "parameters": {
    "time_range": {
      "type": "string",
      "description": "Lookback window: '15m', '1h' (default), '6h', '24h', '7d'",
      "default": "1h"
    },
    "group_by": {
      "type": "string",
      "description": "Primary grouping: 'level' (default), 'service', 'pattern' (clusters similar error messages)",
      "default": "level"
    },
    "service": {
      "type": "string",
      "description": "Filter to a specific service name"
    },
    "level": {
      "type": "string",
      "description": "Filter to a specific log level (debug, info, warn, error, fatal)"
    },
    "environment": {
      "type": "string",
      "description": "Filter to a specific environment"
    },
    "bucket_interval": {
      "type": "string",
      "description": "Time bucket size for trend data: '1m', '5m' (default), '15m', '1h'. Only used when group_by is 'level' or 'service'.",
      "default": "5m"
    }
  }
}
```

#### Response Format

**`group_by=level`** (default):

```json
{
  "time_range": { "start": "...", "end": "..." },
  "total_logs": 15420,
  "by_level": {
    "debug": 2100,
    "info": 11500,
    "warn": 1200,
    "error": 580,
    "fatal": 40
  },
  "error_rate_pct": 4.02,
  "trend": [
    { "bucket": "2025-02-10T07:00:00Z", "debug": 180, "info": 950, "warn": 100, "error": 45, "fatal": 3 },
    { "bucket": "2025-02-10T07:05:00Z", "debug": 175, "info": 960, "warn": 105, "error": 52, "fatal": 5 }
  ],
  "warnings": [
    "Error rate increased 40% in the last 15 minutes compared to the hour average"
  ]
}
```

**`group_by=service`**:

```json
{
  "time_range": { "start": "...", "end": "..." },
  "total_logs": 15420,
  "by_service": [
    { "service": "web-api", "total": 8500, "errors": 320, "error_rate_pct": 3.76 },
    { "service": "worker", "total": 4200, "errors": 210, "error_rate_pct": 5.00 },
    { "service": "scheduler", "total": 2720, "errors": 50, "error_rate_pct": 1.84 }
  ],
  "warnings": [
    "Service 'worker' error rate (5.0%) is above average (4.0%)"
  ]
}
```

**`group_by=pattern`** (error pattern clustering):

```json
{
  "time_range": { "start": "...", "end": "..." },
  "total_errors": 580,
  "patterns": [
    {
      "pattern": "connection refused to *:5432",
      "count": 245,
      "pct_of_errors": 42.2,
      "first_seen": "2025-02-10T06:30:00Z",
      "last_seen": "2025-02-10T07:58:00Z",
      "sample_message": "connection refused to db-primary:5432",
      "services": ["web-api", "worker"]
    },
    {
      "pattern": "context deadline exceeded",
      "count": 180,
      "pct_of_errors": 31.0,
      "first_seen": "2025-02-10T07:10:00Z",
      "last_seen": "2025-02-10T07:59:00Z",
      "sample_message": "context deadline exceeded after 30s",
      "services": ["web-api"]
    }
  ],
  "warnings": [
    "Top error pattern 'connection refused' accounts for 42% of all errors — likely a database connectivity issue"
  ]
}
```

#### Implementation Notes

- **Data source**: Uses `LogStore.Search()` with time-range filters, then aggregates in Go.
- **Pattern clustering** (`group_by=pattern`):
  - Fetch error/fatal logs in the time range (up to 10k).
  - Normalize messages: strip UUIDs, IPs, numbers, timestamps, quoted strings.
  - Group by normalized message → count occurrences.
  - Return top N patterns (default 20).
  - This is a simple string-normalization approach, not ML-based.
- **Trend buckets**: Group logs by time bucket and count per level/service.
- **Warning generation**:
  - Error rate increasing: compare last bucket to average.
  - Single pattern dominance: >50% of errors from one pattern.
  - Silent service: service that usually has logs but has none in the period.
- **Performance**: For large log volumes, may need to add aggregate queries to `LogStore` rather than fetching all logs. Start with in-memory aggregation, optimize later if needed.

#### Store Changes

Add to `LogStore`:

```go
// CountByLevel returns log counts grouped by level within a time range.
CountByLevel(ctx context.Context, params LogCountParams) (map[string]int, error)

// CountByService returns log counts grouped by service within a time range.
CountByService(ctx context.Context, params LogCountParams) ([]ServiceLogCount, error)

type LogCountParams struct {
    Since       time.Time
    Until       time.Time
    Service     string // optional filter
    Level       string // optional filter
    Environment string // optional filter
}

type ServiceLogCount struct {
    Service    string
    Total      int
    ErrorCount int
}
```

These use SQL `COUNT(*) ... GROUP BY` queries on the `logs` table, which is much more efficient than fetching all logs and counting in Go.

For `group_by=pattern`, we still fetch individual error log messages (limited to 10k) and cluster in Go, since SQLite doesn't have built-in pattern clustering.

#### Tests — `internal/mcp/tool_log_stats_test.go`

- `group_by=level` → correct counts per level
- `group_by=service` → correct per-service stats with error rates
- `group_by=pattern` → clusters similar messages, strips UUIDs/IPs
- Time range filtering works correctly
- Service/level filters narrow results
- Trend buckets are correctly aligned
- Empty logs → zero counts, no errors
- Warning: error rate spike detected
- Warning: dominant pattern detected

---

## Phase 4: Performance Analysis

### 4.1 `db_index_analysis` — Index Health & Recommendations

**Why**: Complements `db_table_stats` and `db_query_stats`. Shows unused indexes (wasting write performance), missing indexes (causing sequential scans), duplicate indexes, and bloated indexes.

**File**: `internal/mcp/tool_index_analysis.go`

#### Tool Definition

```json
{
  "name": "db_index_analysis",
  "description": "Analyze database index health: find unused indexes (wasting disk/write overhead), missing indexes (tables with high sequential scan ratios), duplicate indexes, and bloated indexes. Use after db_table_stats shows sequential scans or db_query_stats shows slow queries.",
  "parameters": {
    "connector_id": {
      "type": "string",
      "description": "Data source connector ID (omit to use the first available)"
    },
    "table_name": {
      "type": "string",
      "description": "Analyze indexes for a specific table (omit for all tables)"
    },
    "include_suggestions": {
      "type": "boolean",
      "description": "Include CREATE INDEX suggestions based on sequential scan patterns (default: true)",
      "default": true
    }
  }
}
```

#### Response Format

```json
{
  "unused_indexes": [
    {
      "schema": "public",
      "table": "orders",
      "index_name": "idx_orders_legacy_status",
      "size_bytes": 524288000,
      "size_human": "500 MB",
      "index_scans": 0,
      "last_used": null,
      "suggestion": "DROP INDEX CONCURRENTLY idx_orders_legacy_status; -- saves 500 MB, 0 scans since stats reset"
    }
  ],
  "missing_indexes": [
    {
      "schema": "public",
      "table": "orders",
      "seq_scans": 145000,
      "seq_tup_read": 870000000,
      "idx_scans": 200,
      "live_tuples": 6000000,
      "suggestion": "Table 'orders' has 145K sequential scans vs 200 index scans — likely missing an index on commonly filtered columns"
    }
  ],
  "duplicate_indexes": [
    {
      "table": "users",
      "indexes": ["idx_users_email", "idx_users_email_unique"],
      "columns": ["email"],
      "suggestion": "Indexes idx_users_email and idx_users_email_unique cover the same column(s) — consider dropping one"
    }
  ],
  "bloated_indexes": [
    {
      "schema": "public",
      "table": "events",
      "index_name": "idx_events_created_at",
      "table_size_bytes": 1073741824,
      "index_size_bytes": 2147483648,
      "ratio": 2.0,
      "suggestion": "Index is 2x larger than table — consider REINDEX CONCURRENTLY"
    }
  ],
  "summary": {
    "total_indexes": 45,
    "unused": 3,
    "possibly_missing": 2,
    "duplicates": 1,
    "bloated": 1,
    "total_unused_size_human": "1.2 GB"
  }
}
```

#### Implementation Notes

- **Unused indexes**: Query `pg_stat_user_indexes` where `idx_scan = 0` and index is not a unique/PK constraint.
- **Missing indexes**: Query `pg_stat_user_tables` where `seq_scan >> idx_scan` and `n_live_tup > 10000`.
- **Duplicate indexes**: Compare index definitions from `pg_indexes` — same table, same or prefix-subset columns.
- **Bloated indexes**: Compare `pg_relation_size(indexrelid)` to `pg_relation_size(relid)` — indexes significantly larger than their table.
- **Suggestions**: Generate DDL (`CREATE INDEX CONCURRENTLY`, `DROP INDEX CONCURRENTLY`, `REINDEX CONCURRENTLY`).
- **Read-only**: All queries against system catalogs.

#### Tests — `internal/mcp/tool_index_analysis_test.go`

- Unused index detection (idx_scan = 0)
- Missing index detection (high seq_scan ratio)
- Duplicate index detection
- Bloated index detection
- Unique/PK indexes excluded from "unused" list
- `table_name` filter works
- `include_suggestions=false` omits suggestion fields
- Empty database → empty results, no errors

---

### 4.2 `compare_periods` — Before/After Analysis

**Why**: The most common debugging question is "what changed?" A comparison tool diffs error rates, query performance, or log volumes between two time windows.

**File**: `internal/mcp/tool_compare_periods.go`

#### Tool Definition

```json
{
  "name": "compare_periods",
  "description": "Compare metrics between two time periods to identify what changed. Compares error rates, log volumes, query performance, or alert counts between a 'current' period and a 'baseline' period. Use when the user asks 'what changed?', 'why is it slow now?', or 'is this worse than yesterday?'.",
  "parameters": {
    "metric": {
      "type": "string",
      "description": "What to compare: 'errors' (log error rates), 'log_volume' (total log counts by level), 'alerts' (alert counts by severity), 'query_performance' (from pg_stat_statements if available)",
      "required": true
    },
    "current_period": {
      "type": "string",
      "description": "Current period: 'last_1h' (default), 'last_6h', 'last_24h', 'today'",
      "default": "last_1h"
    },
    "baseline_period": {
      "type": "string",
      "description": "Baseline to compare against: 'previous' (default — same duration immediately before current), 'yesterday_same_time', 'last_week_same_time'",
      "default": "previous"
    },
    "service": {
      "type": "string",
      "description": "Filter to a specific service (for error/log_volume metrics)"
    },
    "environment": {
      "type": "string",
      "description": "Filter to a specific environment"
    }
  }
}
```

#### Response Format

```json
{
  "metric": "errors",
  "current": {
    "period": { "start": "2025-02-10T07:00:00Z", "end": "2025-02-10T08:00:00Z" },
    "total_errors": 580,
    "by_service": {
      "web-api": 320,
      "worker": 210,
      "scheduler": 50
    }
  },
  "baseline": {
    "period": { "start": "2025-02-10T06:00:00Z", "end": "2025-02-10T07:00:00Z" },
    "total_errors": 120,
    "by_service": {
      "web-api": 60,
      "worker": 40,
      "scheduler": 20
    }
  },
  "changes": {
    "total_change_pct": 383.3,
    "direction": "increase",
    "biggest_movers": [
      { "service": "worker", "from": 40, "to": 210, "change_pct": 425.0 },
      { "service": "web-api", "from": 60, "to": 320, "change_pct": 433.3 }
    ]
  },
  "warnings": [
    "Error rate increased 383% compared to the previous hour",
    "Service 'web-api' errors increased 433% — largest contributor to the spike"
  ]
}
```

#### Implementation Notes

- **Period resolution**: Parse period strings into `(start, end)` time ranges. For `yesterday_same_time`, shift back 24h. For `last_week_same_time`, shift back 168h.
- **Metric sources**:
  - `errors` / `log_volume`: Use `LogStore.CountByLevel()` and `LogStore.CountByService()` (from Phase 3).
  - `alerts`: Use `AlertStore.CountBySeverity()` for both periods.
  - `query_performance`: Use `db_query_stats` internally for both periods (if pg_stat_statements supports time-based snapshots — otherwise note this limitation).
- **Change calculation**: `(current - baseline) / baseline * 100`. Handle baseline=0 gracefully (report as "new" rather than infinity).
- **Biggest movers**: Sort by absolute change, return top 5.
- **Read-only tool**.

#### Dependencies

- Phase 3 (`log_stats`) for `LogStore.CountByLevel()` / `CountByService()`.
- Can implement `alerts` and `query_performance` comparisons independently.

#### Tests — `internal/mcp/tool_compare_periods_test.go`

- `metric=errors` with increase → correct change_pct
- `metric=errors` with decrease → negative change_pct, direction=decrease
- `metric=alerts` → uses AlertStore counts
- `baseline_period=yesterday_same_time` → correct time shift
- Baseline with zero values → "new" rather than infinity
- Service filter narrows results
- Empty periods → zero counts, no errors

---

## Phase 5: Distributed Debugging

### 5.1 `trace_lookup` — Distributed Trace Correlation

**Why**: `log_search` supports `trace_id` filtering, but there's no way to follow a trace across services. A trace tool assembles the full request journey — services touched, timing at each hop, and where errors occurred.

**File**: `internal/mcp/tool_trace_lookup.go`

#### Tool Definition

```json
{
  "name": "trace_lookup",
  "description": "Follow a distributed trace across services. Given a trace ID, assembles all log entries from that trace ordered by timestamp, showing the request journey through services, timing between hops, and where errors occurred. Use when investigating a specific request failure or latency issue.",
  "parameters": {
    "trace_id": {
      "type": "string",
      "description": "The trace/correlation ID to look up (from log entries or error reports)",
      "required": true
    },
    "include_context": {
      "type": "boolean",
      "description": "Include surrounding log entries (+/- 2 seconds) from each service for additional context (default: false)",
      "default": false
    }
  }
}
```

#### Response Format

```json
{
  "trace_id": "abc123-def456",
  "total_entries": 12,
  "total_duration_ms": 2340,
  "services_touched": ["api-gateway", "web-api", "user-service", "postgres"],
  "has_errors": true,
  "timeline": [
    {
      "timestamp": "2025-02-10T08:00:00.000Z",
      "service": "api-gateway",
      "level": "info",
      "message": "POST /api/orders received",
      "elapsed_ms": 0
    },
    {
      "timestamp": "2025-02-10T08:00:00.050Z",
      "service": "web-api",
      "level": "info",
      "message": "Processing order creation",
      "elapsed_ms": 50
    },
    {
      "timestamp": "2025-02-10T08:00:01.200Z",
      "service": "user-service",
      "level": "error",
      "message": "Failed to validate user: connection refused to db-primary:5432",
      "elapsed_ms": 1200
    },
    {
      "timestamp": "2025-02-10T08:00:02.340Z",
      "service": "web-api",
      "level": "error",
      "message": "Order creation failed: upstream service error",
      "elapsed_ms": 2340
    }
  ],
  "service_summary": [
    { "service": "api-gateway", "entries": 2, "errors": 0, "time_spent_ms": 50 },
    { "service": "web-api", "entries": 5, "errors": 1, "time_spent_ms": 1140 },
    { "service": "user-service", "entries": 3, "errors": 1, "time_spent_ms": 1150 },
    { "service": "postgres", "entries": 2, "errors": 0, "time_spent_ms": 0 }
  ],
  "context_entries": [],
  "warnings": [
    "Error in user-service at +1200ms — this is likely the root cause",
    "Gap of 1150ms between web-api→user-service — possible network or queue delay"
  ]
}
```

#### Implementation Notes

- **Data source**: `LogStore.Search()` with `trace_id` filter, ordered by timestamp.
- **Timeline construction**: Sort all entries by timestamp, calculate `elapsed_ms` from first entry.
- **Service summary**: Group by service, calculate time spans (last entry - first entry per service).
- **Gap detection**: Flag gaps >500ms between consecutive entries as potential issues.
- **Error root cause**: The first error entry in the timeline is likely the root cause — highlight it.
- **Context entries** (`include_context=true`): For each service in the trace, also fetch logs from `[first_entry - 2s, last_entry + 2s]` for that service (without trace_id filter) to show surrounding activity.
- **Read-only tool**.

#### Store Changes

None — uses existing `LogStore.Search()` with `TraceID` parameter.

#### Tests — `internal/mcp/tool_trace_lookup_test.go`

- Valid trace_id with entries across services → correct timeline
- Timeline sorted by timestamp
- `elapsed_ms` calculated from first entry
- Service summary grouped correctly
- Error detection: first error flagged as root cause
- Gap detection: large gaps produce warnings
- `include_context=true` → additional context entries
- Unknown trace_id → empty timeline, no error
- Single-service trace → still works

---

## Phase 6: Proactive Intelligence

### 6.1 `suggest_monitors` — AI-Powered Monitor Recommendations

**Why**: The introspection tools include `hint` fields and `existing_monitors`, but there's no unified tool that analyzes the full system state and recommends monitors. This turns OpenTrace from reactive to proactive.

**File**: `internal/mcp/tool_suggest_monitors.go`

#### Tool Definition

```json
{
  "name": "suggest_monitors",
  "description": "Analyze the current database and log state to suggest monitors the user should create. Examines query patterns, table health, log error rates, and existing monitor coverage to identify gaps. Use proactively at session start or when the user asks 'what should I monitor?'.",
  "parameters": {
    "focus": {
      "type": "string",
      "description": "Area to focus on: 'all' (default), 'performance' (slow queries, missing indexes), 'errors' (log patterns, error spikes), 'health' (connections, replication, disk), 'security' (auth failures, privilege escalation)",
      "default": "all"
    },
    "connector_id": {
      "type": "string",
      "description": "Data source connector ID (omit to use all available connectors)"
    }
  }
}
```

#### Response Format

```json
{
  "existing_coverage": {
    "total_monitors": 8,
    "categories": {
      "performance": 2,
      "errors": 3,
      "health": 2,
      "security": 1
    }
  },
  "suggestions": [
    {
      "priority": "high",
      "category": "performance",
      "title": "Monitor slow queries (>5s mean execution time)",
      "reason": "db_query_stats shows 3 queries with mean_exec_time > 5000ms but no monitor covers query performance",
      "monitor_config": {
        "title": "Slow Query Alert",
        "monitor_type": "rule",
        "rule_config": {
          "source": "query",
          "query": "SELECT count(*) FROM pg_stat_activity WHERE state = 'active' AND now() - query_start > interval '5 seconds'",
          "operator": "gt",
          "threshold": 3
        },
        "severity": "warning",
        "schedule": "*/5 * * * *",
        "time_range": "5m"
      }
    },
    {
      "priority": "medium",
      "category": "errors",
      "title": "Monitor 'connection refused' error pattern",
      "reason": "log_stats shows 245 'connection refused' errors in the last hour with no monitor covering this pattern",
      "monitor_config": {
        "title": "Connection Refused Errors",
        "monitor_type": "rule",
        "rule_config": {
          "source": "logs",
          "filter": { "query": "connection refused" },
          "metric": "count",
          "operator": "gt",
          "threshold": 10
        },
        "severity": "critical",
        "schedule": "*/5 * * * *",
        "time_range": "5m"
      }
    }
  ],
  "gaps": [
    "No replication lag monitoring",
    "No disk space monitoring",
    "No dead tuple / vacuum monitoring"
  ]
}
```

#### Implementation Notes

- **Data gathering**: Internally calls the logic behind `db_query_stats`, `db_table_stats`, `db_activity`, and `log_stats` (reuse handler logic, not HTTP calls).
- **Coverage analysis**: Load existing monitors from `WatcherStore.List()`, categorize by type/config, identify uncovered areas.
- **Suggestion generation**: Rule-based (not LLM-based) — match known patterns to missing coverage:
  - No query performance monitor + slow queries found → suggest
  - No connection monitor + high utilization → suggest
  - No replication monitor + replicas present → suggest
  - No log pattern monitor + frequent error pattern → suggest
  - No vacuum monitor + high dead tuples → suggest
- **Config generation**: Produce ready-to-use `monitor_config` objects that can be passed directly to `create_monitor`.
- **Admin only** — suggestions include ready-to-create configs.

#### Tests — `internal/mcp/tool_suggest_monitors_test.go`

- No existing monitors → suggestions for all common patterns
- Full coverage → no suggestions, only "gaps" for niche monitors
- `focus=performance` → only performance-related suggestions
- Suggestion configs are valid for `create_monitor`
- Existing monitor deduplication works (don't suggest what's already monitored)

---

### 6.2 `connection_pool_stats` — Connection Pool Health

**Why**: Connection exhaustion is a top production database issue. Shows pool utilization, wait times, and per-application breakdowns.

**File**: `internal/mcp/tool_pool_stats.go`

#### Tool Definition

```json
{
  "name": "connection_pool_stats",
  "description": "Show connection pool health for database connectors: current utilization, idle/active connections, wait queue depth, and per-application breakdown. Use when diagnosing 'database is slow' or 'connection timeout' issues.",
  "parameters": {
    "connector_id": {
      "type": "string",
      "description": "Data source connector ID (omit to show all connectors)"
    }
  }
}
```

#### Response Format

```json
{
  "connectors": [
    {
      "connector_id": "uuid",
      "connector_name": "production-primary",
      "pool": {
        "max_connections": 100,
        "current_connections": 85,
        "idle_connections": 20,
        "active_connections": 65,
        "utilization_pct": 85.0,
        "waiting_queries": 3
      },
      "by_application": [
        { "application_name": "web-api", "connections": 40, "active": 35, "idle": 5 },
        { "application_name": "worker", "connections": 25, "active": 20, "idle": 5 },
        { "application_name": "scheduler", "connections": 10, "active": 5, "idle": 5 },
        { "application_name": "", "connections": 10, "active": 5, "idle": 5 }
      ],
      "warnings": [
        "Pool utilization at 85% — approaching saturation",
        "3 queries waiting for a connection"
      ]
    }
  ],
  "pgx_pool_stats": {
    "acquire_count": 125000,
    "acquire_duration_avg_ms": 2.5,
    "empty_acquire_count": 340,
    "max_idle_destroy_count": 50,
    "max_lifetime_destroy_count": 12
  }
}
```

#### Implementation Notes

- **Postgres-side**: Query `pg_stat_activity` grouped by `application_name` and `state` (active/idle/idle in transaction).
- **pgx pool-side**: Access `pool.Stat()` from the `DatabaseConnector`'s `*pgxpool.Pool` — this gives `AcquireCount()`, `AcquireDuration()`, `EmptyAcquireCount()`, etc.
- **Expose pool access**: Add a `PoolStats()` method to `DatabaseConnector` that returns the pgx pool stats.
- **Warnings**: Flag utilization >80%, waiting queries >0, high empty acquire count.
- **Read-only tool**.

#### Connector Changes

Add to `DatabaseConnector`:

```go
// PoolStats returns pgxpool statistics for this connector.
func (c *DatabaseConnector) PoolStats() *pgxpool.Stat {
    return c.pool.Stat()
}
```

#### Tests — `internal/mcp/tool_pool_stats_test.go`

- Single connector → pool stats returned
- Multiple connectors → all shown
- Warning at 80%+ utilization
- Warning for waiting queries
- pgx pool stats included
- Empty pool (no connections) → zero values, no errors

---

## File Changes Summary

| File | Change | Phase |
|------|--------|-------|
| `internal/mcp/tool_explain_query.go` | New — EXPLAIN ANALYZE tool | 1 |
| `internal/mcp/tool_explain_query_test.go` | New — tests | 1 |
| `internal/mcp/tool_locks.go` | New — Lock contention tool | 1 |
| `internal/mcp/tool_locks_test.go` | New — tests | 1 |
| `internal/mcp/tool_run_history.go` | New — Monitor run history tool | 2 |
| `internal/mcp/tool_run_history_test.go` | New — tests | 2 |
| `internal/mcp/tool_alert_details.go` | New — Alert deep-dive tool | 2 |
| `internal/mcp/tool_alert_details_test.go` | New — tests | 2 |
| `internal/store/store.go` | Add `AlertStore.GetByID`, `WatcherRunStore.ListWithFilter` | 2 |
| `internal/store/sqlite_alert.go` | Implement `GetByID` | 2 |
| `internal/store/sqlite_watcher_run.go` | Implement `ListWithFilter` | 2 |
| `internal/store/sqlite_migrations/000011_alert_run_id.up.sql` | New — add run_id to alerts | 2 |
| `internal/mcp/tool_log_stats.go` | New — Log aggregation tool | 3 |
| `internal/mcp/tool_log_stats_test.go` | New — tests | 3 |
| `internal/store/store.go` | Add `LogStore.CountByLevel`, `CountByService` | 3 |
| `internal/store/sqlite_log.go` | Implement count methods | 3 |
| `internal/mcp/tool_index_analysis.go` | New — Index analysis tool | 4 |
| `internal/mcp/tool_index_analysis_test.go` | New — tests | 4 |
| `internal/mcp/tool_compare_periods.go` | New — Period comparison tool | 4 |
| `internal/mcp/tool_compare_periods_test.go` | New — tests | 4 |
| `internal/mcp/tool_trace_lookup.go` | New — Trace correlation tool | 5 |
| `internal/mcp/tool_trace_lookup_test.go` | New — tests | 5 |
| `internal/mcp/tool_suggest_monitors.go` | New — Monitor recommendations | 6 |
| `internal/mcp/tool_suggest_monitors_test.go` | New — tests | 6 |
| `internal/mcp/tool_pool_stats.go` | New — Connection pool stats | 6 |
| `internal/mcp/tool_pool_stats_test.go` | New — tests | 6 |
| `internal/connector/database.go` | Add `PoolStats()` method | 6 |
| `internal/mcp/server.go` | Register all new tools with access control | 1-6 |
| `web/mock_test.go` | Update mock stores for new interface methods | 2-3 |
| `mcp/server_test.go` | Update mock stores | 2-3 |

---

## Dependencies Between Phases

```
Phase 1 (Query Debugging) ──── no dependencies
Phase 2 (Monitor Investigation) ──── no dependencies
Phase 3 (Log Intelligence) ──── no dependencies
Phase 4 (Performance Analysis)
  └── compare_periods.errors/log_volume depends on Phase 3 (LogStore count methods)
  └── compare_periods.alerts has no dependencies
  └── db_index_analysis has no dependencies
Phase 5 (Distributed Debugging) ──── no dependencies
Phase 6 (Proactive Intelligence)
  └── suggest_monitors reuses logic from Phases 1, 3, 4
  └── connection_pool_stats has no dependencies
```

**Recommended build order**: Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5 → Phase 6

Phases 1, 2, 3 can be built in parallel since they have no interdependencies.

---

## Access Control Matrix

| Tool | Read-only | Admin | Rationale |
|------|-----------|-------|-----------|
| `explain_query` | - | Yes | Executes queries |
| `db_locks` | Yes | - | Reads system catalogs only |
| `monitor_run_history` | Yes | - | Reads existing data |
| `alert_details` | Yes | - | Reads existing data |
| `log_stats` | Yes | - | Aggregates existing logs |
| `db_index_analysis` | Yes | - | Reads system catalogs only |
| `compare_periods` | Yes | - | Aggregates existing data |
| `trace_lookup` | Yes | - | Reads existing logs |
| `suggest_monitors` | - | Yes | Returns create-ready configs |
| `connection_pool_stats` | Yes | - | Reads pool stats |

---

## Testing Strategy

All tools follow the existing test patterns:
- **Unit tests**: Mock stores via interfaces (same pattern as `mcp/server_test.go`)
- **Integration tests**: Use in-memory SQLite (`setupTestDB(t)`) for store methods
- **Connector tests**: Guarded by `testing.Short()` skip — require Docker Postgres
- Run with: `go test -short -race ./internal/mcp/...` (unit + mock)
- Run with: `go test -race ./internal/mcp/...` (includes integration)

---

## Out of Scope

- Real-time streaming/push notifications (MCP is request-response)
- Query cancellation (`pg_cancel_backend`) — too dangerous for MCP
- DDL execution (CREATE INDEX, REINDEX) — suggest only, user executes
- APM-level tracing (OpenTelemetry spans) — we correlate via log trace_id only
- Historical pg_stat_statements snapshots (Postgres doesn't store these natively)
- Machine learning-based anomaly detection (use rule-based thresholds)
