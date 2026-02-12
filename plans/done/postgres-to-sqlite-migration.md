# Migration Plan: PostgreSQL → SQLite

## Motivation

OpenTrace is a local CLI/MCP tool that runs per-user. Requiring a PostgreSQL server for the **application database** is unnecessary overhead. SQLite provides:

- **Zero configuration** — single file, no server process
- **Simpler deployment** — just a binary + data file
- **No Docker dependency** for tests (store tests become instant)
- **Lower resource usage** — no connection pool, no background process

**Important distinction**: This migration only affects the **application database** (where OpenTrace stores its own data: data sources, watchers, alerts, logs). The **DatabaseConnector** still connects to user-configured PostgreSQL databases — that is unchanged.

---

## Scope Summary

| Action | Details |
|--------|---------|
| **Remove** | Codebase Connector, EmbeddingStore, pgvector, ChatStore, MemoryStore |
| **Rewrite** | 5 store implementations (DataSource, Log, Watcher, WatcherRun, Alert) |
| **Rewrite** | Migration files (Postgres SQL → SQLite SQL) |
| **Replace** | `pgxpool.Pool` → `*sql.DB` (stdlib `database/sql`) |
| **Replace** | `pgx/v5` driver → `modernc.org/sqlite` (pure Go, no CGO) |
| **Replace** | Testcontainers → in-memory SQLite for store tests |
| **Update** | Config, main.go wiring, go.mod dependencies |
| **Keep** | `pg_query_go` (validates queries to connected Postgres DBs, not app DB) |
| **Keep** | `testcontainers-go` (still needed for DatabaseConnector integration tests) |

---

## Phase 1: Remove Dead Code

**Goal**: Clean up deprecated/unused code before the migration to reduce scope.

### 1.1 Remove Codebase Connector + Embeddings

Files to delete:
- `internal/connector/codebase.go` — CodebaseConnector implementation
- `internal/connector/codebase_test.go` — its tests
- `internal/store/embedding_store.go` — PgEmbeddingStore
- `internal/store/embedding_store_test.go` — its tests

Files to update:
- `internal/connector/factory.go` — remove `ConnectorCodebase` case from `CreateConnector()`
- `internal/store/store.go` — remove `EmbeddingStore` interface, `CodeChunk`, `CodeSearchResult` types
- `internal/web/server.go` — remove `EmbStore` from `ServerDeps`, remove from `NewServer()` params
- `internal/web/mock_test.go` — remove `mockEmbeddingStore`
- `cmd/opentrace/main.go` — remove `embStore` initialization, remove `embedder` setup, remove from `ServerDeps`
- `internal/llm/` — remove `EmbeddingProvider` interface and implementations (Ollama embed, OpenAI embed)

### 1.2 Remove Deprecated Stores

These tables were dropped in migration 000006 but Go code may still exist:
- `internal/store/chat_store.go` — PgChatStore (if still present)
- `internal/store/memory_store.go` — PgMemoryStore (if still present)
- `internal/store/store.go` — remove `ChatStore`, `MemoryStore` interfaces, related types
- Remove any remaining references in web/server.go, main.go

### 1.3 Remove ConnectorCodebase Type

- `internal/store/store.go` — remove `ConnectorCodebase` from `ConnectorType` constants
- `internal/connector/factory.go` — remove the codebase case

### 1.4 Verify

Run `go build ./...` to confirm no broken references remain.

---

## Phase 2: Add SQLite Driver + Database Layer

**Goal**: Introduce SQLite infrastructure alongside existing Postgres (parallel support during transition).

### 2.1 Add SQLite Dependency

```bash
go get modernc.org/sqlite
```

Why `modernc.org/sqlite` over `mattn/go-sqlite3`:
- Pure Go — no CGO required, cross-compiles easily
- Supports RETURNING, ON CONFLICT, JSON functions, FTS5
- Active development, good compatibility

### 2.2 Create SQLite Connection Helper

New file: `internal/store/sqlite.go`

```go
package store

import (
    "database/sql"
    _ "modernc.org/sqlite"
)

func OpenSQLite(path string) (*sql.DB, error) {
    db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
    if err != nil {
        return nil, err
    }
    db.SetMaxOpenConns(1) // SQLite handles one writer at a time
    return db, nil
}
```

Key pragmas:
- `_journal_mode=WAL` — enables concurrent reads during writes
- `_busy_timeout=5000` — wait up to 5s instead of immediate SQLITE_BUSY
- `_foreign_keys=on` — enforce FK constraints (off by default in SQLite)

### 2.3 Create SQLite Migration Runner

New file: `internal/store/migrate_sqlite.go`

Use `golang-migrate` with the `sqlite` driver, or run migrations manually with `db.Exec()` (simpler for SQLite since there's no separate migration driver complexity).

Recommended approach: embed migration SQL files and run them via `db.Exec()` with a simple `schema_version` table for tracking.

```go
func RunSQLiteMigrations(db *sql.DB, migrationsDir string) error {
    // Create version tracking table
    // Read and apply .up.sql files in order
    // Track current version
}
```

---

## Phase 3: Rewrite Migrations

**Goal**: Convert all active migrations from Postgres SQL to SQLite-compatible SQL.

### 3.1 New Migration Directory

Create `migrations/sqlite/` alongside existing `migrations/` (keep Postgres migrations for reference during transition, delete later).

### 3.2 Schema Translation Rules

| Postgres | SQLite | Notes |
|----------|--------|-------|
| `UUID` (DEFAULT gen_random_uuid()) | `TEXT NOT NULL DEFAULT ''` | Generate UUID in Go app layer |
| `SERIAL` / `BIGSERIAL` | `INTEGER PRIMARY KEY AUTOINCREMENT` | |
| `TIMESTAMPTZ` | `TEXT` | Store as ISO 8601 strings |
| `JSONB` | `TEXT` | Store as JSON strings, use `json_extract()` for queries |
| `BOOLEAN` | `INTEGER` | 0/1 |
| `CREATE TYPE ... AS ENUM` | `TEXT CHECK(col IN (...))` | Inline constraints |
| `CREATE EXTENSION vector` | (remove) | No pgvector |
| `vector(768)` column | (remove) | No embeddings |
| `to_tsvector/plainto_tsquery` | FTS5 virtual table | See 3.3 |
| `GIN index` | (remove or replace) | Not applicable |
| `IVFFlat index` | (remove) | No vector search |
| `USING ivfflat` | (remove) | |
| `$1, $2` placeholders | `?, ?` | Standard database/sql |
| `now()` | `datetime('now')` | SQLite datetime function |
| `INTERVAL` | (not used) | Already handled in app layer |
| Partial index `WHERE ...` | Partial index `WHERE ...` | SQLite supports this |

### 3.3 Full-Text Search Migration

Postgres FTS:
```sql
-- Index
CREATE INDEX idx_logs_message_fts ON logs USING gin(to_tsvector('english', message));
-- Query
WHERE to_tsvector('english', message) @@ plainto_tsquery('english', $1)
```

SQLite FTS5:
```sql
-- Virtual table (separate from main logs table)
CREATE VIRTUAL TABLE logs_fts USING fts5(message, content=logs, content_rowid=id);

-- Triggers to keep FTS in sync
CREATE TRIGGER logs_ai AFTER INSERT ON logs BEGIN
    INSERT INTO logs_fts(rowid, message) VALUES (new.id, new.message);
END;
CREATE TRIGGER logs_ad AFTER DELETE ON logs BEGIN
    INSERT INTO logs_fts(logs_fts, rowid, message) VALUES('delete', old.id, old.message);
END;
CREATE TRIGGER logs_au AFTER UPDATE ON logs BEGIN
    INSERT INTO logs_fts(logs_fts, rowid, message) VALUES('delete', old.id, old.message);
    INSERT INTO logs_fts(rowid, message) VALUES (new.id, new.message);
END;

-- Query
SELECT l.* FROM logs l JOIN logs_fts f ON l.id = f.rowid
WHERE logs_fts MATCH ?
```

### 3.4 Consolidated Migration File

Since this is a clean break, consolidate all 7 Postgres migrations into a **single SQLite init migration**. Only include active tables:

`migrations/sqlite/000001_init.up.sql`:

```sql
-- data_sources
CREATE TABLE data_sources (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL CHECK(type IN ('logs', 'database')),
    name TEXT NOT NULL DEFAULT '',
    config TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'disconnected' CHECK(status IN ('connected', 'disconnected', 'error')),
    status_message TEXT,
    last_tested_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX idx_data_sources_type ON data_sources(type) WHERE status = 'connected';

-- logs
CREATE TABLE logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT NOT NULL,
    level TEXT NOT NULL DEFAULT 'info',
    service TEXT NOT NULL DEFAULT '',
    trace_id TEXT,
    message TEXT NOT NULL DEFAULT '',
    environment TEXT,
    metadata TEXT DEFAULT '{}'
);
CREATE INDEX idx_logs_timestamp ON logs(timestamp);
CREATE INDEX idx_logs_service ON logs(service);
CREATE INDEX idx_logs_level ON logs(level);
CREATE INDEX idx_logs_trace_id ON logs(trace_id) WHERE trace_id IS NOT NULL;

-- logs FTS
CREATE VIRTUAL TABLE logs_fts USING fts5(message, content=logs, content_rowid=id);
-- (triggers defined here)

-- app_config
CREATE TABLE app_config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '{}'
);

-- watchers
CREATE TABLE watchers (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT,
    query TEXT NOT NULL,
    filters TEXT DEFAULT '{}',
    severity TEXT NOT NULL DEFAULT 'info' CHECK(severity IN ('info', 'warning', 'critical')),
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'paused', 'error')),
    time_range TEXT NOT NULL DEFAULT '15m',
    notify TEXT DEFAULT '{}',
    last_run_at TEXT,
    next_run_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- watcher_runs
CREATE TABLE watcher_runs (
    id TEXT PRIMARY KEY,
    watcher_id TEXT NOT NULL REFERENCES watchers(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'running' CHECK(status IN ('running', 'completed', 'error')),
    summary TEXT,
    details TEXT,
    has_alert INTEGER NOT NULL DEFAULT 0,
    error_message TEXT,
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    finished_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_watcher_runs_watcher ON watcher_runs(watcher_id, created_at DESC);

-- alerts
CREATE TABLE alerts (
    id TEXT PRIMARY KEY,
    watcher_id TEXT REFERENCES watchers(id) ON DELETE SET NULL,
    run_id TEXT REFERENCES watcher_runs(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    severity TEXT NOT NULL DEFAULT 'info' CHECK(severity IN ('info', 'warning', 'critical')),
    read INTEGER NOT NULL DEFAULT 0,
    dismissed INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_alerts_unread ON alerts(created_at DESC) WHERE read = 0 AND dismissed = 0;
```

---

## Phase 4: Rewrite Store Implementations

**Goal**: Convert all 5 active stores from pgx to database/sql + SQLite.

### 4.1 Common Changes Across All Stores

- Replace `*pgxpool.Pool` → `*sql.DB` in struct and constructor
- Replace `pool.QueryRow(ctx, sql, args...)` → `db.QueryRowContext(ctx, sql, args...)`
- Replace `pool.Query(ctx, sql, args...)` → `db.QueryContext(ctx, sql, args...)`
- Replace `pool.Exec(ctx, sql, args...)` → `db.ExecContext(ctx, sql, args...)`
- Replace `$1, $2, $3` → `?, ?, ?` placeholders
- Replace `pgx.ErrNoRows` → `sql.ErrNoRows`
- Replace `RETURNING ...` → use `result.LastInsertId()` where possible, or keep `RETURNING` (SQLite 3.35+ supports it via modernc driver)
- Generate UUIDs in Go with `uuid.New().String()` before INSERT
- Store timestamps as ISO 8601 strings, parse with `time.Parse(time.RFC3339, ...)`

### 4.2 DataSourceStore Rewrite

File: `internal/store/data_source.go`

Changes:
- Replace struct field `pool *pgxpool.Pool` → `db *sql.DB`
- `Create()`: generate UUID in Go, use `RETURNING` or separate SELECT
- `Update()`: COALESCE works in SQLite, keep pattern
- Config field: marshal/unmarshal JSON manually (json.RawMessage → TEXT)
- Rename: `PgDataSourceStore` → `DataSourceStoreImpl` (or `SQLiteDataSourceStore`)

### 4.3 LogStore Rewrite

File: `internal/store/log_store.go`

Changes:
- **Critical**: Replace `pgx.CopyFrom()` batch insert with transaction + INSERT loop
  ```go
  tx, _ := db.BeginTx(ctx, nil)
  stmt, _ := tx.PrepareContext(ctx, "INSERT INTO logs (...) VALUES (?, ?, ?, ?, ?, ?, ?)")
  for _, entry := range entries {
      stmt.ExecContext(ctx, ...)
  }
  tx.Commit()
  ```
  This is actually fast in SQLite with WAL mode + prepared statements in a transaction.
- **FTS**: Replace `to_tsvector @@ plainto_tsquery` with FTS5 MATCH query:
  ```go
  // Old: WHERE to_tsvector('english', message) @@ plainto_tsquery('english', $1)
  // New: JOIN logs_fts ON logs.id = logs_fts.rowid WHERE logs_fts MATCH ?
  ```
- Keep dynamic WHERE clause construction, change `$N` → `?`
- Add FTS sync triggers in migration (or handle in BatchInsert with manual FTS insert)

### 4.4 WatcherStore Rewrite

File: `internal/store/watcher_store.go`

Changes:
- Replace `now()` → `datetime('now')` in SQL or generate in Go
- COALESCE pattern works in SQLite — keep it
- `GetDueWatchers()`: `WHERE status = 'active' AND next_run_at <= datetime('now')`
- JSONB columns → TEXT (json_extract() available if needed)
- Boolean: `has_alert` stored as INTEGER (0/1)

### 4.5 WatcherRunStore Rewrite

File: `internal/store/watcher_run_store.go`

Changes:
- Similar patterns to WatcherStore
- `json.Marshal(details)` → store as TEXT
- Generate UUID in Go before INSERT
- Replace `RETURNING` with `RETURNING` (supported) or post-SELECT

### 4.6 AlertStore Rewrite

File: `internal/store/alert_store.go`

Changes:
- Boolean fields `read`, `dismissed` → INTEGER (0/1)
- Dynamic WHERE construction: change `$N` → `?`
- `CountUnread()`: `WHERE read = 0 AND dismissed = 0`

### 4.7 Naming Convention

Rename store implementations:
- `PgDataSourceStore` → `dataSourceStore` (unexported, since we only have one impl now)
- Or keep current naming and just change to `SqliteDataSourceStore` if you prefer explicit naming

Recommendation: just use unexported names since there's only one implementation.

---

## Phase 5: Update Application Wiring

### 5.1 Configuration Changes

File: `internal/config/config.go`

```go
// Old
AppDatabaseURL string  // postgres://localhost/opentrace

// New
DataDir string  // ~/.opentrace/ (directory for SQLite file + other data)
```

The SQLite database file lives at `{DataDir}/opentrace.db`.

Default: `~/.opentrace/opentrace.db` (or respect `OPENTRACE_DATA_DIR` env var).

Remove: `OPENTRACE_APP_DATABASE_URL` env var.

### 5.2 Main.go Changes

File: `cmd/opentrace/main.go`

```go
// Old
pool, err := pgxpool.New(ctx, cfg.AppDatabaseURL)
defer pool.Close()
dsStore := store.NewPgDataSourceStore(pool)
logStore := store.NewPgLogStore(pool)
embStore := store.NewPgEmbeddingStore(pool)
// ... etc

// New
db, err := store.OpenSQLite(cfg.DatabasePath())
defer db.Close()
store.RunSQLiteMigrations(db)
dsStore := store.NewDataSourceStore(db)
logStore := store.NewLogStore(db)
// ... no embStore
```

Remove:
- `embStore` initialization
- `embedder` / `EmbeddingProvider` setup
- `EmbStore` from `ServerDeps`

### 5.3 Web Server Changes

File: `internal/web/server.go`

Remove `EmbStore` and `Embedder` from `ServerDeps`. No other web handler changes needed (handlers call store interfaces, not pgx directly).

### 5.4 MCP Server

File: `internal/mcp/server.go`

No changes needed — `Deps` only references `Registry`, `WatcherStore`, `AlertStore` (interfaces, not implementations).

---

## Phase 6: Update Tests

### 6.1 Store Integration Tests

Replace testcontainer-based setup with in-memory SQLite:

```go
func setupTestDB(t *testing.T) *sql.DB {
    db, err := sql.Open("sqlite", ":memory:?_foreign_keys=on")
    require.NoError(t, err)
    runMigrations(db) // apply SQLite migrations
    t.Cleanup(func() { db.Close() })
    return db
}
```

Benefits:
- **No Docker required** for store tests
- **Instant** — no container startup time
- **No `testing.Short()` skip** — these become fast unit tests
- **Parallel-safe** — each test gets its own in-memory DB

Files to update:
- `internal/testutil/db.go` — rewrite for SQLite (keep Postgres helper for connector tests)
- `internal/store/testhelper_test.go` — rewrite for SQLite
- `internal/store/data_source_test.go` — update setup calls
- `internal/store/log_store_test.go` — update setup + FTS assertions
- `internal/store/watcher_store_test.go` — update setup calls
- `internal/store/watcher_run_store_test.go` — update setup calls
- `internal/store/alert_store_test.go` — update setup calls

Delete:
- `internal/store/embedding_store_test.go`
- `internal/testutil/db_test.go` (rewrite or remove — was testing pgvector)

### 6.2 Connector Tests (Keep Postgres)

`internal/connector/database_test.go` and related tests still need testcontainers because they test against **real Postgres databases** (the user's connected DBs). These are unchanged.

### 6.3 Mock Stores (No Changes)

`internal/web/mock_test.go` and `internal/mcp/server_test.go` use in-memory mocks — no changes needed.

### 6.4 Migration Tests

Rewrite `internal/store/migrate_test.go` to test SQLite migrations instead of Postgres migrations.

---

## Phase 7: Clean Up Dependencies

### 7.1 Remove from go.mod

```
github.com/jackc/pgx/v5                    # Postgres driver (KEEP — used by DatabaseConnector)
github.com/pgvector/pgvector-go            # REMOVE — no more embeddings
github.com/testcontainers/testcontainers-go # KEEP — DatabaseConnector tests
```

Wait — **pgx/v5 must stay** because `DatabaseConnector` creates its own pgx pool to connect to user Postgres databases. Only pgvector-go is fully removable.

### 7.2 Add to go.mod

```
modernc.org/sqlite  # Pure Go SQLite driver
```

### 7.3 Remove from go.mod (migration driver)

The golang-migrate pgx5 driver import changes. Keep golang-migrate but use the sqlite driver for app migrations:
```
github.com/golang-migrate/migrate/v4/database/sqlite  # if using golang-migrate for SQLite
```

Or drop golang-migrate for the app DB entirely and use a simpler hand-rolled migration runner (recommended — SQLite migrations are simple enough).

### 7.4 Run `go mod tidy`

---

## Phase 8: Data Migration Tool (Optional)

If existing users have data in Postgres, provide a one-time migration command:

```bash
opentrace migrate-to-sqlite --from postgres://... --to ~/.opentrace/opentrace.db
```

This reads from Postgres and writes to SQLite. Skip if there are no existing users with production data.

---

## Execution Order

| Step | Phase | Description | Risk |
|------|-------|-------------|------|
| 1 | Phase 1 | Remove dead code (embeddings, codebase connector, deprecated stores) | Low — removing unused code |
| 2 | Phase 1 | Verify build passes | Low |
| 3 | Phase 2 | Add SQLite driver + connection helper | Low — additive |
| 4 | Phase 3 | Write SQLite migration file | Low — new file |
| 5 | Phase 4 | Rewrite store implementations one by one | **Medium** — core logic change |
| 6 | Phase 5 | Update config + main.go wiring | Medium — changes app startup |
| 7 | Phase 6 | Update tests | Medium — many files |
| 8 | Phase 7 | Clean up go.mod | Low |
| 9 | Phase 8 | Data migration tool (if needed) | Low — optional |

**Recommended approach**: Do Phase 1 as a separate commit/PR, then Phases 2-7 together as the main migration.

---

## Files Changed Summary

### Delete (Phase 1)
- `internal/connector/codebase.go`
- `internal/connector/codebase_test.go`
- `internal/store/embedding_store.go`
- `internal/store/embedding_store_test.go`
- `internal/store/chat_store.go` (if present)
- `internal/store/memory_store.go` (if present)
- `internal/llm/embedding_*.go` (embedding provider files)

### New Files
- `internal/store/sqlite.go` — connection helper + migration runner
- `migrations/sqlite/000001_init.up.sql` — consolidated SQLite schema

### Major Rewrites
- `internal/store/data_source.go`
- `internal/store/log_store.go`
- `internal/store/watcher_store.go`
- `internal/store/watcher_run_store.go`
- `internal/store/alert_store.go`
- `internal/store/migrate.go`
- `internal/store/store.go` (remove embedding/chat/memory interfaces)
- `cmd/opentrace/main.go`
- `internal/config/config.go`
- `internal/web/server.go`

### Test Rewrites
- `internal/testutil/db.go`
- `internal/store/testhelper_test.go`
- `internal/store/data_source_test.go`
- `internal/store/log_store_test.go`
- `internal/store/watcher_store_test.go`
- `internal/store/watcher_run_store_test.go`
- `internal/store/alert_store_test.go`
- `internal/store/migrate_test.go`

### Minor Updates
- `internal/web/mock_test.go` (remove embedding mock)
- `internal/connector/factory.go` (remove codebase case)
- `go.mod` / `go.sum`

---

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| SQLite write concurrency (single writer) | WAL mode + busy timeout; OpenTrace is single-user so this is fine |
| FTS5 is less powerful than Postgres FTS | FTS5 handles the current use case (simple text search) well |
| Losing JSONB query operators | Only used in one migration backfill; app layer handles JSON |
| Data loss for existing users | Phase 8 migration tool (if needed) |
| pgx still in go.mod (DatabaseConnector) | Expected — it's for connected DBs, not app DB |
| modernc.org/sqlite binary size | ~15MB increase; acceptable for a CLI tool |

---

## Success Criteria

- [ ] `go build ./...` passes
- [ ] `go test -short -race ./...` passes (no Docker needed)
- [ ] `go test -race ./...` passes (Docker only for DatabaseConnector tests)
- [ ] `opentrace serve` starts with SQLite backend
- [ ] `opentrace mcp` works unchanged
- [ ] Watchers create/execute/alert correctly
- [ ] Log ingestion and search works with FTS5
- [ ] Data sources CRUD works
- [ ] Dashboard UI works unchanged (no frontend changes needed)
