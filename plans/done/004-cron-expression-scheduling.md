# Plan 004: Cron Expression Scheduling

## Overview

Replace the current interval-only scheduling (`time_range` used as both schedule interval AND log analysis window) with a **dedicated `schedule` field** supporting cron expressions. This gives users precise control over when monitors run while keeping backwards compatibility.

**Effort**: Low-Medium | **Impact**: Medium

---

## Current State

- `time_range` serves a **dual purpose**: scheduling interval AND log lookback window
- Scheduler polls every `PollInterval` (default 30s) for due watchers
- `GetDueWatchers()` returns watchers where `next_run_at <= now()` and `status = 'active'`
- After execution, `updateWatcherTiming()` sets `next_run_at = now() + ParseTimeRange(time_range)`
- `time_range` is a string like "5m", "15m", "1h", "6h", "24h"
- `ParseTimeRange()` converts string to `time.Duration`
- No support for "run at 9am daily" or "run on weekdays only"
- The UI form hint says: "How often this watcher runs AND the log window it analyzes"

### Why This Matters — Examples

**Example 1: Business-hours-only monitoring**
A user monitors checkout errors. With `time_range: "15m"`, the monitor runs 96 times/day — including 2 AM. With cron: `0 8-20 * * 1-5` runs only during business hours, saving LLM API costs and reducing noise.

**Example 2: Daily summary reports**
A user wants a "daily health check" at 9 AM. Currently impossible — `24h` drifts because `next_run = now + 24h` is relative to completion time. With cron: `0 9 * * *` always fires at exactly 9 AM.

**Example 3: Time drift problem**
With interval `24h`, if a run takes 2 minutes to complete, over a week the run time drifts by 14 minutes. Cron expressions don't drift.

---

## Goals

1. **Split the dual-purpose `time_range`** into `schedule` (when to run) and `time_range` (log lookback window)
2. Support standard cron expressions (5-field: min hour dom month dow)
3. Support predefined schedules: `@hourly`, `@daily`, `@weekly`, `@every 5m`
4. Backwards compatible — existing duration strings ("5m", "1h") keep working as both schedule and lookback
5. Human-readable schedule descriptions in UI and API
6. Proper validation with clear error messages (no silent fallback to 15m)

### Explicitly Deferred (v2)

- Timezone-aware scheduling (DST is a bug magnet — UTC-only for now)
- Calendar-based exclusions ("skip holidays")
- Maintenance windows
- Per-monitor jitter to avoid thundering herd
- Second-level cron precision (minute-level is sufficient)

---

## Phase 1: Schedule Type + Cron Library

### 1.1 Dependency

Use `github.com/robfig/cron/v3` — the standard Go cron library:
- Parses 5-field cron expressions
- Supports predefined schedules (`@every 5m`, `@hourly`, `@daily`)
- `Schedule.Next(time.Time)` computes next run time

### 1.2 Schedule Type — `internal/watcher/schedule.go`

```go
type Schedule struct {
    Expression string         // Raw expression: "*/5 * * * *" or "5m" or "@daily"
    cronSched  cron.Schedule  // Parsed cron schedule (nil for simple intervals)
    interval   time.Duration  // Parsed interval (zero for cron expressions)
}

func ParseSchedule(expr string) (*Schedule, error)
func (s *Schedule) Next(from time.Time) time.Time
func (s *Schedule) Description() string  // "Every 5 minutes", "Daily at midnight"
func (s *Schedule) IsInterval() bool     // true for "5m", false for cron expressions
```

**Parsing logic:**

1. Try `ParseTimeRange()` first (existing "5m", "1h" format) → interval mode
2. Try `cron.ParseStandard(expr)` → cron mode (5-field)
3. Try predefined (`@every`, `@hourly`, `@daily`, `@weekly`) → cron mode
4. Return **error** if none match (no silent fallback!)

**Next-run logic:**

- Interval mode: `from.Add(interval)` — same as current behavior
- Cron mode: `cronSched.Next(from)` — cron library computes

### 1.3 Tests — `internal/watcher/schedule_test.go`

| Input | Type | Next (from midnight) | Description |
|-------|------|---------------------|-------------|
| `"5m"` | interval | 00:05 | "Every 5 minutes" |
| `"1h"` | interval | 01:00 | "Every hour" |
| `"*/5 * * * *"` | cron | 00:05 | "Every 5 minutes" |
| `"0 9 * * *"` | cron | 09:00 | "Daily at 9:00 AM" |
| `"0 9 * * 1-5"` | cron | next weekday 09:00 | "Weekdays at 9:00 AM" |
| `"@hourly"` | cron | 01:00 | "Every hour" |
| `"@daily"` | cron | next midnight | "Daily at midnight" |
| `"@every 30s"` | cron | 00:00:30 | "Every 30 seconds" |
| `"invalid"` | error | — | — |

---

## Phase 2: Data Layer — Migration + Model Update

### 2.1 Add `schedule` column

Migration `000008_add_schedule.sql`:

```sql
ALTER TABLE watchers ADD COLUMN schedule TEXT NOT NULL DEFAULT '';
```

- Empty string means "use time_range as schedule" (backward compat)
- When `schedule` is set, it controls when the monitor runs
- `time_range` continues to control the log lookback window

### 2.2 Model Update

```go
type Watcher struct {
    // ... existing fields
    Schedule string `json:"schedule,omitempty"` // Cron or interval: "0 9 * * *", "5m", "@daily"
    // TimeRange stays — it's the log lookback window
}
```

### 2.3 Backward Compatibility

- If `schedule` is empty, the executor uses `time_range` for both scheduling and lookback (current behavior)
- If `schedule` is set, it controls when to run; `time_range` controls log lookback
- Existing monitors continue working unchanged

---

## Phase 3: Executor Integration

### 3.1 Update `updateWatcherTiming()`

```go
func (e *Executor) updateWatcherTiming(ctx context.Context, w store.Watcher) {
    now := time.Now()

    // Determine schedule expression: prefer explicit schedule, fall back to time_range
    schedExpr := w.Schedule
    if schedExpr == "" {
        schedExpr = w.TimeRange
    }

    sched, err := ParseSchedule(schedExpr)
    if err != nil {
        // Fallback: 15 minutes
        log.Printf("watcher %s: invalid schedule %q, using 15m fallback: %v", w.ID, schedExpr, err)
        next := now.Add(15 * time.Minute)
        e.watcherStore.UpdateRunTime(ctx, w.ID, now, next)
        return
    }

    next := sched.Next(now)
    e.watcherStore.UpdateRunTime(ctx, w.ID, now, next)
}
```

### 3.2 Missed Run Strategy

When the server starts after being down during a scheduled cron time:
- **Current behavior preserved**: `GetDueWatchers()` returns any watcher where `next_run_at <= now()`, so missed runs execute immediately on startup
- After execution, `Next()` computes the next future time from `now`, skipping all missed windows
- This is the correct behavior: run once to catch up, then resume normal schedule

---

## Phase 4: API, MCP & Dashboard UI

### 4.1 API Validation

When creating/updating a monitor with a `schedule` field:
1. Validate with `ParseSchedule()` — reject invalid expressions with clear error
2. Compute and store initial `next_run_at` using the schedule
3. Return `schedule_description` in responses

### 4.2 API Response Enhancement

```json
{
  "schedule": "0 9 * * 1-5",
  "time_range": "1h",
  "schedule_description": "Weekdays at 9:00 AM",
  "next_run_at": "2025-02-11T09:00:00Z"
}
```

### 4.3 MCP Tool Update

Update `create_monitor` tool:
- Accept `schedule` parameter
- Return `schedule_description` in responses

### 4.4 Dashboard UI

**Rule monitors**: Add "Advanced" toggle that reveals a cron expression input field below the existing dropdown. Show "Next 3 runs" preview when a cron expression is entered.

**AI monitors**: Same — add advanced toggle below the "Run Every" dropdown.

Keep the simple dropdown as default (covers 90%+ of users). The cron input is for power users.

---

## Phase 5: Documentation

Update README to document:
- New `schedule` field in API
- Supported cron expression format
- Examples of common schedules
- Backward compatibility with existing `time_range`-only monitors

---

## File Changes Summary

| File | Change |
|------|--------|
| `internal/watcher/schedule.go` | New — Schedule type, ParseSchedule, Next, Description |
| `internal/watcher/schedule_test.go` | New — comprehensive tests |
| `internal/watcher/executor.go` | Update next-run calculation to use Schedule |
| `internal/store/models.go` | Add Schedule field to Watcher, CreateWatcherParams, UpdateWatcherParams |
| `internal/store/sqlite_migrations/000008_add_schedule.sql` | New — migration |
| `internal/store/watcher_store.go` | Update Create/Update/scan for schedule column |
| `internal/web/watchers.go` | Validate schedule expressions |
| `internal/web/templates/watchers.html` | Advanced schedule picker UI |
| `internal/mcp/server.go` | Accept schedule param in create_monitor |
| `go.mod` | Add `github.com/robfig/cron/v3` |
| `README.md` | Document cron scheduling |
