# Plan 004: Cron Expression Scheduling

## Overview

Replace the current interval-only scheduling (`time_range` as a duration like "5m", "1h") with **cron expression support**, giving users precise control over when monitors run. Supports both simple intervals (backwards compatible) and full cron expressions.

**Effort**: Low | **Impact**: Medium

---

## Current State

- Scheduler polls every `PollInterval` (default 30s) for due watchers
- `GetDueWatchers()` returns watchers where `next_run_at <= now()` and `status = 'active'`
- After execution, `UpdateRunTime()` sets `next_run_at = now() + timeRange`
- `time_range` field is a string like "5m", "15m", "1h", "6h", "24h"
- `parseTimeRange()` converts string to `time.Duration`
- No support for "run at 9am daily" or "run on weekdays only"

---

## Goals

1. Support standard cron expressions (5-field: min hour dom month dow)
2. Support predefined schedules: `@hourly`, `@daily`, `@weekly`
3. Backwards compatible — existing duration strings ("5m", "1h") keep working
4. Next-run calculation uses cron schedule instead of simple addition
5. UI shows human-readable schedule description
6. Timezone-aware scheduling

---

## Phase 1: Cron Library Integration

### 1.1 Dependency

Use `github.com/robfig/cron/v3` — the standard Go cron library:
- Parses 5-field cron expressions
- Supports predefined schedules (`@every 5m`, `@hourly`, `@daily`)
- Timezone support via `cron.WithLocation()`
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
func (s *Schedule) Description() string  // Human-readable: "Every 5 minutes", "Daily at midnight"
```

**Parsing logic:**

1. Try `parseTimeRange()` first (existing "5m", "1h" format) → interval mode
2. Try `cron.ParseStandard(expr)` → cron mode
3. Try predefined (`@every`, `@hourly`, `@daily`, `@weekly`) → cron mode
4. Return error if none match

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
| `"0 9 * * 1-5"` | cron | 09:00 (next weekday) | "Weekdays at 9:00 AM" |
| `"@hourly"` | cron | 01:00 | "Every hour" |
| `"@daily"` | cron | next midnight | "Daily at midnight" |
| `"@every 30s"` | cron | 00:00:30 | "Every 30 seconds" |
| `"invalid"` | error | — | — |

---

## Phase 2: Executor Integration

### 2.1 Update `Executor.updateNextRun()`

Currently:
```go
dur := parseTimeRange(watcher.TimeRange)
nextRun := time.Now().Add(dur)
```

New:
```go
sched, err := ParseSchedule(watcher.TimeRange)
if err != nil {
    // fallback: 15 minutes
    nextRun = time.Now().Add(15 * time.Minute)
} else {
    nextRun = sched.Next(time.Now())
}
```

### 2.2 Timezone Handling

Add optional `timezone` field to watcher/monitor model:

```go
type Watcher struct {
    // ... existing fields
    Timezone string // e.g., "America/New_York", default "UTC"
}
```

- If set, cron expressions are evaluated in that timezone
- Intervals ignore timezone (they're relative)
- Store as TEXT in SQLite, validate against `time.LoadLocation()`

### 2.3 Migration

Add `timezone` column to watchers table:

```sql
-- 000003_add_timezone.sql
ALTER TABLE watchers ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';
```

---

## Phase 3: API & Validation

### 3.1 Create/Update Validation

When creating or updating a monitor:

1. Validate `time_range` with `ParseSchedule()` — reject invalid expressions
2. Validate `timezone` with `time.LoadLocation()` — reject invalid zones
3. Compute and store `next_run_at` using the new schedule

### 3.2 API Response Enhancement

Include parsed schedule info in watcher responses:

```json
{
  "time_range": "0 9 * * 1-5",
  "timezone": "America/New_York",
  "schedule_description": "Weekdays at 9:00 AM (Eastern)",
  "next_run_at": "2025-02-11T14:00:00Z"
}
```

### 3.3 MCP Tool Update

Update `create_monitor` and `preview_monitor` tools:
- Accept `schedule` parameter (alias for `time_range` with better name)
- Accept `timezone` parameter
- Return `schedule_description` in responses

---

## Phase 4: Dashboard UI

### 4.1 Schedule Picker

Replace the current `time_range` dropdown with a **schedule picker**:

**Simple mode** (default):
- Dropdown: Every 1m, 5m, 15m, 30m, 1h, 6h, 12h, 24h
- Same as current behavior

**Advanced mode** (toggle):
- Cron expression input field with real-time preview
- "Next 5 runs" preview showing upcoming execution times
- Predefined shortcuts: "Weekdays at 9am", "Hourly during business hours", "Every Monday at 8am"
- Timezone selector (defaults to browser timezone)

### 4.2 Monitor List Display

Show human-readable schedule in the monitor list:
- "Every 5 minutes" instead of "5m"
- "Weekdays at 9:00 AM ET" instead of "0 9 * * 1-5"
- Next run time with relative display: "Next run: in 23 minutes"

---

## File Changes Summary

| File | Change |
|------|--------|
| `internal/watcher/schedule.go` | New — Schedule type, ParseSchedule, Next, Description |
| `internal/watcher/schedule_test.go` | New — comprehensive tests |
| `internal/watcher/executor.go` | Update next-run calculation to use Schedule |
| `internal/store/store.go` | Add Timezone field to Watcher struct |
| `internal/store/sqlite_migrations/000003_add_timezone.sql` | New — migration |
| `internal/store/sqlite_watcher.go` | Update Create/Update/scan for timezone |
| `internal/web/watchers.go` | Validate schedule expressions, include description in response |
| `internal/web/templates/watchers_form.html` | Schedule picker UI |
| `internal/mcp/server.go` | Accept schedule/timezone params |
| `go.mod` | Add `github.com/robfig/cron/v3` |

---

## Out of Scope

- Calendar-based exclusions (e.g., "skip holidays")
- Maintenance windows (pause all monitors during deployment)
- Per-monitor jitter to avoid thundering herd (future optimization)
- Second-level cron precision (minute-level is sufficient)
