# OpenTrace — Developer Guide

## Quick Start

```bash
# Prerequisites: Go 1.25 (Homebrew), Ollama (optional, for AI watchers)
export PATH="/opt/homebrew/bin:$PATH"

# Run the web server (creates ~/.opentrace/opentrace.db on first run)
cp .env.example .env  # edit as needed
go run ./cmd/opentrace

# Other subcommands
go run ./cmd/opentrace version     # print version
go run ./cmd/opentrace mcp         # start MCP stdio server
go run ./cmd/opentrace agent       # start VM metrics agent
go run ./cmd/opentrace seed        # initialize sample data
```

The app listens on `127.0.0.1:8080` by default. First visit triggers onboarding (create admin user).

## Running Tests

```bash
# Unit + SQLite store tests (no Docker needed, fast)
go test -short -race ./...

# Full suite including Postgres integration tests (needs Docker)
go test -race ./...

# Single package
go test -short -race ./internal/store/...
go test -short -race ./internal/watcher/...
go test -short -race ./internal/web/...
go test -short -race ./internal/mcp/...

# Verbose single test
go test -short -race -v -run TestScheduler ./internal/watcher/...
```

Store tests use in-memory SQLite (`:memory:`), no file cleanup needed.
Integration tests (`testing.Short()` skip) require Docker for Postgres via testcontainers.

## Build

```bash
go build -o opentrace ./cmd/opentrace

# With version info (mimics CI)
go build -ldflags "-X github.com/adham90/opentrace/internal/version.Version=dev \
  -X github.com/adham90/opentrace/internal/version.Commit=$(git rev-parse --short HEAD) \
  -X github.com/adham90/opentrace/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o opentrace ./cmd/opentrace
```

Note: `pg_query_go` requires CGO. Cross-compilation needs `CGO_ENABLED=1` with appropriate C compiler.

## Architecture

```
cmd/opentrace/          CLI entry (web server, mcp, agent, seed subcommands)
internal/
  agent/                AI agent loop (tool calling, step validation)
  config/               Environment-based configuration
  connector/            Postgres connector, registry, query execution
  digest/               Health digest builder
  guardrail/            SQL read-only validation via AST
  llm/                  Multi-provider: Anthropic, OpenAI, Ollama, Gemini
  mcp/                  MCP server + 74 tools (database, watchers, alerts, logs)
  store/                SQLite stores (all data models, migrations)
  version/              Build version (set via ldflags)
  vmagent/              Server metrics collection agent
  watcher/              Scheduler, executor, adaptive engine, notifications
  web/                  Chi HTTP handlers, templates, auth, middleware
```

**Key patterns:**
- Store interface per domain (`DataSourceStore`, `WatcherStore`, etc.) backed by `*sql.DB`
- `NewServerWithDeps(ServerDeps{...})` for full dependency injection
- `connector.Registry` with `sync.RWMutex` manages live database connections
- MCP tools registered in `internal/mcp/catalog.go`
- Watcher types: `"ai"` (LLM agent loop) and `"rule"` (query/log/health checks)

## Database

SQLite at `~/.opentrace/opentrace.db` (override with `OPENTRACE_DATA_DIR`).
- WAL mode, `busy_timeout=5000`, `foreign_keys=on`, `MaxOpenConns=1`
- Migrations: `internal/store/sqlite_migrations/*.sql` (embedded via `//go:embed`)
- FTS5 virtual table `logs_fts` for full-text log search
- Conventions: `?` placeholders, booleans as INTEGER 0/1, timestamps as RFC3339 TEXT, UUIDs as TEXT

## Coding Conventions

- `writeJSON(w, status, data)` / `writeError(w, status, msg)` for HTTP responses
- `store.ErrNotFound` sentinel error for 404s
- `testing.Short()` guard on integration tests
- Mock stores in `web/mock_test.go`, `mcp/server_test.go`
- In `web/watchers.go`, don't name local vars `watcher` — conflicts with package import
- When adding store interface methods, update ALL mock implementations (web, mcp, watcher, digest)
- MCP tool args come as `float64` from JSON, not `int` — cast with `args["x"].(float64)`
- Run `go mod tidy` after adding new imports
- Use `go vet ./...` for linting (golangci-lint doesn't support Go 1.25 yet)

## Deployment

- **Docker**: `ghcr.io/adham90/opentrace` — built on `v*` tags via GoReleaser
- **Auto-upgrade**: `docker-compose.prod.yml` includes Watchtower sidecar
- **Cloud configs**: `deploy/` has DigitalOcean, Hetzner (cloud-init), plus `fly.toml`, `railway.toml`, `render.yaml`
- **CI**: `.github/workflows/ci.yml` (test + vet + build), `.github/workflows/release.yml` (GoReleaser + Docker)
- **Constraint**: linux/amd64 only (pg_query_go needs CGO, arm64 QEMU builds too slow)

## Logging

Uses Go's `log/slog` with structured key-value fields. All log calls use `slog.Info`, `slog.Warn`, `slog.Error`, or `slog.Debug` — never the old `log` package.

Key patterns:
- Watcher operations include `"watcher_id"` and `"watcher_title"` fields
- Connector operations include `"connector"` and `"type"` fields
- Errors always include `"error"` key
- MCP mode redirects all logging to stderr via `slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))`

## Debugging

- **pprof**: Available at `/debug/pprof/` (admin auth required) — heap, goroutine, CPU profiles
- **Dev mode**: Set `OPENTRACE_DEV=true` for live template reload and `/api/dev/hash` endpoint
- **Health check**: `GET /healthz` returns `{"status":"ok","version":"..."}`
- **Version**: `GET /api/version` returns build info; `GET /api/version/check` checks GitHub releases
- **Watcher runs**: `/api/watchers/{id}/runs/{runId}/events` streams SSE execution events
- **Logs**: Full-text search at `/logs` page or via MCP `log_search` tool

## MCP Workflow Patterns

The MCP server sends instructions during the `initialize` handshake and returns `suggested_tools` with pre-filled args in tool responses. This reduces round trips by guiding the agent through optimal tool chains.

### Entry points

| User intent | Start with |
|---|---|
| "What's wrong?" / investigating | `diagnose` |
| "System health" / status | `system_overview` |
| "What needs attention?" | `triage_alerts` |
| "Slow queries?" | `db_query_stats` |
| Full investigation playbook | `runbook` |

### Suggestion chains (tools that return `suggested_tools`)

- `diagnose` → `error_detail`, `log_search`, `watch_status`, `list_healthchecks`
- `error_groups` → `error_detail`, `diagnose`
- `error_detail` → `log_search` (with exception_class), `resolve_error`
- `log_search` → `log_context`, `error_detail`, `trace_lookup`
- `db_query_stats` → `explain_query` (with slowest query pre-filled)
- `triage_alerts` → `error_detail`, `investigate`, `uptime_status`, `diagnose`
- `investigate` → `log_search` (with service + error level), `diagnose`
- `uptime_status` → `diagnose` (for down endpoints)
- `system_overview` → `error_groups`, `log_summary`, `uptime_status`, `diagnose`
- `watch_status` → `diagnose`, `log_search`

### Adding new suggestions

Use the existing helpers in `internal/mcp/suggestions.go`:
```go
suggest("tool_name", "why this is suggested", map[string]any{"arg": "value"})
withSuggestions(resp, suggestions...)
```
