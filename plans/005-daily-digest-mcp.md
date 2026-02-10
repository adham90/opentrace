# Plan 005: Daily Digest via MCP

## Overview

Add a **daily digest** capability that summarizes database health, recent alerts, and monitor status. Available on-demand via an MCP tool, a dashboard widget, and optionally generated on a schedule. The MCP tool generates digests on-the-fly for freshness; scheduled generation stores snapshots for historical comparison.

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

## Implementation Order

Phases are reordered to deliver value early:

1. **Phase 1** — Data model + builder (foundation)
2. **Phase 2** — MCP tool (immediate value, on-the-fly generation, no storage needed)
3. **Phase 3** — Digest storage (persistence for history after the shape is validated)
4. **Phase 4** — Configuration + scheduled generation
5. **Phase 5** — Dashboard widget + history page

This means you get a working `get_digest` MCP tool after just Phase 1+2 without needing migrations or a scheduler.

---

## Phase 1: Digest Data Model

### 1.1 Digest Types — `internal/digest/digest.go`

```go
package digest

type Digest struct {
    ID            string          `json:"id"`
    GeneratedAt   time.Time       `json:"generated_at"`
    PeriodStart   time.Time       `json:"period_start"`
    PeriodEnd     time.Time       `json:"period_end"`
    Environment   string          `json:"environment,omitempty"`
    Status        DigestStatus    `json:"status"`

    // Summary counts
    AlertSummary   AlertSummary   `json:"alert_summary"`
    MonitorSummary MonitorSummary `json:"monitor_summary"`

    // Details
    TopAlerts     []AlertDigest   `json:"top_alerts"`
    MonitorHealth []MonitorHealth `json:"monitor_health"`
    Trends        *Trends         `json:"trends,omitempty"`
}

// DigestStatus is derived from alert/monitor state.
// healthy | info_only | needs_attention | critical | degraded
type DigestStatus string

const (
    StatusHealthy        DigestStatus = "healthy"         // no alerts
    StatusInfoOnly       DigestStatus = "info_only"       // only info alerts
    StatusNeedsAttention DigestStatus = "needs_attention"  // warnings present
    StatusCritical       DigestStatus = "critical"         // critical alerts present
    StatusDegraded       DigestStatus = "degraded"         // monitor errors
)

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

// Trends stores raw counts for current and previous periods.
// Presentation layer (MCP response, dashboard) formats these as percentages.
type Trends struct {
    AlertsPrevCount    int `json:"alerts_prev_count"`
    AlertsCurrentCount int `json:"alerts_current_count"`
    FailedRunsPrev     int `json:"failed_runs_prev"`
    FailedRunsCurrent  int `json:"failed_runs_current"`
}

// AlertsChangePercent returns the percentage change in alerts vs previous period.
// Returns 0 if previous count is 0.
func (t *Trends) AlertsChangePercent() float64 {
    if t.AlertsPrevCount == 0 {
        return 0
    }
    return float64(t.AlertsCurrentCount-t.AlertsPrevCount) / float64(t.AlertsPrevCount) * 100
}

// FailedRunsChangePercent returns the percentage change in failed runs vs previous period.
func (t *Trends) FailedRunsChangePercent() float64 {
    if t.FailedRunsPrev == 0 {
        return 0
    }
    return float64(t.FailedRunsCurrent-t.FailedRunsPrev) / float64(t.FailedRunsPrev) * 100
}
```

### 1.2 Status Derivation — `internal/digest/status.go`

The digest status is computed deterministically from the data, not hardcoded:

```go
// DeriveStatus computes the overall digest status from alert and monitor summaries.
func DeriveStatus(alerts AlertSummary, monitors MonitorSummary) DigestStatus {
    if monitors.InError > 0 {
        return StatusDegraded
    }
    if alerts.Critical > 0 {
        return StatusCritical
    }
    if alerts.Warning > 0 {
        return StatusNeedsAttention
    }
    if alerts.Info > 0 {
        return StatusInfoOnly
    }
    return StatusHealthy
}
```

### 1.3 Digest Builder — `internal/digest/builder.go`

```go
type Builder struct {
    alertStore   store.AlertStore
    watcherStore store.WatcherStore
    runStore     store.WatcherRunStore
}

func NewBuilder(as store.AlertStore, ws store.WatcherStore, rs store.WatcherRunStore) *Builder

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
4. Optionally query previous period (same duration, immediately before PeriodStart) → compute Trends with raw counts
5. Derive status via `DeriveStatus()`
6. Assemble and return Digest

### 1.4 Store Additions

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

### 1.5 Tests — `internal/digest/builder_test.go`

- Empty period → zero counts, no errors, status = healthy
- Period with mixed alerts → correct severity breakdown
- Monitor with no runs → shows as healthy (not error)
- Monitor in error state → status = degraded (takes priority over critical alerts)
- Trend calculation: more alerts than previous → positive AlertsChangePercent
- Trend calculation: zero previous alerts → AlertsChangePercent returns 0 (not divide-by-zero)
- Status derivation: critical > needs_attention > info_only > healthy

---

## Phase 2: MCP Integration

Ship the MCP tool **before storage** — digests are generated on-the-fly from live data.

### 2.1 New MCP Tool: `get_digest`

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

### 2.2 Response Format (designed for LLM consumption)

The response includes structured data so Claude can reason about it. **No `recommendation` field** — the LLM generates its own advice from the raw data, which is more flexible and context-aware than any hardcoded rules.

```json
{
  "summary": "3 new alerts in the last 24 hours (1 critical, 2 warnings). All 8 monitors active. 47 successful runs, 2 failures.",
  "status": "needs_attention",
  "period": {
    "start": "2025-02-09T08:00:00Z",
    "end": "2025-02-10T08:00:00Z"
  },
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
    "total": 8,
    "active": 8,
    "errored": 0,
    "failed_runs": 2,
    "problematic": [
      {"name": "Connection Saturation", "alerts_in_period": 3, "failed_runs": 1}
    ]
  },
  "trends": {
    "alerts_current": 3,
    "alerts_previous": 1,
    "alerts_change_pct": 200.0,
    "failed_runs_current": 2,
    "failed_runs_previous": 0
  }
}
```

The `status` field: `healthy` (no alerts), `info_only` (only info alerts), `needs_attention` (warnings), `critical` (critical alerts), `degraded` (monitor errors).

The `summary` field is a pre-formatted one-liner for quick display. The rest is structured data the LLM (or dashboard) can use for deeper reasoning.

### 2.3 Proactive Digest via MCP Resource

Register an MCP **resource** `digest://latest` that provides the latest digest as ambient context. This is more reliable than tool-description hints because MCP resources can be read at session start without the LLM needing to decide to call a tool.

```go
// Register as an MCP resource
server.AddResource("digest://latest", mcp.Resource{
    Name:        "Latest Health Digest",
    Description: "Current health summary of all monitored databases",
    MIMEType:    "application/json",
})
```

As a fallback, also add a hint to the `get_digest` tool description:

```
At the start of a session, if the user hasn't asked a specific question, consider running get_digest to proactively inform them of any issues.
```

### 2.4 Freshness Strategy

On-demand `get_digest` calls always generate a **fresh** digest from live data. This ensures the response is never stale. Generation is fast (a few SQL queries) so caching is unnecessary at this stage.

If a stored digest exists (Phase 3) and is < 5 minutes old, the API endpoints may return the cached version. The MCP tool always generates fresh.

---

## Phase 3: Digest Storage

### 3.1 SQLite Table

Promote key summary fields to columns for queryability. The full digest JSON is still stored for detail views.

```sql
-- 000004_add_digests.sql
CREATE TABLE IF NOT EXISTS digests (
    id TEXT PRIMARY KEY,
    environment TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'healthy',
    period_start TEXT NOT NULL,
    period_end TEXT NOT NULL,
    alert_total INTEGER NOT NULL DEFAULT 0,
    alert_critical INTEGER NOT NULL DEFAULT 0,
    alert_warning INTEGER NOT NULL DEFAULT 0,
    monitor_total INTEGER NOT NULL DEFAULT 0,
    monitor_errored INTEGER NOT NULL DEFAULT 0,
    failed_runs INTEGER NOT NULL DEFAULT 0,
    data TEXT NOT NULL,  -- full JSON blob of Digest struct for detail view
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_digests_period ON digests(period_end DESC);
CREATE INDEX idx_digests_env ON digests(environment, period_end DESC);
CREATE INDEX idx_digests_status ON digests(status, period_end DESC);
```

This lets you query `WHERE status = 'critical'` or `WHERE alert_critical > 0` without `json_extract()`.

### 3.2 Digest Store — `internal/store/sqlite_digest.go`

```go
type DigestStore interface {
    Save(ctx context.Context, digest Digest) error
    GetLatest(ctx context.Context, environment string) (*Digest, error)
    List(ctx context.Context, limit int, environment string) ([]Digest, error)
    DeleteOlderThan(ctx context.Context, before time.Time) (int, error)
}
```

`DeleteOlderThan` is used for retention cleanup (see Phase 4).

### 3.3 Auto-Generation

The scheduler generates a daily digest automatically:

- At the configured time (default: midnight UTC)
- Covers the previous 24 hours
- Stored in digests table
- Available immediately via API and MCP

---

## Phase 4: Configuration & Retention

### 4.1 Digest Settings

Add to app config (environment variables or config file):

```
OPENTRACE_DIGEST_SCHEDULE=0 8 * * *  # Generate at 8am daily (uses Plan 004 cron)
OPENTRACE_DIGEST_TIMEZONE=America/New_York
OPENTRACE_DIGEST_RETENTION=30  # Keep digests for 30 days
```

### 4.2 Retention Cleanup

Run cleanup as part of scheduled digest generation:

```go
// After generating and storing a new digest, clean up old ones
func (s *Scheduler) cleanupOldDigests(ctx context.Context) {
    cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
    deleted, err := s.digestStore.DeleteOlderThan(ctx, cutoff)
    if err != nil {
        log.Printf("digest cleanup error: %v", err)
        return
    }
    if deleted > 0 {
        log.Printf("cleaned up %d old digests", deleted)
    }
}
```

### 4.3 Per-Environment Digests

If multiple environments exist (production, staging), generate separate digests for each.

---

## Phase 5: Dashboard Widget

### 5.1 Digest Card on Home Page

Add a "Health Summary" card to the alerts dashboard. Fetches data via `GET /api/digests/latest`.

```
+-------------------------------------+
| Health Summary (Last 24h)           |
|                                     |
| [critical] 1 Critical  [warn] 2 Warnings |
| 8 monitors active . 47 runs . 2 failed   |
|                                     |
| Top issue: Connection Saturation    |
| 92/100 connections (3 alerts today) |
|                                     |
| [View Full Digest]                  |
+-------------------------------------+
```

The widget uses HTMX to load content from a partial endpoint:

- `GET /partials/digest-summary` — returns the HTML fragment for the card
- Uses `hx-trigger="load"` to fetch on page load
- Uses `hx-trigger="every 60s"` to auto-refresh

### 5.2 Digest History Page

`/digests` — list of past digests with:
- Period covered
- Alert counts (from promoted columns — no JSON parsing needed)
- Status badge (healthy/warning/critical) using the `status` column
- Click to expand full details (loads `data` JSON)
- Compare with previous period (side-by-side trend display)

### 5.3 API Endpoints

- `GET /api/digests/latest?env=production` — latest stored digest (or generate fresh if none exists)
- `GET /api/digests?limit=7&env=production` — digest history
- `POST /api/digests/generate` — trigger digest generation on demand

### 5.4 Routes and Templates

```go
// web/server.go — register digest routes
r.Route("/digests", func(r chi.Router) {
    r.Get("/", s.handleDigestHistory)          // digest history page
})
r.Route("/api/digests", func(r chi.Router) {
    r.Get("/latest", s.handleGetLatestDigest)
    r.Get("/", s.handleListDigests)
    r.Post("/generate", s.handleGenerateDigest)
})
r.Get("/partials/digest-summary", s.handleDigestSummaryPartial)
```

Templates:
- `templates/digests.html` — full digest history page with list and detail view
- `templates/partials/digest-summary.html` — HTMX fragment for home page card

---

## File Changes Summary

| File | Change | Phase |
|------|--------|-------|
| `internal/digest/digest.go` | New — Digest types, Trends with numeric fields | 1 |
| `internal/digest/status.go` | New — DeriveStatus logic | 1 |
| `internal/digest/builder.go` | New — Digest generation logic | 1 |
| `internal/digest/builder_test.go` | New — tests | 1 |
| `internal/store/store.go` | Add CountRuns, CountBySeverity to existing interfaces | 1 |
| `internal/store/sqlite_watcher_run.go` | Add CountRuns method | 1 |
| `internal/store/sqlite_alert.go` | Add CountBySeverity method | 1 |
| `internal/mcp/server.go` | Add get_digest tool + digest://latest resource | 2 |
| `internal/mcp/server_test.go` | Tests for get_digest | 2 |
| `internal/store/store.go` | Add DigestStore interface | 3 |
| `internal/store/sqlite_digest.go` | New — SQLite DigestStore with DeleteOlderThan | 3 |
| `internal/store/sqlite_digest_test.go` | New — tests | 3 |
| `internal/store/sqlite_migrations/000004_add_digests.sql` | New — migration with promoted columns | 3 |
| `internal/web/server.go` | Register digest routes + partials | 5 |
| `internal/web/digests.go` | New — digest HTTP handlers | 5 |
| `internal/web/templates/home.html` | Add HTMX digest widget | 5 |
| `internal/web/templates/digests.html` | New — digest history page | 5 |
| `internal/web/templates/partials/digest-summary.html` | New — HTMX fragment | 5 |

---

## Dependencies

- Plan 004 (Cron Scheduling) — for digest generation schedule (optional, can use simple interval as fallback)

---

## Out of Scope

- Email delivery of digests (combine with Plan 001 webhook notifiers)
- PDF/image export of digests
- Custom digest templates
- AI-generated narrative summaries (the LLM generates advice from structured data naturally)
- Comparison dashboards (week-over-week trending beyond the Trends struct)
