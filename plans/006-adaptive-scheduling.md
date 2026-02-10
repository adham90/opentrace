# Plan 006: Adaptive Scheduling

## Overview

Make the monitor scheduler **context-aware** — automatically increase polling frequency when anomalies are detected and decrease it during stable periods. This reduces noise during calm periods while providing rapid feedback when something goes wrong.

**Effort**: High | **Impact**: Medium

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
4. Dependency chains: trigger monitor B when monitor A fires
5. Backoff on repeated failures (error state)
6. All transitions logged for auditability

---

## Adaptive Scheduling Model

### State Machine

Each monitor has an **adaptive state** that affects scheduling:

```
         alert fired           N consecutive clean runs
NORMAL ──────────────► ESCALATED ─────────────────────► NORMAL
   │                      │
   │  M clean runs        │  still alerting
   │  (if cool-down on)   │  after max_escalation_duration
   ▼                      ▼
RELAXED ◄────────────  ESCALATED
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
                      ERROR (paused, needs manual intervention)
```

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
    RelaxedInterval      string        `json:"relaxed_interval"`      // e.g., "2h" — run less often when stable
    RelaxAfterRuns       int           `json:"relax_after_runs"`      // consecutive clean runs before relaxing (default: 20)

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
| `backoff_multiplier` | 2.0 |
| `max_backoff_interval` | 1 hour |
| `max_consecutive_errors` | 5 |

---

## Phase 1: Data Model

### 1.1 Migration — `000005_adaptive_scheduling.sql`

```sql
ALTER TABLE watchers ADD COLUMN adaptive_config TEXT;  -- JSON AdaptiveConfig
ALTER TABLE watchers ADD COLUMN adaptive_state TEXT NOT NULL DEFAULT 'normal';  -- normal | escalated | relaxed | backing_off
ALTER TABLE watchers ADD COLUMN consecutive_clean_runs INTEGER NOT NULL DEFAULT 0;
ALTER TABLE watchers ADD COLUMN consecutive_errors INTEGER NOT NULL DEFAULT 0;
ALTER TABLE watchers ADD COLUMN escalated_at TEXT;  -- timestamp when escalation started
ALTER TABLE watchers ADD COLUMN base_time_range TEXT;  -- original time_range before adaptive override
```

### 1.2 Store Updates

Add to `WatcherStore`:

```go
UpdateAdaptiveState(ctx context.Context, id uuid.UUID, params UpdateAdaptiveParams) error

type UpdateAdaptiveParams struct {
    AdaptiveState       string
    ConsecutiveCleanRuns int
    ConsecutiveErrors    int
    EscalatedAt         *time.Time
    TimeRange           string  // current effective time_range
}
```

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
    IF state != escalated:
        → state=escalated, interval=escalated_interval, set escalated_at=now
    ELSE IF now - escalated_at > escalation_duration:
        → state=normal, interval=base_interval (escalation expired)
    ELSE:
        → stay escalated

IF NOT outcome.Alerted AND NOT outcome.Errored:
    consecutive_clean_runs++
    consecutive_errors = 0
    IF state == escalated AND consecutive_clean_runs >= cooldown_runs:
        → state=normal, interval=base_interval
    IF state == normal AND relax_enabled AND consecutive_clean_runs >= relax_after_runs:
        → state=relaxed, interval=relaxed_interval
    IF state == backing_off:
        → state=normal, interval=base_interval (successful run clears backoff)
```

### 2.2 Integration with Executor

After each run completes in `executor.go`:

```go
// After execution
outcome := RunOutcome{
    Alerted: result.HasAlert,
    Errored: runErr != nil,
}

if watcher.AdaptiveConfig != nil && watcher.AdaptiveConfig.Enabled {
    transition := adaptiveEngine.Transition(watcher, outcome)

    // Apply transition
    watcherStore.UpdateAdaptiveState(ctx, watcher.ID, UpdateAdaptiveParams{
        AdaptiveState:        transition.NewState,
        ConsecutiveCleanRuns: ...,
        ConsecutiveErrors:    ...,
        ...
    })

    // Use transition.NewInterval for next_run_at calculation
    effectiveInterval = transition.NewInterval

    if transition.ShouldPause {
        watcherStore.UpdateStatus(ctx, watcher.ID, WatcherError)
    }

    // Log transition
    log.Printf("Monitor %s: %s", watcher.Title, transition.LogMessage)
}
```

### 2.3 Tests — `internal/watcher/adaptive_test.go`

- Normal → Escalated on alert
- Escalated → Normal after N clean runs
- Escalated → Normal after escalation duration expires
- Normal → Relaxed after M clean runs (if enabled)
- Relaxed → Escalated on alert (immediate)
- Any → Backing off on error
- Backing off → Normal on success
- Backing off → Error (paused) after max errors
- Backoff interval calculation with multiplier
- Backoff interval capped at max
- Disabled config → no transitions

---

## Phase 3: Dependency Chains

### 3.1 Concept

Allow monitors to trigger other monitors when they fire:

- Monitor A (Connection Saturation) fires → trigger Monitor B (Active Query Analysis)
- Monitor C (Replication Lag) fires → trigger Monitor D (Write-Heavy Table Check)

This is a lightweight form of runbook automation.

### 3.2 Data Model

Add to monitor config:

```go
type Watcher struct {
    // ... existing
    TriggerMonitorIDs []string `json:"trigger_monitor_ids,omitempty"` // IDs of monitors to run when this fires
}
```

Column addition:
```sql
ALTER TABLE watchers ADD COLUMN trigger_monitor_ids TEXT; -- JSON array of UUIDs
```

### 3.3 Implementation

In executor, after alert creation:

```go
if len(watcher.TriggerMonitorIDs) > 0 {
    for _, targetID := range watcher.TriggerMonitorIDs {
        target, err := watcherStore.GetByID(ctx, targetID)
        if err != nil || target.Status != WatcherActive {
            continue
        }
        // Queue for immediate execution
        go executor.Execute(ctx, *target)
    }
}
```

### 3.4 Safeguards

- Max chain depth: 3 (prevent infinite loops)
- Track chain via context: `ctx = context.WithValue(ctx, "trigger_depth", depth+1)`
- A monitor cannot trigger itself
- Triggered runs are marked with `triggered_by` in the run record

### 3.5 UI

In monitor edit form, add "Trigger monitors" multi-select field listing other active monitors.

---

## Phase 4: Dashboard Visibility

### 4.1 Adaptive State Display

Show adaptive state in the monitor list:

- **Normal**: no badge
- **Escalated**: red pulsing badge "Escalated (checking every 1m)"
- **Relaxed**: blue badge "Relaxed (checking every 2h)"
- **Backing off**: yellow badge "Backing off (next check in 8m)"
- **Error (paused)**: red badge "Paused — 5 consecutive errors"

### 4.2 State History

Log adaptive state transitions as events viewable in the monitor's run history:

```
[10:05] State: normal → escalated (alert fired: 92 connections)
[10:06] Running (escalated: 1m interval)
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
- Relaxed interval
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

---

## File Changes Summary

| File | Change |
|------|--------|
| `internal/watcher/adaptive.go` | New — AdaptiveEngine, state transitions |
| `internal/watcher/adaptive_test.go` | New — comprehensive state machine tests |
| `internal/watcher/executor.go` | Integrate adaptive transitions after each run |
| `internal/store/store.go` | Add AdaptiveConfig, UpdateAdaptiveParams, trigger fields |
| `internal/store/sqlite_watcher.go` | Update CRUD for new columns |
| `internal/store/sqlite_migrations/000005_adaptive_scheduling.sql` | New — migration |
| `internal/web/watchers.go` | Include adaptive state in responses, validate config |
| `internal/web/templates/watchers_form.html` | Adaptive config UI section |
| `internal/web/templates/watchers_list.html` | Adaptive state badges |
| `internal/mcp/server.go` | Include adaptive state in list_monitors |

---

## Dependencies

- Plan 004 (Cron Scheduling) — uses `ParseSchedule` for interval computation

---

## Out of Scope

- Machine learning-based anomaly detection for adaptive thresholds
- Cross-monitor correlation (if monitors A and B both escalate, something bigger is wrong)
- Automatic remediation actions (run a query to kill connections, etc.)
- SLA-based scheduling (ensure X checks per hour minimum)
