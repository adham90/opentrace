# Plan 006: Adaptive Scheduling

## Overview

Make the monitor scheduler **context-aware** — automatically increase polling frequency when anomalies are detected and decrease it during stable periods. This reduces noise during calm periods while providing rapid feedback when something goes wrong.

**Effort**: Medium-High | **Impact**: Medium

---

## Current State

- Monitors run on a fixed schedule (interval or cron expression from Plan 004)
- No concept of "escalation" or "cool-down"
- A monitor that fires an alert keeps running at the same frequency
- A monitor that hasn't fired in weeks also runs at the same frequency
- `next_run_at` is computed as `now + interval` after each run
- Scheduler calls `GetDueWatchers()` every 30s to find due monitors

---

## Goals

1. Escalation: when a monitor fires an alert, temporarily increase its frequency
2. Cool-down: when a monitor has been quiet, optionally reduce its frequency
3. Configurable per monitor (opt-in, not forced)
4. Backoff on repeated failures (error state)
5. Resume capability for paused monitors
6. All transitions logged for auditability

> **Note:** Dependency chains (trigger monitor B when monitor A fires) are covered separately in [Plan 007](./007-dependency-chains.md).

---

## Adaptive Scheduling Model

### Applicability: Interval vs Cron Monitors

Adaptive scheduling behaves differently depending on monitor type:

- **Interval-based monitors** (`time_range` only, no `schedule`): Full support — escalation changes the interval, relaxation widens it.
- **Cron-based monitors** (`schedule` field set): Adaptive scheduling controls **whether the monitor executes** at its scheduled time, not when it runs.
  - **Escalated**: An additional interval-based check runs *between* cron ticks (e.g., cron fires at 9am, but escalation adds a 1m interval check until cool-down).
  - **Relaxed**: The monitor **skips** its next N scheduled cron ticks (configurable via `relax_skip_runs`, default 1). This means a daily monitor might run every other day when relaxed.
  - **Backing off / Error**: Same as interval — backoff delays or pauses the monitor.

### State Machine

Each monitor has an **adaptive state** that affects scheduling:

```
         alert fired           N consecutive clean runs
NORMAL ──────────────► ESCALATED ─────────────────────► NORMAL
   │                      │
   │  M clean runs        │  still alerting
   │  (if cool-down on)   │  after max_escalation_duration
   ▼                      ▼
RELAXED              SUSTAINED (normal interval, alerts continue)
   │
   │ alert fired
   ▼
ESCALATED
```

Additionally, on execution errors:

```
         exec error           successful run
ANY ────────────────► BACKING_OFF ──────────────► NORMAL
                         │
                         │ repeated errors
                         ▼
                      ERROR (paused, needs manual resume)
```

**Key difference from initial design:** When escalation duration expires but the monitor is still alerting, it transitions to **SUSTAINED** state (normal interval) rather than back to NORMAL. This avoids pretending things are fine while alerts are still firing. SUSTAINED returns to NORMAL only after `cooldown_runs` consecutive clean runs.

### Configuration Per Monitor

```go
type AdaptiveConfig struct {
    Enabled              bool          `json:"enabled"`

    // Escalation
    EscalatedInterval    string        `json:"escalated_interval"`    // e.g., "1m" — run every minute when alerting
    EscalationDuration   string        `json:"escalation_duration"`   // e.g., "30m" — max time in escalated state
    CooldownRuns         int           `json:"cooldown_runs"`         // consecutive clean runs before returning to normal (default: 3)

    // Relaxation (optional)
    RelaxEnabled         bool          `json:"relax_enabled"`
    RelaxedInterval      string        `json:"relaxed_interval"`      // e.g., "2h" — run less often when stable (interval monitors)
    RelaxAfterRuns       int           `json:"relax_after_runs"`      // consecutive clean runs before relaxing (default: 20)
    RelaxSkipRuns        int           `json:"relax_skip_runs"`       // cron monitors: skip N scheduled runs when relaxed (default: 1)

    // Error backoff
    BackoffMultiplier    float64       `json:"backoff_multiplier"`    // e.g., 2.0 — double interval on each error
    MaxBackoffInterval   string        `json:"max_backoff_interval"`  // e.g., "1h" — cap backoff
    MaxConsecutiveErrors int           `json:"max_consecutive_errors"` // e.g., 5 — pause monitor after N errors
}
```

**Defaults** (when `enabled: true` but no custom values):

| Setting | Default |
|---------|---------|
| `escalated_interval` | 1/4 of normal interval (min 1m) |
| `escalation_duration` | 30 minutes |
| `cooldown_runs` | 3 |
| `relax_enabled` | false |
| `relax_skip_runs` | 1 |
| `backoff_multiplier` | 2.0 |
| `max_backoff_interval` | 1 hour |
| `max_consecutive_errors` | 5 |

---

## Phase 1: Data Model

### 1.1 Migration — `000009_adaptive_scheduling.up.sql`

```sql
ALTER TABLE watchers ADD COLUMN adaptive_config TEXT;  -- JSON AdaptiveConfig
ALTER TABLE watchers ADD COLUMN adaptive_state TEXT NOT NULL DEFAULT 'normal';  -- normal | escalated | sustained | relaxed | backing_off | error
ALTER TABLE watchers ADD COLUMN consecutive_clean_runs INTEGER NOT NULL DEFAULT 0;
ALTER TABLE watchers ADD COLUMN consecutive_errors INTEGER NOT NULL DEFAULT 0;
ALTER TABLE watchers ADD COLUMN escalated_at TEXT;  -- timestamp when escalation started
ALTER TABLE watchers ADD COLUMN base_time_range TEXT;  -- original time_range before adaptive override
```

### 1.2 Store Updates

Add to `WatcherStore`:

```go
UpdateAdaptiveState(ctx context.Context, id uuid.UUID, params UpdateAdaptiveParams) error
ResumeMonitor(ctx context.Context, id uuid.UUID) error

type UpdateAdaptiveParams struct {
    AdaptiveState        string
    ConsecutiveCleanRuns int
    ConsecutiveErrors    int
    EscalatedAt          *time.Time
    TimeRange            string  // current effective time_range
}
```

**Concurrency note:** SQLite with `MaxOpenConns=1` naturally serializes writes, so read-modify-write on counters is safe. However, `UpdateAdaptiveState` should use atomic SQL increments where possible (`SET consecutive_clean_runs = consecutive_clean_runs + 1`) to make the intent explicit and be resilient if the connection pool configuration changes in the future.

### 1.3 Watcher Model Update

```go
type Watcher struct {
    // ... existing fields
    AdaptiveConfig       *AdaptiveConfig `json:"adaptive_config,omitempty"`
    AdaptiveState        string          `json:"adaptive_state"`
    ConsecutiveCleanRuns int             `json:"consecutive_clean_runs"`
    ConsecutiveErrors    int             `json:"consecutive_errors"`
    EscalatedAt          *time.Time      `json:"escalated_at,omitempty"`
    BaseTimeRange        string          `json:"base_time_range,omitempty"`
}
```

---

## Phase 2: Adaptive Logic

### 2.1 State Transition Engine — `internal/watcher/adaptive.go`

```go
type AdaptiveEngine struct{}

type RunOutcome struct {
    Alerted bool
    Errored bool
}

func (e *AdaptiveEngine) Transition(w *store.Watcher, outcome RunOutcome) AdaptiveTransition

type AdaptiveTransition struct {
    NewState        string
    NewInterval     string        // effective interval to use for next_run_at
    NewTimeRange    string        // effective time_range for log lookback
    ResetCounters   bool          // reset consecutive counters
    ShouldPause     bool          // pause monitor (max errors reached)
    LogMessage      string        // human-readable transition description
}
```

**Transition rules:**

```
IF adaptive not enabled:
    → no change

IF outcome.Errored:
    consecutive_errors++
    IF consecutive_errors >= max_consecutive_errors:
        → state=error, pause monitor
    ELSE:
        backoff_interval = base_interval * (backoff_multiplier ^ consecutive_errors)
        capped at max_backoff_interval
        → state=backing_off, interval=backoff_interval

IF outcome.Alerted:
    consecutive_clean_runs = 0
    IF state == sustained:
        → stay sustained (already past escalation, normal interval, alerts still flowing)
    ELSE IF state != escalated:
        → state=escalated, interval=escalated_interval, time_range=escalated_interval, set escalated_at=now
    ELSE IF now - escalated_at > escalation_duration:
        → state=sustained, interval=base_interval, time_range=base_time_range
           (stop rapid-fire polling, but don't pretend things are fine)
    ELSE:
        → stay escalated

IF NOT outcome.Alerted AND NOT outcome.Errored:
    consecutive_clean_runs++
    consecutive_errors = 0
    IF state == escalated AND consecutive_clean_runs >= cooldown_runs:
        → state=normal, interval=base_interval, time_range=base_time_range
    IF state == sustained AND consecutive_clean_runs >= cooldown_runs:
        → state=normal, interval=base_interval, time_range=base_time_range
    IF state == normal AND relax_enabled AND consecutive_clean_runs >= relax_after_runs:
        → state=relaxed, interval=relaxed_interval, time_range=relaxed_interval
    IF state == backing_off:
        → state=normal, interval=base_interval, time_range=base_time_range
```

### 2.2 Time Range Adaptation

The `time_range` (log lookback window) must adapt alongside the interval to avoid re-alerting on the same log entries:

| State | Interval | time_range |
|-------|----------|------------|
| Normal | base interval | base time_range (original) |
| Escalated | escalated_interval (e.g., 1m) | escalated_interval (e.g., 1m) |
| Sustained | base interval | base time_range |
| Relaxed | relaxed_interval (e.g., 2h) | relaxed_interval (e.g., 2h) |
| Backing off | backoff_interval | base time_range |

On first escalation, `base_time_range` is saved so it can be restored later.

### 2.3 Integration with Executor

After each run completes in `executor.go`:

```go
// After execution
outcome := RunOutcome{
    Alerted: result.HasAlert,
    Errored: runErr != nil,
}

if w.AdaptiveConfig != nil && w.AdaptiveConfig.Enabled {
    transition := adaptiveEngine.Transition(w, outcome)

    // Apply transition using atomic updates where possible
    watcherStore.UpdateAdaptiveState(ctx, w.ID, UpdateAdaptiveParams{
        AdaptiveState:        transition.NewState,
        ConsecutiveCleanRuns: ...,
        ConsecutiveErrors:    ...,
        TimeRange:            transition.NewTimeRange,
        ...
    })

    // Use transition.NewInterval for next_run_at calculation
    effectiveInterval = transition.NewInterval

    if transition.ShouldPause {
        watcherStore.UpdateStatus(ctx, w.ID, WatcherError)
    }

    // Log transition
    log.Printf("Monitor %s: %s", w.Title, transition.LogMessage)
}
```

### 2.4 Tests — `internal/watcher/adaptive_test.go`

- Normal → Escalated on alert
- Escalated → Normal after N clean runs
- Escalated → Sustained after escalation duration expires (still alerting)
- Sustained → Normal after N clean runs
- Sustained stays Sustained while still alerting
- Normal → Relaxed after M clean runs (if enabled)
- Relaxed → Escalated on alert (immediate)
- Any → Backing off on error
- Backing off → Normal on success
- Backing off → Error (paused) after max errors
- Backoff interval calculation with multiplier
- Backoff interval capped at max
- Disabled config → no transitions
- Time range adapts with each state transition
- Cron-based monitor: relaxation skips scheduled runs

---

## Phase 3: Resume Paused Monitors

When a monitor hits `max_consecutive_errors` and enters the `error` state, it is paused and needs manual intervention.

### 3.1 API Endpoint

```
POST /api/watchers/{id}/resume
```

Behavior:
- Sets `adaptive_state` back to `normal`
- Resets `consecutive_errors` to 0
- Resets `consecutive_clean_runs` to 0
- Sets `next_run_at` to now (run immediately)
- Returns 200 with the updated watcher

Validation:
- Only monitors in `error` state can be resumed (return 409 otherwise)

### 3.2 Store Method

```go
func (s *SQLiteWatcherStore) ResumeMonitor(ctx context.Context, id uuid.UUID) error {
    _, err := s.db.ExecContext(ctx, `
        UPDATE watchers
        SET adaptive_state = 'normal',
            consecutive_errors = 0,
            consecutive_clean_runs = 0,
            escalated_at = NULL,
            next_run_at = ?,
            status = 'active'
        WHERE id = ? AND adaptive_state = 'error'`,
        time.Now().UTC().Format(time.RFC3339), id.String())
    return err
}
```

### 3.3 Dashboard UI

On monitors in `error` state, show a "Resume" button that calls the resume endpoint.

---

## Phase 4: Dashboard Visibility

### 4.1 Adaptive State Display

Show adaptive state in the monitor list:

- **Normal**: no badge
- **Escalated**: red pulsing badge "Escalated (checking every 1m)"
- **Sustained**: orange badge "Sustained alert (normal interval)"
- **Relaxed**: blue badge "Relaxed (checking every 2h)"
- **Backing off**: yellow badge "Backing off (next check in 8m)"
- **Error (paused)**: red badge "Paused — 5 consecutive errors" + **Resume** button

### 4.2 State History

Log adaptive state transitions as events viewable in the monitor's run history:

```
[10:05] State: normal → escalated (alert fired: 92 connections)
[10:06] Running (escalated: 1m interval, 1m lookback)
[10:07] Running (escalated: 1m interval) — clean
[10:08] Running (escalated: 1m interval) — clean
[10:09] Running (escalated: 1m interval) — clean
[10:09] State: escalated → normal (3 consecutive clean runs)
```

### 4.3 Configuration UI

In monitor create/edit form, add collapsible "Adaptive Scheduling" section:

- Toggle: Enable adaptive scheduling
- Escalation interval (with "auto" default = 1/4 of normal)
- Escalation duration
- Cooldown runs
- Toggle: Enable relaxation
- Relaxed interval (or "skip N runs" for cron monitors)
- Relax after N clean runs

---

## Phase 5: MCP Integration

### 5.1 Monitor Status in MCP

Update `list_monitors` to include adaptive state:

```json
{
  "id": "...",
  "title": "Connection Saturation",
  "adaptive_state": "escalated",
  "effective_interval": "1m",
  "consecutive_clean_runs": 1,
  "next_run_at": "2025-02-10T10:06:00Z"
}
```

### 5.2 AI Recommendations

The AI can use adaptive state information:

- "Connection Saturation monitor has been in escalated state for 25 minutes. The issue persists. Would you like me to investigate the connection sources?"
- "All monitors have been relaxed for 3 days — your database looks healthy."
- "Replication Lag monitor is paused after 5 consecutive errors. The target database may be unreachable. Would you like me to resume it?"

---

## File Changes Summary

| File | Change |
|------|--------|
| `internal/watcher/adaptive.go` | New — AdaptiveEngine, state transitions, time_range adaptation |
| `internal/watcher/adaptive_test.go` | New — comprehensive state machine tests (14 cases) |
| `internal/watcher/executor.go` | Integrate adaptive transitions after each run |
| `internal/store/store.go` | Add AdaptiveConfig, UpdateAdaptiveParams, ResumeMonitor |
| `internal/store/sqlite_watcher.go` | Update CRUD for new columns, atomic counter updates |
| `internal/store/sqlite_migrations/000009_adaptive_scheduling.up.sql` | New — migration |
| `internal/web/watchers.go` | Include adaptive state in responses, validate config, resume endpoint |
| `internal/web/templates/watchers_form.html` | Adaptive config UI section |
| `internal/web/templates/watchers_list.html` | Adaptive state badges + resume button |
| `internal/mcp/server.go` | Include adaptive state in list_monitors |

---

## Dependencies

- Plan 004 (Cron Scheduling) — uses `ParseSchedule` for interval computation

---

## Design Decisions

1. **JSON column for `adaptive_config`**: Stored as TEXT with JSON. This is simple and sufficient for CRUD operations. If we later need to query across monitors by config values (e.g., "all monitors with backoff > 2x"), we'd need SQLite JSON extraction functions. Acceptable trade-off for now.

2. **SQLite serialization for counters**: With `MaxOpenConns=1`, writes are naturally serialized. We still use atomic SQL increments (`consecutive_clean_runs + 1`) for correctness if this changes. Documented here for future reference.

3. **SUSTAINED state**: Added to handle the edge case where escalation duration expires but the monitor is still alerting. Without this, the monitor would return to normal and appear healthy while still firing alerts.

---

## Out of Scope

- Dependency chains / trigger monitors (see [Plan 007](./007-dependency-chains.md))
- Machine learning-based anomaly detection for adaptive thresholds
- Cross-monitor correlation (if monitors A and B both escalate, something bigger is wrong)
- Automatic remediation actions (run a query to kill connections, etc.)
- SLA-based scheduling (ensure X checks per hour minimum)
