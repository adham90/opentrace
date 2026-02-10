# Plan 005: Daily Digest via MCP

## Overview

Add a **daily digest** capability that summarizes database health, recent alerts, and monitor status. Delivered proactively through the MCP connection when Claude Code is active, or available on-demand via a dashboard widget and MCP tool.

**Effort**: Medium | **Impact**: Medium

---

## Current State

- Alerts exist with unread/read/dismissed states
- `AlertStore.List()` supports filtering by environment, severity, unread
- `AlertStore.CountUnread()` and `CountTotal()` provide summary counts
- Monitor runs are tracked with status, duration, and results
- No aggregation or summarization of historical data
- No "what happened since I last checked" view
- MCP tools are request-response only — no proactive push

---

## Goals

1. Generate a structured health digest covering alerts, monitors, and trends
2. MCP tool to request digest on-demand ("What happened overnight?")
3. Dashboard widget showing the latest digest
4. Historical digest storage for comparison ("Was last week worse?")
5. Configurable digest schedule and content

---

## Phase 1: Digest Data Model

### 1.1 Digest Generator — `internal/digest/digest.go`

```go
package digest

type Digest struct {
    GeneratedAt   time.Time       `json:"generated_at"`
    PeriodStart   time.Time       `json:"period_start"`
    PeriodEnd     time.Time       `json:"period_end"`
    Environment   string          `json:"environment,omitempty"`

    // Summary counts
    AlertSummary  AlertSummary    `json:"alert_summary"`
    MonitorSummary MonitorSummary `json:"monitor_summary"`

    // Details
    TopAlerts     []AlertDigest   `json:"top_alerts"`
    MonitorHealth []MonitorHealth `json:"monitor_health"`
    Trends        *Trends         `json:"trends,omitempty"`
}

type AlertSummary struct {
    Total        int `json:"total"`
    Critical     int `json:"critical"`
    Warning      int `json:"warning"`
    Info         int `json:"info"`
    Unread       int `json:"unread"`
    NewInPeriod  int `json:"new_in_period"`
}

type MonitorSummary struct {
    Total        int `json:"total"`
    Active       int `json:"active"`
    Paused       int `json:"paused"`
    InError      int `json:"in_error"`
    RunsInPeriod int `json:"runs_in_period"`
    FailedRuns   int `json:"failed_runs"`
}

type AlertDigest struct {
    ID           string    `json:"id"`
    MonitorTitle string    `json:"monitor_title"`
    Severity     string    `json:"severity"`
    Summary      string    `json:"summary"`
    CreatedAt    time.Time `json:"created_at"`
    Read         bool      `json:"read"`
}

type MonitorHealth struct {
    ID          string     `json:"id"`
    Title       string     `json:"title"`
    Status      string     `json:"status"`
    LastRunAt   *time.Time `json:"last_run_at"`
    LastRunOK   bool       `json:"last_run_ok"`
    RunCount    int        `json:"run_count"`    // runs in period
    AlertCount  int        `json:"alert_count"`  // alerts in period
}

type Trends struct {
    AlertsVsPrevPeriod  string `json:"alerts_vs_prev_period"`  // "+15%", "-30%", "same"
    FailedRunsChange    string `json:"failed_runs_change"`
}
```

### 1.2 Digest Builder — `internal/digest/builder.go`

```go
type Builder struct {
    alertStore   store.AlertStore
    watcherStore store.WatcherStore
    runStore     store.WatcherRunStore
}

func (b *Builder) Generate(ctx context.Context, opts DigestOpts) (*Digest, error)

type DigestOpts struct {
    PeriodStart time.Time
    PeriodEnd   time.Time
    Environment string // optional filter
    TopN        int    // max alerts to include (default 10)
}
```

**Implementation steps:**

1. Query alerts in period → build AlertSummary + TopAlerts (sorted by severity then time)
2. Query all watchers → build MonitorSummary
3. Query runs in period → compute per-monitor RunCount, AlertCount, LastRunOK
4. Optionally query previous period → compute Trends
5. Assemble and return Digest

### 1.3 Store Additions

Add to `WatcherRunStore`:

```go
// Count runs in a time period, optionally filtered by status
CountRuns(ctx context.Context, params CountRunParams) (int, error)

type CountRunParams struct {
    Since       time.Time
    Until       time.Time
    Status      string // optional: "completed", "failed", "error"
    WatcherID   *uuid.UUID // optional
}
```

Add to `AlertStore`:

```go
// Count alerts in a time period by severity
CountBySeverity(ctx context.Context, since, until time.Time, environment string) (map[string]int, error)
```

### 1.4 Tests — `internal/digest/builder_test.go`

- Empty period → zero counts, no errors
- Period with mixed alerts → correct severity breakdown
- Monitor with no runs → shows as healthy (not error)
- Trend calculation: more alerts than previous → positive change

---

## Phase 2: Digest Storage

### 2.1 SQLite Table

```sql
-- 000004_add_digests.sql
CREATE TABLE IF NOT EXISTS digests (
    id TEXT PRIMARY KEY,
    environment TEXT NOT NULL DEFAULT '',
    period_start TEXT NOT NULL,
    period_end TEXT NOT NULL,
    data TEXT NOT NULL,  -- JSON blob of Digest struct
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_digests_period ON digests(period_end DESC);
CREATE INDEX idx_digests_env ON digests(environment, period_end DESC);
```

### 2.2 Digest Store — `internal/store/sqlite_digest.go`

```go
type DigestStore interface {
    Save(ctx context.Context, digest Digest) error
    GetLatest(ctx context.Context, environment string) (*Digest, error)
    List(ctx context.Context, limit int, environment string) ([]Digest, error)
}
```

### 2.3 Auto-Generation

The scheduler can generate a daily digest automatically:

- After the first run each day (midnight UTC or configured time)
- Covers the previous 24 hours
- Stored in digests table
- Available immediately via API and MCP

---

## Phase 3: MCP Integration

### 3.1 New MCP Tool: `get_digest`

```json
{
  "name": "get_digest",
  "description": "Get a health digest summarizing database alerts, monitor status, and trends. Use this when the user asks 'what happened overnight?', 'any issues?', 'daily report', or similar.",
  "parameters": {
    "period": {
      "type": "string",
      "description": "Time period: 'last_24h' (default), 'last_12h', 'last_7d', 'today', 'yesterday'",
      "default": "last_24h"
    },
    "environment": {
      "type": "string",
      "description": "Optional environment filter"
    }
  }
}
```

**Response format** (designed for LLM consumption):

```json
{
  "summary": "3 new alerts in the last 24 hours (1 critical, 2 warnings). All 8 monitors active. 47 successful runs, 2 failures.",
  "status": "needs_attention",
  "alerts": {
    "total_new": 3,
    "critical": 1,
    "warning": 2,
    "info": 0,
    "unread": 2,
    "top_alerts": [
      {
        "monitor": "Connection Saturation",
        "severity": "critical",
        "summary": "92 of 100 connections in use",
        "time": "2025-02-10T03:15:00Z"
      }
    ]
  },
  "monitors": {
    "active": 8,
    "errored": 0,
    "problematic": [
      {"name": "Connection Saturation", "alerts_24h": 3}
    ]
  },
  "recommendation": "Connection saturation is critical. Consider increasing max_connections or investigating connection leaks."
}
```

The `status` field: `healthy` (no alerts), `info_only` (only info alerts), `needs_attention` (warnings), `critical` (critical alerts), `degraded` (monitor errors).

### 3.2 Proactive Digest Hint

Add to MCP tool descriptions:

```
At the start of a session, if the user hasn't asked a specific question, consider running get_digest to proactively inform them of any issues: "I checked your database health — here's what's happening..."
```

This is a hint, not forced behavior — the LLM decides when it's appropriate.

---

## Phase 4: Dashboard Widget

### 4.1 Digest Card on Home Page

Add a "Health Summary" card to the alerts dashboard:

```
┌─────────────────────────────────────┐
│ Health Summary (Last 24h)           │
│                                     │
│ 🔴 1 Critical  🟡 2 Warnings       │
│ 8 monitors active · 47 runs · 2 failed │
│                                     │
│ Top issue: Connection Saturation    │
│ 92/100 connections (3 alerts today) │
│                                     │
│ [View Full Digest]                  │
└─────────────────────────────────────┘
```

### 4.2 Digest History Page

`/digests` — list of past digests with:
- Period covered
- Alert counts
- Status badge (healthy/warning/critical)
- Click to expand full details
- Compare with previous period

### 4.3 API Endpoints

- `GET /api/digests/latest?env=production` — latest digest
- `GET /api/digests?limit=7&env=production` — digest history
- `POST /api/digests/generate` — trigger digest generation on demand

---

## Phase 5: Configuration

### 5.1 Digest Settings

Add to app config (environment variables or config file):

```
OPENTRACE_DIGEST_SCHEDULE=0 8 * * *  # Generate at 8am daily (uses Plan 004 cron)
OPENTRACE_DIGEST_TIMEZONE=America/New_York
OPENTRACE_DIGEST_RETENTION=30  # Keep digests for 30 days
```

### 5.2 Per-Environment Digests

If multiple environments exist (production, staging), generate separate digests for each.

---

## File Changes Summary

| File | Change |
|------|--------|
| `internal/digest/digest.go` | New — Digest types |
| `internal/digest/builder.go` | New — Digest generation logic |
| `internal/digest/builder_test.go` | New — tests |
| `internal/store/store.go` | Add DigestStore interface, CountRuns, CountBySeverity |
| `internal/store/sqlite_digest.go` | New — SQLite DigestStore implementation |
| `internal/store/sqlite_digest_test.go` | New — tests |
| `internal/store/sqlite_watcher_run.go` | Add CountRuns method |
| `internal/store/sqlite_alert.go` | Add CountBySeverity method |
| `internal/store/sqlite_migrations/000004_add_digests.sql` | New — migration |
| `internal/mcp/server.go` | Add get_digest tool |
| `internal/web/server.go` | Register digest routes |
| `internal/web/digests.go` | New — digest HTTP handlers |
| `internal/web/templates/home.html` | Add digest widget |
| `internal/web/templates/digests.html` | New — digest history page |

---

## Dependencies

- Plan 004 (Cron Scheduling) — for digest generation schedule (optional, can use simple interval as fallback)

---

## Out of Scope

- Email delivery of digests (combine with Plan 001 webhook notifiers)
- PDF/image export of digests
- Custom digest templates
- AI-generated narrative summaries (future: feed digest to LLM for natural language report)
- Comparison dashboards (week-over-week trending)
