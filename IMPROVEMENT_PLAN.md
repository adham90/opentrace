# OpenTrace Improvement Plan

Findings from a full code review of the codebase. Organized by priority.

---

## Fix Now (bugs and security)

### 1. Proxy Auth Fails Open

**Problem:** When user creation fails, the request continues unauthenticated instead of being rejected.

**Location:** `internal/api/proxy_auth.go:40-43`

```go
if err != nil {
    slog.Error("proxy auth: failed to create user", "email", email, "error", err)
    next.ServeHTTP(w, r) // ← continues without auth context
    return
}
```

**Fix:** Return HTTP 500 instead of calling `next.ServeHTTP`. If we can't identify the user, reject the request.

```go
if err != nil {
    slog.Error("proxy auth: failed to create user", "email", email, "error", err)
    server.WriteError(w, http.StatusInternalServerError, "failed to establish user identity")
    return
}
```

**Risk if ignored:** Any request with an `X-Forwarded-User` header proceeds unauthenticated when the user store is down or broken.

---

### 2. Log Level Case Inconsistency (metric bug)

**Problem:** The ingest handler accepts both `"error"` and `"ERROR"` and stores them as-is. Downstream code is inconsistent about which case it checks:

| Code | Checks |
|------|--------|
| `watcher/watch_metrics.go:62` | `"error"`, `"fatal"` (lowercase only) |
| `notifications/deploy_watcher.go:145` | `"ERROR"`, `"FATAL"` (uppercase only) |
| `domain/overview/service.go:154` | both cases |
| `domain/logs/service.go:88` | both cases |

**Location:** `internal/ingest/handler.go:163-168` (validation accepts both cases but doesn't normalize)

**Fix:** Normalize levels to lowercase at ingest time, right after validation passes. Add one line after the validation loop:

```go
entries[i].Level = strings.ToLower(entries[i].Level)
```

Then clean up all the dual-case checks downstream — they can all assume lowercase.

**Risk if ignored:** WatchMetrics returns 0 for error rate if the client sends uppercase levels. DeployWatcher misses errors if the client sends lowercase. Silent metric corruption.

---

### 3. Notification Dispatcher Race Condition

**Problem:** `AddSender` appends to `d.senders` without a lock. `Notify` iterates the same slice concurrently. This is a data race.

**Location:** `internal/mcp/notifications/dispatcher.go:67-70` and `:96`

**Fix:** Add a `sync.RWMutex` to `Dispatcher`. Use `RLock` in `Notify`, `Lock` in `AddSender`.

```go
type Dispatcher struct {
    mu      sync.RWMutex
    senders []Sender
}

func (d *Dispatcher) AddSender(s Sender) {
    if s != nil {
        d.mu.Lock()
        d.senders = append(d.senders, s)
        d.mu.Unlock()
    }
}

func (d *Dispatcher) Notify(n Notification) {
    d.mu.RLock()
    senders := d.senders
    d.mu.RUnlock()
    // ... dispatch to senders
}
```

**Risk if ignored:** Race detector will catch it. In production, corrupted slice reads can cause panics or missed notifications.

---

## Fix Soon (missing behavior, resource issues)

### 4. WatchStream Doesn't Send Notifications

**Problem:** The scheduler dispatches alerts to notifiers (webhooks, Telegram). The stream evaluator creates alerts in the DB but never notifies anyone. Stream-triggered alerts are invisible.

**Location:** `internal/watcher/watch_stream.go:149-161` — creates alert, no notification dispatch.
Compare with `internal/watcher/watch_scheduler.go:201-203` — calls `NotifyAllWatchAlert`.

**Fix:** Add a `notifiers []WatchAlertNotifier` field to `WatchStreamEvaluator` and call `NotifyAllWatchAlert` after creating an alert, same as the scheduler does.

Changes needed:
- Add `Notifiers []WatchAlertNotifier` to `WatchStreamEvaluator` struct
- Accept notifiers in `NewWatchStreamEvaluator`
- After the `CreateAlert` call in `evaluateOne`, add: `NotifyAllWatchAlert(ctx, s.notifiers, alert, w)`
- Update wiring in `main.go` to pass notifiers

**Risk if ignored:** Users relying on stream-triggered alerts (reactive, on log ingestion) never get notified. Only the 15-second poll cycle sends notifications.

---

### 5. DeployWatcher Goroutine Leak

**Problem:** Each deploy spawns a 30-minute observation goroutine with no tracking. `time.After(30*time.Minute)` allocates a timer that can't be GC'd until it fires. No way to stop these goroutines on shutdown.

**Location:** `internal/mcp/notifications/deploy_watcher.go:45-52`

```go
go w.observe(ctx, deploy, baseline)  // untracked goroutine

// inside observe():
timeout := time.After(30 * time.Minute)  // leaks until fired
```

**Fix:** Replace `time.After` with `context.WithTimeout` and track goroutines with a `sync.WaitGroup` for clean shutdown.

```go
func (w *DeployWatcher) OnDeploy(ctx context.Context, deploy store.Deploy) {
    // ... baseline snapshot ...
    w.wg.Add(1)
    go func() {
        defer w.wg.Done()
        obsCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
        defer cancel()
        w.observe(obsCtx, deploy, baseline)
    }()
}

func (w *DeployWatcher) Stop() {
    w.wg.Wait()
}
```

**Risk if ignored:** Goroutine accumulation under frequent deploys. Ungraceful shutdown — observation goroutines outlive the application context.

---

### 6. Duplicated `getBaselineMetricValue`

**Problem:** Same switch statement exists in two places with different names.

**Locations:**
- `internal/watcher/watch_session.go:74-96` — function `getBaselineMetricValue`
- `internal/watcher/watch_evidence.go:195-217` — method `getBaselineValue`

**Fix:** Delete one, keep the other as a package-level function in a shared file (e.g., `watch_helpers.go`), and update both callers.

```go
// watch_helpers.go
func baselineMetricValue(w *store.Watch) float64 {
    if w.BaselineJSON == nil {
        return 0
    }
    switch w.Metric {
    case store.WatchMetricErrorRate:
        return w.BaselineJSON.ErrorRate
    // ... rest of switch
    }
}
```

**Risk if ignored:** A new metric added to one copy but not the other causes silent wrong baseline values.

---

## Clean Up (dead code, architecture)

### 7. Wire MCP Tools Through Domain Services

**Problem:** `internal/domain/` has 14 service packages with repository interfaces, business logic, and tests. `internal/app/` wires them together. But nothing imports either package — MCP tools and HTTP handlers go directly to stores.

**Locations:**
- `internal/domain/` — 14 service packages (~4,500 lines)
- `internal/app/app.go` — wiring layer
- `internal/mcp/tools/` — takes store interfaces directly via own dep structs

**What the domain layer enables:**
- Narrow repository interfaces (8 methods instead of 13 for logs) = simpler test fakes
- Typed return structs instead of JSON blobs = better test assertions
- Business logic tested independently from JSON formatting
- Read/write separation enforced by interface design

**Plan:**
1. Pick one tool category as a pilot (suggest: `logs` — it has the most complete domain service)
2. Change `tools.LogsDeps` to accept `*logs.Service` instead of `store.LogStore`
3. Have `LogsSearch` call `svc.Search()` and format the result, instead of calling the store directly
4. Verify tests still pass, update MCP tool tests to mock the service instead of the store
5. Repeat for remaining 13 tool categories
6. Wire `app.New()` into `main.go` and pass `app.App` services to MCP deps

**Risk if ignored:** MCP tool tests remain coupled to fat store interfaces. Business logic stays mixed with presentation in tool handlers.

---

### 8. Delete `internal/storage/`

**Problem:** File storage abstraction (`Storage` interface + `LocalStorage` implementation) copied from omaklabs/base boilerplate. Zero imports anywhere in the project. No planned feature needs it.

**Location:** `internal/storage/` — `storage.go`, `local.go`, `local_test.go`

**Fix:** Delete the directory.

```bash
rm -rf internal/storage/
```

**Risk if ignored:** None. It just adds noise to the codebase.

---

## Leave As-Is

### 9. `internal/routes/` vs `internal/api/` Split

**Situation:** 4 features use the `server.Module` pattern in `internal/routes/` (auth, deploys, events, servers). Everything else is in `internal/api/` as a monolithic server.

**Decision:** Leave it. Both work. The split is cosmetic and doesn't cause bugs. Migrate organically when touching those handlers for other reasons.

---

### 10. 26 Store Interfaces

**Situation:** `pkg/store/stores.go` has 26 store interfaces. Some are specialized (`RunbookEffectivenessStore`, `ToolTransitionStore`, `TestCorrelationStore`).

**Decision:** Leave it. The 1:1 store-per-table pattern is consistent and predictable. Every table has an interface, an implementation, and models. Easy to navigate, easy to find things. Consolidating would save lines but blur boundaries.

---

## Low-Effort Hardening

### 11. Guardrail SQL Validator Bypass

**Problem:** The read-only SQL validator has two gaps:
- `WITH` (CTE) queries are allowed without checking the inner statement. A malicious CTE can wrap a `DELETE` inside a `SELECT`.
- `PRAGMA` is blanket-allowed, but some PRAGMAs are write operations (`wal_checkpoint`, `optimize`, `integrity_check`).

**Location:** `internal/guardrail/sql_generic.go:55-57` (CTE) and `:78` (PRAGMA)

**Fix for PRAGMAs:** Allowlist known read-only PRAGMAs:

```go
case strings.HasPrefix(upper, "PRAGMA"):
    allowed := []string{
        "PRAGMA TABLE_INFO",
        "PRAGMA TABLE_LIST",
        "PRAGMA TABLE_XINFO",
        "PRAGMA INDEX_LIST",
        "PRAGMA INDEX_INFO",
        "PRAGMA FOREIGN_KEY_LIST",
        "PRAGMA DATABASE_LIST",
        "PRAGMA COMPILE_OPTIONS",
        "PRAGMA JOURNAL_MODE",  // read-only when no argument
        "PRAGMA PAGE_COUNT",
        "PRAGMA PAGE_SIZE",
        "PRAGMA MAX_PAGE_COUNT",
        "PRAGMA FREELIST_COUNT",
    }
    for _, prefix := range allowed {
        if strings.HasPrefix(upper, prefix) {
            return nil
        }
    }
    return fmt.Errorf("PRAGMA %q is not in the read-only allowlist", cleaned)
```

**Fix for CTEs:** After allowing `WITH`, validate that the final statement is a `SELECT`:

```go
case strings.HasPrefix(upper, "WITH"):
    // Find the last top-level SELECT — naive but catches obvious attacks.
    // A CTE wrapping DELETE/INSERT would not have a bare SELECT at the end.
    rest := upper
    lastSelect := strings.LastIndex(rest, "SELECT")
    if lastSelect < 0 {
        return fmt.Errorf("CTE must contain a SELECT statement")
    }
    return nil
```

**Risk if ignored:** Low — the guardrail runs against monitored databases (Postgres, MySQL, Turso), not the OpenTrace SQLite DB. But a connected database connector could be used to mutate data via CTE or PRAGMA if an agent constructs a malicious query.

---

## Execution Order

| Phase | Items | Effort |
|-------|-------|--------|
| 1 | #1 proxy auth, #2 level casing, #3 race condition | ~1 hour |
| 2 | #4 stream notifications, #5 goroutine leak, #6 dedup function | ~2 hours |
| 3 | #8 delete storage/, #11 pragma allowlist | ~30 min |
| 4 | #7 wire domain layer (pilot with logs) | ~3 hours |
| 5 | #7 wire remaining 13 tool categories | ongoing |
