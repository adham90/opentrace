# Plan 007: Monitor Dependency Chains

## Overview

Allow monitors to trigger other monitors when they fire an alert. This is a lightweight form of **runbook automation** — when a high-level monitor detects an issue, it automatically kicks off deeper diagnostic monitors.

**Effort**: Medium | **Impact**: Medium

---

## Current State

- Monitors run independently on their own schedules
- No concept of one monitor triggering another
- When an alert fires, users must manually investigate or set up separate monitors hoping they coincide
- Plan 006 (Adaptive Scheduling) provides state-aware scheduling but no inter-monitor coordination

---

## Use Cases

- **Connection Saturation** fires → trigger **Active Query Analysis** (what queries are using all the connections?)
- **Replication Lag** fires → trigger **Write-Heavy Table Check** (which tables are generating the most WAL?)
- **Disk Usage Alert** fires → trigger **Table Bloat Analysis** (which tables need vacuuming?)
- **Slow Query Spike** fires → trigger **Lock Contention Check** (are queries blocking each other?)

---

## Goals

1. A monitor can specify a list of other monitors to trigger on alert
2. Triggered monitors run immediately (bypassing their normal schedule)
3. Chain depth is limited to prevent infinite loops
4. Triggered runs are clearly marked in the run history
5. Configuration via API, dashboard UI, and MCP

---

## Phase 1: Data Model

### 1.1 Migration — `000010_trigger_monitor_ids.up.sql`

```sql
ALTER TABLE watchers ADD COLUMN trigger_monitor_ids TEXT;  -- JSON array of UUID strings
```

### 1.2 Watcher Model Update

```go
type Watcher struct {
    // ... existing fields
    TriggerMonitorIDs []string `json:"trigger_monitor_ids,omitempty"`
}
```

### 1.3 Watcher Run Model Update

Add `triggered_by` to track provenance:

```go
type WatcherRun struct {
    // ... existing fields
    TriggeredBy string `json:"triggered_by,omitempty"` // ID of the monitor that triggered this run
}
```

Column addition (same migration):

```sql
ALTER TABLE watcher_runs ADD COLUMN triggered_by TEXT;  -- UUID of triggering monitor
```

---

## Phase 2: Trigger Execution

### 2.1 Trigger Context

Use a typed context key (not a string) to track chain depth:

```go
// internal/watcher/trigger.go

type triggerDepthKey struct{}

func TriggerDepth(ctx context.Context) int {
    if v, ok := ctx.Value(triggerDepthKey{}).(int); ok {
        return v
    }
    return 0
}

func WithTriggerDepth(ctx context.Context, depth int) context.Context {
    return context.WithValue(ctx, triggerDepthKey{}, depth)
}

const MaxTriggerDepth = 3
```

### 2.2 Trigger Dispatch — Bounded Execution

Instead of fire-and-forget goroutines, use a bounded worker pool to prevent resource exhaustion:

```go
// internal/watcher/trigger.go

type TriggerDispatcher struct {
    executor     *Executor
    watcherStore store.WatcherStore
    sem          chan struct{} // bounded concurrency
}

func NewTriggerDispatcher(executor *Executor, ws store.WatcherStore, maxConcurrent int) *TriggerDispatcher {
    return &TriggerDispatcher{
        executor:     executor,
        watcherStore: ws,
        sem:          make(chan struct{}, maxConcurrent),
    }
}

func (d *TriggerDispatcher) DispatchTriggers(ctx context.Context, source *store.Watcher) {
    depth := TriggerDepth(ctx)
    if depth >= MaxTriggerDepth {
        log.Printf("Trigger chain depth limit reached for monitor %s", source.Title)
        return
    }

    for _, targetID := range source.TriggerMonitorIDs {
        if targetID == source.ID.String() {
            continue // self-trigger prevention
        }

        target, err := d.watcherStore.GetByID(ctx, uuid.MustParse(targetID))
        if err != nil || target.Status != store.WatcherActive {
            continue
        }

        childCtx := WithTriggerDepth(ctx, depth+1)

        select {
        case d.sem <- struct{}{}:
            go func(t store.Watcher) {
                defer func() { <-d.sem }()
                d.executor.ExecuteTriggered(childCtx, t, source.ID.String())
            }(target)
        case <-ctx.Done():
            return
        }
    }
}
```

### 2.3 Integration with Executor

In `executor.go`, after alert creation:

```go
// After alert is created
if len(w.TriggerMonitorIDs) > 0 {
    triggerDispatcher.DispatchTriggers(ctx, w)
}
```

New method on Executor:

```go
func (e *Executor) ExecuteTriggered(ctx context.Context, w store.Watcher, triggeredBy string) {
    // Same as Execute but sets TriggeredBy on the run record
    run := store.WatcherRun{
        // ... normal fields
        TriggeredBy: triggeredBy,
    }
    // ... rest of execution
}
```

---

## Phase 3: Safeguards

| Safeguard | Implementation |
|-----------|---------------|
| Max chain depth | `MaxTriggerDepth = 3`, checked via context |
| No self-triggering | Skip if `targetID == source.ID` |
| Only active monitors | Skip if `target.Status != WatcherActive` |
| Bounded concurrency | Semaphore channel (default: 5 concurrent triggered runs) |
| Context cancellation | Respects server shutdown via parent context |
| Provenance tracking | `triggered_by` field on run record |

### 3.1 Validation on Save

When creating/updating a monitor's `trigger_monitor_ids`:

- Reject if any ID equals the monitor's own ID
- Reject if any referenced monitor doesn't exist
- Warn (but allow) if a cycle is detected (A triggers B triggers A) — the depth limit prevents infinite loops, but the user should be aware

---

## Phase 4: Dashboard UI

### 4.1 Monitor Edit Form

Add a "Trigger monitors on alert" multi-select field:
- Lists all other active monitors
- Shows monitor title + connector name for disambiguation
- Selected monitors displayed as removable chips

### 4.2 Run History

Triggered runs show provenance:

```
[10:05] ▶ Connection Saturation — ALERT (92/100 connections)
[10:05]   └── Triggered: Active Query Analysis
[10:05]   └── Triggered: Connection Source Breakdown
[10:06] ▶ Active Query Analysis (triggered by Connection Saturation) — ALERT
[10:06]   └── Triggered: Lock Contention Check
[10:06] ▶ Connection Source Breakdown (triggered by Connection Saturation) — OK
```

### 4.3 Dependency Visualization (stretch)

Optional: show a simple directed graph of monitor trigger relationships on the watchers list page. Can be deferred.

---

## Phase 5: MCP Integration

### 5.1 Update `create_watcher` / `list_watchers`

Include `trigger_monitor_ids` in watcher creation and listing:

```json
{
  "id": "...",
  "title": "Connection Saturation",
  "trigger_monitor_ids": ["uuid-of-active-query-analysis"],
  "last_run": {
    "triggered_by": null
  }
}
```

### 5.2 AI-Driven Chain Suggestions

The AI can suggest trigger chains based on alert context:

- "Connection Saturation fired. You have an Active Query Analysis monitor — would you like me to set it up as an automatic trigger?"

---

## File Changes Summary

| File | Change |
|------|--------|
| `internal/watcher/trigger.go` | New — TriggerDispatcher, depth tracking, bounded execution |
| `internal/watcher/trigger_test.go` | New — chain depth limits, self-trigger prevention, concurrency |
| `internal/watcher/executor.go` | Add ExecuteTriggered, integrate trigger dispatch after alerts |
| `internal/store/store.go` | Add TriggerMonitorIDs to Watcher, TriggeredBy to WatcherRun |
| `internal/store/sqlite_watcher.go` | Update CRUD for trigger_monitor_ids column, scan triggered_by |
| `internal/store/sqlite_migrations/000010_trigger_monitor_ids.up.sql` | New — migration |
| `internal/web/watchers.go` | Validate trigger_monitor_ids on save, include in responses |
| `internal/web/templates/watchers_form.html` | Multi-select trigger monitors field |
| `internal/web/templates/watchers_run_history.html` | Show triggered_by provenance |
| `internal/mcp/server.go` | Include trigger_monitor_ids in create/list watchers |

---

## Dependencies

- Plan 006 (Adaptive Scheduling) — should be implemented first so triggered monitors can benefit from escalation
- Executor must be initialized before TriggerDispatcher

---

## Out of Scope

- Conditional triggers (only trigger B if A's alert matches certain criteria)
- Trigger delays (wait N seconds before triggering)
- Trigger on resolution (trigger when an alert clears, not just when it fires)
- Cross-connector triggers (trigger a monitor on a different database)
- Automatic chain suggestion via LLM analysis
