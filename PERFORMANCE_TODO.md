# OpenTrace Performance Optimization Plan

> Generated from deep performance audit of the Go server codebase.
> Work through items in priority order. Check off each item as completed.

---

## Critical Priority

### 1. [x] Add SQLite PRAGMAs (cache, synchronous, mmap, temp_store)

**Impact:** 2-3x write throughput | **Effort:** 10 min

**File:** `internal/store/sqlite.go`

**Problem:** We open SQLite with WAL mode but miss 4 critical performance PRAGMAs. The default `synchronous = FULL` forces an fsync on every write, and the default 2MB cache is far too small.

**Current code (line ~18-26):**
```go
dsn := path + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
db.SetMaxOpenConns(1)
```

**Fix — add after `db.Open()`:**
```go
db.Exec("PRAGMA cache_size = -64000")      // 64MB page cache (default: 2MB)
db.Exec("PRAGMA synchronous = NORMAL")     // safe with WAL, skips fsync per write
db.Exec("PRAGMA mmap_size = 30000000")     // 30MB memory-mapped I/O
db.Exec("PRAGMA temp_store = MEMORY")      // temp tables in RAM, not disk
```

**Why it matters:** `synchronous = NORMAL` alone can 2-3x write throughput. Combined with larger cache and mmap, this is the single highest-ROI change in the entire codebase.

---

### 2. [x] Bound Goroutines with Worker Pools

**Impact:** Prevents OOM under load | **Effort:** 2-3 hrs

**Problem:** Three locations spawn unbounded goroutines — fire-and-forget with no cap. Under sustained load (10k logs/sec or 1000+ health checks), goroutines can explode.

**Location A — Watch stream evaluation:**
- **File:** `internal/watcher/watch_stream.go:55`
- `go s.evaluateMatching(services)` — new goroutine per log batch, no limit

**Location B — Health check scheduler:**
- **File:** `internal/healthcheck/scheduler.go:96`
- `go s.runCheck(ctx, hc)` — one goroutine per health check per tick, no limit

**Location C — Audit logging:**
- **File:** `internal/web/server.go:510-522`
- `go func() { _ = s.auditStore.Log(...) }()` — no backpressure, errors dropped

**Fix pattern — bounded worker pool:**
```go
// Option A: semaphore
sem := make(chan struct{}, 16) // max 16 concurrent
for _, hc := range checks {
    sem <- struct{}{}
    go func(hc HealthCheck) {
        defer func() { <-sem }()
        s.runCheck(ctx, hc)
    }(hc)
}

// Option B: golang.org/x/sync/semaphore
sem := semaphore.NewWeighted(16)
for _, hc := range checks {
    sem.Acquire(ctx, 1)
    go func(hc HealthCheck) {
        defer sem.Release(1)
        s.runCheck(ctx, hc)
    }(hc)
}
```

**For audit logging:** reuse the `ActivityLogger` pattern (bounded channel + worker pool) instead of fire-and-forget goroutines.

---

### 3. [x] Fix Journey Store N+1 Query

**Impact:** 2000 queries → 1 query | **Effort:** 1 hr

**File:** `internal/store/journey_store.go` — `BuildSessions()`

**Problem:** For each session aggregate, 2 extra queries fetch entry/exit paths:
```
1 query (all session aggregates)
+ 2 queries x N sessions (entry path + exit path)
= 2001 queries for 1000 sessions
```

**Current code (lines 70-128):**
```go
for _, a := range aggs {
    // Query 1: entry path
    err := s.db.QueryRowContext(ctx, `
        SELECT path, status FROM logs l
        JOIN request_summaries rs ON rs.log_id = l.id
        WHERE l.session_id = ? AND l.service = ?
        ORDER BY l.timestamp ASC LIMIT 1`, ...)

    // Query 2: exit path
    err = s.db.QueryRowContext(ctx, `
        SELECT path, status FROM logs l
        JOIN request_summaries rs ON rs.log_id = l.id
        WHERE l.session_id = ? AND l.service = ?
        ORDER BY l.timestamp DESC LIMIT 1`, ...)
}
```

**Fix — single query with window functions:**
```sql
SELECT
    session_id, service,
    FIRST_VALUE(path) OVER (PARTITION BY session_id ORDER BY timestamp ASC) AS entry_path,
    FIRST_VALUE(status) OVER (PARTITION BY session_id ORDER BY timestamp ASC) AS entry_status,
    FIRST_VALUE(path) OVER (PARTITION BY session_id ORDER BY timestamp DESC) AS exit_path,
    FIRST_VALUE(status) OVER (PARTITION BY session_id ORDER BY timestamp DESC) AS exit_status
FROM logs l
JOIN request_summaries rs ON rs.log_id = l.id
WHERE l.session_id != '' AND l.timestamp >= ?
GROUP BY l.session_id, l.service
```

---

### 4. [ ] Propagate App Context Instead of `context.Background()`

**Impact:** Graceful shutdown actually works | **Effort:** 3-4 hrs

**Problem:** Found ~417 `context.Background()` calls. Background tasks don't receive cancellation signals, so they keep running during shutdown — potentially writing to SQLite mid-close.

**Key locations:**
- `cmd/opentrace/main.go:328-450` — all 6 background ticker goroutines
- `internal/web/server.go:511` — audit logging
- `internal/watcher/watch_stream.go:59` — watch evaluation (uses 30s timeout but parent is Background)
- Various cleanup/aggregation jobs

**Fix approach:**
1. Pass the app-level `ctx` (from `main()`) down through `ServerDeps` or a dedicated lifecycle struct.
2. Replace `context.Background()` with `ctx` (or a child of `ctx`) in all background operations.
3. Ensure all goroutines select on `ctx.Done()` and exit cleanly.
4. Add `sync.WaitGroup` tracking so `main()` can wait for all background work to finish before closing the DB.

---

## High Priority

### 5. [x] Add Missing Composite Indexes

**Impact:** 10-100x faster aggregation queries | **Effort:** 30 min

**Problem:** Several heavy queries scan large tables without proper indexes.

**Create new migration file:** `internal/store/sqlite_migrations/000034_performance_indexes.up.sql`

```sql
-- Metrics: LatestByServer() subquery (metric_store.go:115)
CREATE INDEX IF NOT EXISTS idx_metrics_server_name_ts
    ON metrics(server_id, metric_name, timestamp DESC);

-- Trends: AggregateBuckets() GROUP BY (trend_store.go:45)
CREATE INDEX IF NOT EXISTS idx_logs_ts_service_env
    ON logs(timestamp, service, environment);

-- Analytics: AggregateEndpointStats() GROUP BY (analytics_store.go)
CREATE INDEX IF NOT EXISTS idx_logs_ts_level
    ON logs(timestamp, level);

-- Journey: entry/exit path lookups (journey_store.go)
CREATE INDEX IF NOT EXISTS idx_logs_session_service_ts
    ON logs(session_id, service, timestamp)
    WHERE session_id != '';
```

**Verify with:** `EXPLAIN QUERY PLAN` on each affected query after adding indexes.

---

### 6. [x] Eliminate Duplicate Metadata Marshal in Log Ingestion

**Impact:** ~50% fewer allocations on hottest path | **Effort:** 1 hr

**Problem:** The log ingestion hot path marshals metadata twice:
1. Web handler (`internal/web/logs.go`) copies fields into `store.LogEntry`
2. Store layer (`internal/store/log_store.go:54`) calls `json.Marshal(e.Metadata)` again

Also: `[]byte` → `string` conversions for Timeline/TimeBreakdown allocate unnecessarily.

**Files:**
- `internal/web/logs.go:135-191` — handler builds LogEntry
- `internal/store/log_store.go:54` — store re-marshals

**Fix options:**
- Option A: Marshal metadata once in the web handler, store it as `[]byte`/`string` in `LogEntry`, and skip marshaling in the store.
- Option B: Keep `Metadata` as `map[string]any` but add a `MetadataJSON []byte` field that caches the marshaled form.

Also: FTS5 triggers fire synchronously on every INSERT inside the batch transaction. For large batches, consider:
- Disabling triggers during batch insert
- Rebuilding FTS after the transaction commits

---

### 7. [x] Parallelize Aggregation Jobs with errgroup

**Impact:** Aggregation cycle 3-5x faster | **Effort:** 30 min

**File:** `cmd/opentrace/main.go:364-489`

**Problem:** All 5 aggregation jobs (trends, analytics, journeys, error impact, heatmaps) run sequentially in a single goroutine every 5 minutes. If one is slow, it blocks the rest.

**Current code:**
```go
go func() {
    ticker := time.NewTicker(5 * time.Minute)
    for { select { case <-ticker.C:
        trendStore.AggregateBuckets(ctx, ...)       // blocks
        analyticsStore.AggregateEndpointStats(...)   // waits
        journeyStore.BuildSessions(...)              // waits
        errorImpactStore.ComputeImpactScores(...)    // waits
    }}
}()
```

**Fix:**
```go
go func() {
    ticker := time.NewTicker(5 * time.Minute)
    for { select { case <-ticker.C:
        g, gctx := errgroup.WithContext(ctx)
        g.Go(func() error { return trendStore.AggregateBuckets(gctx, ...) })
        g.Go(func() error { return analyticsStore.AggregateEndpointStats(gctx, ...) })
        g.Go(func() error { return journeyStore.BuildSessions(gctx, ...) })
        g.Go(func() error { return errorImpactStore.ComputeImpactScores(gctx, ...) })
        if err := g.Wait(); err != nil {
            slog.Error("aggregation error", "error", err)
        }
    }}
}()
```

**Note:** Since SQLite is single-writer (`MaxOpenConns(1)`), these will still serialize at the DB level. But they'll interleave computation vs. I/O, and any non-DB work runs truly parallel. If we later add read replicas or switch to a reader/writer split, this becomes a real win.

---

### 8. [ ] Fix ActivityLogger Shutdown with WaitGroup

**Impact:** No data loss on shutdown | **Effort:** 15 min

**File:** `internal/mcp/activity_logger.go`

**Problem:** `Close()` calls `close(ch)` but doesn't wait for workers to drain. In-flight activity logs are lost on shutdown, and a send-on-closed-channel panic is possible.

**Current code (lines 51-54):**
```go
func (al *ActivityLogger) Close() {
    close(al.ch)
}
```

**Fix:**
```go
type ActivityLogger struct {
    ch chan ActivityEvent
    wg sync.WaitGroup  // ADD
    // ...
}

func (al *ActivityLogger) Start() {
    for i := 0; i < al.workers; i++ {
        al.wg.Add(1)  // ADD
        go func() {
            defer al.wg.Done()  // ADD
            for event := range al.ch {
                al.process(event)
            }
        }()
    }
}

func (al *ActivityLogger) Close() {
    close(al.ch)
    al.wg.Wait()  // ADD — wait for workers to drain
}
```

---

## Medium Priority

### 9. [ ] Replace MarshalIndent with Marshal in MCP Tools

**Impact:** 20-30% fewer allocations in MCP tool responses | **Effort:** 15 min

**Problem:** MCP tool responses use `json.MarshalIndent` (pretty-printing) which allocates ~30% more memory than `json.Marshal`. No MCP client needs indented JSON — they parse it programmatically.

**Files to update:**
- `internal/mcp/tool_log_search.go:324`
- `internal/mcp/server.go:199, 1196, 1229, 1278, 1315`
- Any other `MarshalIndent` calls in `internal/mcp/`

**Fix:** Global find-and-replace:
```go
// Before
json.MarshalIndent(resp, "", "  ")

// After
json.Marshal(resp)
```

Also in `server.go:199` — activity logging marshals args just to truncate to 500 chars. Consider logging the arg count + keys instead of full JSON.

---

### 10. [ ] Slice Pre-allocation and strings.Builder

**Impact:** Minor GC pressure reduction | **Effort:** 30 min

**Problem:** Several hot paths create slices without capacity hints and build strings with `fmt.Sprintf` + concatenation in loops.

**Location A — Missing slice capacity:**
- `internal/store/log_store.go:320`: `make([]LogEntry, 0)` → `make([]LogEntry, 0, limit)`
- `internal/mcp/tool_log_search.go:212`: `var summaryLines []string` → `make([]string, 0, len(entries))`

**Location B — String concatenation in loops:**
- `internal/mcp/tool_log_search.go:220-246`: builds summary lines with repeated `fmt.Sprintf` + `+=`
- `internal/connector/logs.go:107-118`: same pattern
- `internal/connector/metrics.go:86-93`: same pattern

**Fix pattern:**
```go
// Before (tool_log_search.go:220)
line := fmt.Sprintf("[%s] %s [%s]", ts, e.Level, e.Service)
line += fmt.Sprintf(" %s", e.ExceptionClass)

// After
var b strings.Builder
b.Grow(128) // pre-allocate typical line size
b.WriteString("[")
b.WriteString(ts)
b.WriteString("] ")
b.WriteString(e.Level)
b.WriteString(" [")
b.WriteString(e.Service)
b.WriteString("]")
if e.ExceptionClass != "" {
    b.WriteString(" ")
    b.WriteString(e.ExceptionClass)
}
summaryLines[i] = b.String()
```

---

## Progress Tracker

| # | Item | Status | Completed Date |
|---|------|--------|----------------|
| 1 | SQLite PRAGMAs | Done | 2026-02-25 |
| 2 | Bound goroutines with worker pools | Done | 2026-02-25 |
| 3 | Fix journey store N+1 | Done | 2026-02-25 |
| 4 | Propagate app context | Not Started | |
| 5 | Add composite indexes | Done | 2026-02-25 |
| 6 | Eliminate duplicate metadata marshal | Done | 2026-02-25 |
| 7 | Parallelize aggregation jobs | Done | 2026-02-25 |
| 8 | Fix ActivityLogger shutdown | Not Started | |
| 9 | Replace MarshalIndent with Marshal | Not Started | |
| 10 | Slice pre-allocation + strings.Builder | Not Started | |

**Estimated total effort:** ~10-12 hours
**Estimated performance gain:** 2-3x write throughput (item 1), prevent OOM (item 2), eliminate N+1 (item 3), 15-25% fewer allocations across hot paths (items 6, 9, 10)
