# Monitors: Rule-Based & AI Monitors Plan

## Overview

Evolve the existing "watcher" system into a unified **Monitors** system with two evaluation strategies:

| Monitor Type | How it evaluates | Use case |
|---|---|---|
| **AI Monitor** | Runs query/log search → sends results to LLM → LLM decides if alert needed | Complex pattern detection, anomaly analysis, multi-signal correlation |
| **Rule Monitor** | Runs query/log search → evaluates result against a condition (threshold, count, pattern) | Simple, deterministic checks — fast, no LLM cost |

The existing AI watcher flow is preserved and renamed. Rule monitors reuse the same scheduling, alerting, and notification infrastructure — only the evaluation step changes.

---

## Terminology Rename

| Old | New |
|---|---|
| Watcher | Monitor |
| watcher (DB table) | monitors (DB table) |
| WatcherStore | MonitorStore |
| WatcherRun | MonitorRun |
| WatcherRunStore | MonitorRunStore |
| watcher_runs | monitor_runs |

> **Decision**: We can do this rename incrementally — add `monitor_type` column first, rename tables/interfaces in a later phase. The plan below assumes incremental approach to minimize blast radius.

---

## Rule Monitor Sources

Three sources a rule monitor can evaluate:

### 1. Query Monitor
Runs a SQL query against a connected data source (Postgres via connector), checks the result.

**Examples:**
| Scenario | Query | Condition |
|---|---|---|
| Stuck transactions | `SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction' AND now() - xact_start > '5 min'` | row_count > 0 |
| Connection saturation | `SELECT count(*) FROM pg_stat_activity` | value > 80 |
| Replication lag | `SELECT EXTRACT(EPOCH FROM replay_lag) FROM pg_stat_replication` | value > 60 |
| Dead tuples | `SELECT n_dead_tup FROM pg_stat_user_tables WHERE relname = 'orders'` | value > 50000 |
| Long-running queries | `SELECT count(*) FROM pg_stat_activity WHERE state = 'active' AND now() - query_start > '30s'` | value > 3 |
| Stuck jobs | `SELECT count(*) FROM jobs WHERE status = 'pending' AND created_at < now() - interval '1 hour'` | row_count > 0 |
| Failed payments | `SELECT count(*) FROM payments WHERE status = 'failed' AND created_at > now() - interval '15 min'` | value > 5 |
| Stale sync | `SELECT EXTRACT(EPOCH FROM now() - max(synced_at)) FROM sync_log` | value > 3600 |
| Signup drop | `SELECT count(*) FROM users WHERE created_at > now() - interval '1 hour'` | value < 1 |

### 2. Log Monitor
Filters ingested logs (using existing LogStore.Search), checks volume or patterns.

**Examples:**
| Scenario | Filter | Condition |
|---|---|---|
| Error spike | service=`payments`, level=`ERROR` | count > 10 in 5m |
| Specific failure | message contains `connection refused` | count > 0 in 5m |
| Silent service | service=`worker` | count < 1 in 15m |
| Auth failures | message matches `401\|403\|unauthorized` | count > 20 in 5m |
| Disk warnings | level=`WARN`, message contains `disk` | count > 0 in 10m |

### 3. Health Monitor
Checks if a data source is reachable and responsive.

**Examples:**
| Scenario | Condition |
|---|---|
| Database down | connection fails |
| Slow database | ping latency > 2000ms |
| Read replica down | connection to replica fails |

---

## Phase 1: Data Model Changes

### 1.1 New Migration: `000008_monitor_type.up.sql`

```sql
-- Add monitor_type to watchers table (default 'ai' preserves existing behavior)
ALTER TABLE watchers ADD COLUMN monitor_type TEXT NOT NULL DEFAULT 'ai';

-- Rule configuration (JSON) — only used when monitor_type = 'rule'
ALTER TABLE watchers ADD COLUMN rule_config TEXT;

-- Data source ID for query/health monitors (FK to data_sources)
ALTER TABLE watchers ADD COLUMN data_source_id TEXT;

-- Index for quick lookup by type
CREATE INDEX idx_watchers_monitor_type ON watchers(monitor_type);
```

### 1.2 Rule Config Schema

The `rule_config` column stores a JSON object whose shape depends on the rule source:

**Query Monitor:**
```json
{
  "source": "query",
  "query": "SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction'",
  "metric": "value",
  "operator": "gt",
  "threshold": 0
}
```

- `metric`: `"value"` (first column of first row), `"row_count"` (number of rows returned)
- `operator`: `"gt"`, `"gte"`, `"lt"`, `"lte"`, `"eq"`, `"neq"`
- `threshold`: numeric value to compare against

**Log Monitor:**
```json
{
  "source": "logs",
  "filter": {
    "service": "payments-service",
    "level": "ERROR",
    "query": "connection refused"
  },
  "time_window": "5m",
  "metric": "count",
  "operator": "gt",
  "threshold": 10
}
```

- `filter`: maps to `LogSearchParams` fields (service, level, query, environment)
- `time_window`: duration string — how far back to search from now
- `metric`: `"count"` (number of matching logs)

**Health Monitor:**
```json
{
  "source": "health",
  "checks": ["connection", "latency"],
  "latency_threshold_ms": 2000
}
```

- `checks`: which health checks to run
- `latency_threshold_ms`: max acceptable ping time

### 1.3 Go Types

```go
// MonitorType represents the evaluation strategy.
type MonitorType string

const (
    MonitorTypeAI   MonitorType = "ai"
    MonitorTypeRule  MonitorType = "rule"
)

// RuleSource identifies what the rule evaluates.
type RuleSource string

const (
    RuleSourceQuery  RuleSource = "query"
    RuleSourceLogs   RuleSource = "logs"
    RuleSourceHealth RuleSource = "health"
)

// RuleOperator defines comparison operators.
type RuleOperator string

const (
    OpGreaterThan      RuleOperator = "gt"
    OpGreaterThanEqual RuleOperator = "gte"
    OpLessThan         RuleOperator = "lt"
    OpLessThanEqual    RuleOperator = "lte"
    OpEqual            RuleOperator = "eq"
    OpNotEqual         RuleOperator = "neq"
)

// RuleConfig is the top-level rule configuration.
type RuleConfig struct {
    Source           RuleSource       `json:"source"`
    // Query source fields
    Query            string           `json:"query,omitempty"`
    Metric           string           `json:"metric,omitempty"`       // "value", "row_count", "count"
    Operator         RuleOperator     `json:"operator,omitempty"`
    Threshold        float64          `json:"threshold"`
    // Log source fields
    Filter           *LogFilter       `json:"filter,omitempty"`
    TimeWindow       string           `json:"time_window,omitempty"`  // "5m", "1h", etc.
    // Health source fields
    Checks           []string         `json:"checks,omitempty"`
    LatencyThreshold int              `json:"latency_threshold_ms,omitempty"`
}

// LogFilter defines log search criteria for rule monitors.
type LogFilter struct {
    Service     string `json:"service,omitempty"`
    Level       string `json:"level,omitempty"`
    Query       string `json:"query,omitempty"`
    Environment string `json:"environment,omitempty"`
}
```

### 1.4 Model Changes to Watcher/Monitor

Add to existing `Watcher` struct:

```go
type Watcher struct {
    // ... existing fields ...
    MonitorType  MonitorType      `json:"monitor_type"`
    RuleConfig   *RuleConfig      `json:"rule_config,omitempty"`
    DataSourceID *uuid.UUID       `json:"data_source_id,omitempty"`
}
```

Add to `CreateWatcherParams` and `UpdateWatcherParams` similarly.

---

## Phase 2: Rule Evaluator Engine

### 2.1 Evaluator Interface

Create `internal/watcher/evaluator.go`:

```go
// EvalResult holds the output of an evaluation.
type EvalResult struct {
    HasAlert bool
    Summary  string      // human-readable summary of what was found
    Details  any         // structured data (query results, log counts, etc.)
    Value    *float64    // the measured value (for display in UI)
}

// Evaluator evaluates a monitor and returns whether an alert should fire.
type Evaluator interface {
    Evaluate(ctx context.Context, w store.Watcher) (*EvalResult, error)
}
```

### 2.2 Rule Evaluator

Create `internal/watcher/rule_evaluator.go`:

```go
type RuleEvaluator struct {
    registry *connector.Registry
    logStore store.LogStore
}

func (re *RuleEvaluator) Evaluate(ctx context.Context, w store.Watcher) (*EvalResult, error) {
    cfg := w.RuleConfig
    switch cfg.Source {
    case RuleSourceQuery:
        return re.evaluateQuery(ctx, w)
    case RuleSourceLogs:
        return re.evaluateLogs(ctx, w)
    case RuleSourceHealth:
        return re.evaluateHealth(ctx, w)
    default:
        return nil, fmt.Errorf("unknown rule source: %s", cfg.Source)
    }
}
```

**evaluateQuery**: Uses connector.Registry to get the data source connector → runs the SQL query → extracts the metric (value or row_count) → compares against threshold.

**evaluateLogs**: Builds a `LogSearchParams` from the filter config with `Start` = `now - time_window` → calls `LogStore.Search` → counts results → compares against threshold.

**evaluateHealth**: Gets the connector from Registry → attempts a ping/simple query → measures latency → evaluates against checks.

### 2.3 AI Evaluator (refactor existing)

Wrap the existing agent-based flow into the same interface:

```go
type AIEvaluator struct {
    registry      *connector.Registry
    providerCache *llm.ProviderCache
    agentCfg      agent.RunConfig
    runStore      store.WatcherRunStore
    eventHub      *EventHub
}

func (ae *AIEvaluator) Evaluate(ctx context.Context, w store.Watcher) (*EvalResult, error) {
    // existing agent execution logic from Executor.Execute steps 2-7
    // returns EvalResult with HasAlert from EvaluateFindings()
}
```

### 2.4 Executor Changes

Modify `Executor.Execute` to dispatch based on `MonitorType`:

```go
func (e *Executor) Execute(ctx context.Context, w store.Watcher) {
    run, err := e.runStore.Create(ctx, w.ID)
    // ... existing setup ...

    var result *EvalResult
    switch w.MonitorType {
    case store.MonitorTypeAI:
        result, err = e.aiEvaluator.Evaluate(ctx, w)
    case store.MonitorTypeRule:
        result, err = e.ruleEvaluator.Evaluate(ctx, w)
    }

    // ... existing alert creation & notification logic using result ...
}
```

The key: **Scheduler, run tracking, alerting, and notifications are completely unchanged.** Only the evaluation dispatch is new.

---

## Phase 3: API Endpoints

### 3.1 New/Modified Endpoints

All existing watcher endpoints continue to work. New additions:

| Method | Path | Description |
|---|---|---|
| GET | `/api/monitors/templates` | List available monitor templates |
| POST | `/api/monitors/preview` | Run a rule evaluation without saving (live preview) |
| GET | `/api/datasources/{id}/ping` | Health check a data source (for health monitors) |

### 3.2 Preview Endpoint

This powers the "Run Preview" button in the UI — critical for good UX.

```
POST /api/monitors/preview
{
  "monitor_type": "rule",
  "rule_config": {
    "source": "query",
    "query": "SELECT count(*) FROM pg_stat_activity",
    "metric": "value",
    "operator": "gt",
    "threshold": 80
  },
  "data_source_id": "uuid-here"
}

Response:
{
  "current_value": 42,
  "would_alert": false,
  "summary": "Value is 42 (threshold: > 80)",
  "query_time_ms": 23
}
```

### 3.3 Templates Endpoint

Returns a list of pre-built monitor templates:

```
GET /api/monitors/templates

Response:
[
  {
    "id": "stuck-transactions",
    "name": "Stuck Transactions",
    "description": "Alert when transactions are idle for more than 5 minutes",
    "category": "database",
    "monitor_type": "rule",
    "rule_config": {
      "source": "query",
      "query": "SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction' AND now() - xact_start > interval '5 minutes'",
      "metric": "value",
      "operator": "gt",
      "threshold": 0
    },
    "severity": "critical",
    "time_range": "5m"
  },
  ...
]
```

Templates are defined as a Go slice (not DB) — simple, no migration needed, easy to add.

### 3.4 Create/Update Params Changes

The existing `POST /api/watchers` and `PUT /api/watchers/:id` endpoints accept the new fields:

```json
{
  "title": "Stuck transactions on production",
  "monitor_type": "rule",
  "data_source_id": "uuid-of-production-db",
  "rule_config": {
    "source": "query",
    "query": "SELECT count(*) ...",
    "metric": "value",
    "operator": "gt",
    "threshold": 0
  },
  "severity": "critical",
  "time_range": "5m",
  "notify": ["dashboard"]
}
```

When `monitor_type` is `"rule"`, the `description`, `model`, and `effort` fields are ignored (they're AI-specific).

---

## Phase 4: Dashboard UI

### 4.1 New Monitor Page — Template Selection

Route: `/monitors/new`

```
┌─────────────────────────────────────────────────────────┐
│ New Monitor                                              │
│                                                          │
│ What would you like to monitor?                          │
│                                                          │
│ ┌─────────────┐ ┌─────────────┐ ┌─────────────┐        │
│ │  Query       │ │  Logs        │ │  Health      │        │
│ │  Run SQL     │ │  Watch log   │ │  Check if    │        │
│ │  against     │ │  patterns    │ │  databases   │        │
│ │  your DB     │ │  & volume    │ │  are alive   │        │
│ └─────────────┘ └─────────────┘ └─────────────┘        │
│                                                          │
│ ┌─────────────┐                                         │
│ │  AI          │                                         │
│ │  Let AI      │                                         │
│ │  analyze     │                                         │
│ │  your data   │                                         │
│ └─────────────┘                                         │
│                                                          │
│ ── Or start from a template ──────────────────────────  │
│                                                          │
│ Database                                                 │
│ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐     │
│ │ Stuck        │ │ Connection   │ │ Long-Running │     │
│ │ Transactions │ │ Count        │ │ Queries      │     │
│ └──────────────┘ └──────────────┘ └──────────────┘     │
│                                                          │
│ Logs                                                     │
│ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐     │
│ │ Error Spike  │ │ Silent       │ │ Auth         │     │
│ │              │ │ Service      │ │ Failures     │     │
│ └──────────────┘ └──────────────┘ └──────────────┘     │
└─────────────────────────────────────────────────────────┘
```

### 4.2 Query Monitor Form

Route: `/monitors/new?type=rule&source=query` or after selecting a template.

```
┌─────────────────────────────────────────────────────────┐
│ New Query Monitor                                        │
│                                                          │
│ Name                                                     │
│ ┌─────────────────────────────────────────────────┐     │
│ │ Stuck transactions on production                 │     │
│ └─────────────────────────────────────────────────┘     │
│                                                          │
│ Data Source                                              │
│ ┌──────────────────────────────────────────┐            │
│ │ production-db                         ▼  │            │
│ └──────────────────────────────────────────┘            │
│                                                          │
│ Query                                                    │
│ ┌─────────────────────────────────────────────────┐     │
│ │ SELECT count(*)                                  │     │
│ │ FROM pg_stat_activity                            │     │
│ │ WHERE state = 'idle in transaction'              │     │
│ │   AND now() - xact_start > interval '5 min'     │     │
│ └─────────────────────────────────────────────────┘     │
│                                                          │
│ [▶ Run Preview]   Current value: 2  ✓ No alert          │
│                                                          │
│ Alert When                                               │
│ ┌──────────┐  ┌────────────────────┐  ┌──────┐         │
│ │ Value  ▼ │  │ is greater than  ▼ │  │ 0    │         │
│ └──────────┘  └────────────────────┘  └──────┘         │
│                                                          │
│ Severity          │ Check Every                          │
│ ┌────────────┐    │ ┌────┐ ┌──────────┐                │
│ │ Critical ▼ │    │ │ 5  │ │ minutes ▼│                │
│ └────────────┘    │ └────┘ └──────────┘                │
│                                                          │
│ Environment                                              │
│ ┌──────────────────────────────────────────┐            │
│ │ production                            ▼  │            │
│ └──────────────────────────────────────────┘            │
│                                                          │
│ Notifications                                            │
│ ☑ Dashboard alert                                       │
│ ☐ Webhook                                               │
│                                                          │
│                              [Cancel]  [Create Monitor]  │
└─────────────────────────────────────────────────────────┘
```

### 4.3 Log Monitor Form

Route: `/monitors/new?type=rule&source=logs`

```
┌─────────────────────────────────────────────────────────┐
│ New Log Monitor                                          │
│                                                          │
│ Name                                                     │
│ ┌─────────────────────────────────────────────────┐     │
│ │ Payment error spike                              │     │
│ └─────────────────────────────────────────────────┘     │
│                                                          │
│ Filter Logs                                              │
│ Service  ┌───────────────────────────────────────┐      │
│          │ payments-service                    ▼  │      │
│          └───────────────────────────────────────┘      │
│ Level    ┌───────────────────────────────────────┐      │
│          │ ERROR                               ▼  │      │
│          └───────────────────────────────────────┘      │
│ Message  ┌───────────┐ ┌─────────────────────────┐     │
│          │ contains ▼ │ │ connection refused       │     │
│          └───────────┘ └─────────────────────────┘     │
│                                              [+ filter] │
│                                                          │
│ Time Window                                              │
│ Look at the last ┌────┐ ┌──────────┐                   │
│                   │ 5  │ │ minutes ▼│                   │
│                   └────┘ └──────────┘                   │
│                                                          │
│ [▶ Preview]   Matching now: 3 logs in last 5 min        │
│                                                          │
│ Alert When                                               │
│ ┌──────────┐  ┌────────────────────┐  ┌──────┐         │
│ │ Count  ▼ │  │ is greater than  ▼ │  │ 10   │         │
│ └──────────┘  └────────────────────┘  └──────┘         │
│                                                          │
│ Check Every ┌────┐ ┌──────────┐                         │
│             │ 5  │ │ minutes ▼│                         │
│             └────┘ └──────────┘                         │
│                                                          │
│                              [Cancel]  [Create Monitor]  │
└─────────────────────────────────────────────────────────┘
```

### 4.4 Health Monitor Form

Route: `/monitors/new?type=rule&source=health`

```
┌─────────────────────────────────────────────────────────┐
│ New Health Monitor                                       │
│                                                          │
│ Name                                                     │
│ ┌─────────────────────────────────────────────────┐     │
│ │ Production DB health check                       │     │
│ └─────────────────────────────────────────────────┘     │
│                                                          │
│ Data Sources                                             │
│ ☑ production-db                                         │
│ ☑ read-replica                                          │
│ ☐ staging-db                                            │
│                                                          │
│ Alert When                                               │
│ ☑ Connection fails                                      │
│ ☑ Ping latency exceeds ┌──────┐ ms                     │
│                         │ 2000 │                         │
│                         └──────┘                         │
│                                                          │
│ Check Every ┌────┐ ┌──────────┐                         │
│             │ 1  │ │ minutes ▼│                         │
│             └────┘ └──────────┘                         │
│                                                          │
│                              [Cancel]  [Create Monitor]  │
└─────────────────────────────────────────────────────────┘
```

### 4.5 Monitor List Page Updates

The existing watchers list page adds:
- A **type badge** per row: `AI` | `Query` | `Log` | `Health`
- Filter/tab to show by type
- The "New Monitor" button goes to the template selection page
- Last measured value column for rule monitors (e.g., "42 / threshold: 80")

---

## Phase 5: MCP Tool Updates

### 5.1 Updated Tools

| Tool | Change |
|---|---|
| `create_watcher` → `create_monitor` | Add `monitor_type`, `rule_config`, `data_source_id` params |
| `list_watchers` → `list_monitors` | Add `monitor_type` filter param |
| New: `preview_monitor` | Run a rule evaluation ad-hoc, return current value |

### 5.2 Example MCP Usage

```
User: "Create a monitor that alerts if there are more than 100 active connections on production-db"

Tool call: create_monitor({
  title: "Connection count on production-db",
  monitor_type: "rule",
  data_source_id: "...",
  rule_config: {
    source: "query",
    query: "SELECT count(*) FROM pg_stat_activity",
    metric: "value",
    operator: "gt",
    threshold: 100
  },
  severity: "warning",
  time_range: "5m"
})
```

---

## Phase 6: Templates Library

### 6.1 Built-in Templates

Defined in `internal/watcher/templates.go` as a Go slice — no DB storage needed.

**Database category:**
| Template | Source | Default Severity |
|---|---|---|
| Stuck Transactions | query | critical |
| Connection Saturation | query | warning |
| Long-Running Queries | query | warning |
| Replication Lag | query | critical |
| Dead Tuple Bloat | query | info |
| Table Size Growth | query | info |

**Logs category:**
| Template | Source | Default Severity |
|---|---|---|
| Error Rate Spike | logs | warning |
| Silent Service (Heartbeat) | logs | critical |
| Auth Failure Spike | logs | warning |
| Specific Error Pattern | logs | warning |

**Health category:**
| Template | Source | Default Severity |
|---|---|---|
| Database Connectivity | health | critical |
| Database Latency | health | warning |

Each template pre-fills: name, query/filter, metric, operator, threshold, severity, time_range. The user only needs to pick a data source and optionally adjust thresholds.

---

## Implementation Order

### Iteration 1 — Query Rule Monitors (Highest value, simplest path)
1. Migration: add `monitor_type`, `rule_config`, `data_source_id` columns
2. Go types: `MonitorType`, `RuleConfig`, model changes
3. `RuleEvaluator` with `evaluateQuery` only
4. Executor dispatch by `monitor_type`
5. API: accept new fields in create/update, preview endpoint
6. UI: query monitor form with live preview
7. Tests: rule evaluator unit tests, API integration tests

### Iteration 2 — Log Rule Monitors
1. `evaluateLogs` in `RuleEvaluator`
2. UI: log monitor form with filter builder
3. Tests

### Iteration 3 — Health Monitors
1. `evaluateHealth` in `RuleEvaluator`
2. Data source ping endpoint
3. UI: health monitor form
4. Tests

### Iteration 4 — Templates & Polish
1. Templates library (`templates.go`)
2. Templates API endpoint
3. UI: template selection page
4. Monitor list page updates (type badges, value display)

### Iteration 5 — Rename Watcher → Monitor (Optional, low priority)
1. DB migration: rename tables
2. Go interfaces: rename stores
3. API: add `/api/monitors/*` routes (keep `/api/watchers/*` as aliases)
4. MCP: rename tools
5. UI: update all labels

---

## Key Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Rule config storage | JSON TEXT column | Flexible, schema varies by source type, avoids table explosion |
| Template storage | Go code, not DB | Templates are read-only, versioned with code, no migration needed |
| Evaluator interface | `Evaluate(ctx, Watcher) → EvalResult` | Same contract for AI and Rule — executor doesn't care which |
| Preview endpoint | Stateless POST | No side effects, no run record created, fast feedback loop |
| Incremental rename | Add type column first, rename later | Ship value fast, avoid big-bang migration |
| Health monitor scope | Per data source, not arbitrary URLs | Reuse existing connector infrastructure |
| Operator set | gt/gte/lt/lte/eq/neq | Covers all practical cases, no regex on numeric values |
| Log metric | Count only (initially) | Rate/spike detection adds complexity, ship count first |

---

## Files to Create/Modify

### New Files
- `internal/store/sqlite_migrations/000008_monitor_type.up.sql`
- `internal/watcher/evaluator.go` — interface
- `internal/watcher/rule_evaluator.go` — query, log, health evaluation
- `internal/watcher/rule_evaluator_test.go`
- `internal/watcher/templates.go` — built-in template definitions
- `internal/web/templates/monitors_new.html` — template selection + forms

### Modified Files
- `internal/store/models.go` — add MonitorType, RuleConfig, new fields to Watcher
- `internal/store/store.go` — no interface changes needed (same CRUD)
- `internal/store/watcher_store.go` — scan new columns
- `internal/watcher/executor.go` — dispatch to evaluator by type
- `internal/web/watchers.go` — new endpoints (preview, templates)
- `internal/web/server.go` — register new routes
- `internal/web/templates/watchers.html` — type badges, value column
- `internal/mcp/server.go` — update create_watcher tool params
