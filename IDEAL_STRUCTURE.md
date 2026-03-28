# OpenTrace: Ideal Project Structure

This document describes the "perfect" structure for OpenTrace if we could
rebuild from scratch with no migration cost. It follows the **Ports & Adapters
(Hexagonal) Architecture** adapted for Go idioms, inspired by
[Three Dots Labs](https://threedots.tech/post/introducing-clean-architecture/)
and [Alex Edwards](https://www.alexedwards.net/blog/the-fat-service-pattern).

## Design principles

1. **Domain is king** — business logic has zero dependencies on HTTP, MCP, SQL, or any framework
2. **Interfaces live next to consumers** — defined in the domain, not a shared bag
3. **Adapters are swappable** — database, transport, and external services are pluggable
4. **One domain, one directory** — everything about "errors" lives under one tree
5. **Every domain gets a service, no exceptions** — even thin CRUD wrappers go through a service for consistency. A developer never asks "does this have a service or not?"
6. **Test the domain, not the wiring** — 90%+ coverage on domain logic, lightweight integration tests on adapters

---

## Directory layout

```
opentrace/
├── cmd/
│   └── opentrace/
│       └── main.go                    # Wire everything, start server
│
├── internal/
│   │
│   │── domain/                        # ← CORE: Pure business logic, zero imports from adapters
│   │   │
│   │   ├── logs/
│   │   │   ├── service.go             # LogService: search, summarize, trace, compare
│   │   │   ├── service_test.go        # Unit tests with in-memory fakes
│   │   │   ├── model.go              # LogEntry, SearchParams, SearchResult (domain types)
│   │   │   └── repository.go          # LogRepository interface (port)
│   │   │
│   │   ├── errors/
│   │   │   ├── service.go             # ErrorService: list, investigate, impact, resolve
│   │   │   ├── service_test.go
│   │   │   ├── model.go              # ErrorGroup, ErrorImpact, Investigation
│   │   │   └── repository.go          # ErrorRepository, ErrorImpactRepository interfaces
│   │   │
│   │   ├── overview/
│   │   │   ├── service.go             # OverviewService: status, triage, diagnose
│   │   │   ├── service_test.go
│   │   │   └── model.go              # StatusReport, TriageEntry, DiagnoseReport
│   │   │
│   │   ├── analytics/
│   │   │   ├── service.go             # AnalyticsService: traffic, endpoints, trends, movers
│   │   │   ├── service_test.go
│   │   │   ├── model.go              # TrafficReport, EndpointStats, TrendData
│   │   │   └── repository.go          # AnalyticsRepository interface
│   │   │
│   │   ├── database/
│   │   │   ├── service.go             # DatabaseService: schema, queries, explain, runbooks
│   │   │   ├── service_test.go
│   │   │   ├── model.go              # TableInfo, QueryStats, RunbookResult
│   │   │   └── executor.go            # QueryExecutor interface (port)
│   │   │
│   │   ├── watches/
│   │   │   ├── service.go             # WatchService: create, evaluate, alert
│   │   │   ├── service_test.go
│   │   │   ├── model.go              # Watch, Alert, WatchRule
│   │   │   └── repository.go          # WatchRepository interface
│   │   │
│   │   ├── deploys/
│   │   │   ├── service.go
│   │   │   └── model.go
│   │   │
│   │   ├── healthchecks/
│   │   │   ├── service.go
│   │   │   ├── checker.go             # HTTP health check execution logic
│   │   │   └── model.go
│   │   │
│   │   ├── code/
│   │   │   ├── service.go             # CodeIntelService: risk, annotations, test gen
│   │   │   └── model.go
│   │   │
│   │   ├── auth/
│   │   │   ├── service.go             # AuthService: login, token validation, RBAC
│   │   │   ├── service_test.go
│   │   │   └── model.go              # User, Session, Role
│   │   │
│   │   └── notifications/
│   │       ├── dispatcher.go           # Notification dispatch logic
│   │       ├── deploy_watcher.go       # Post-deploy error spike detection
│   │       ├── error_watcher.go        # New error group detection
│   │       └── health_watcher.go       # Health check state transitions
│   │
│   ├── adapter/                       # ← INFRASTRUCTURE: Implements domain ports
│   │   │
│   │   ├── sqlite/                    # SQLite implementations of all repositories
│   │   │   ├── log_repo.go
│   │   │   ├── log_repo_test.go       # Integration test with in-memory SQLite
│   │   │   ├── error_repo.go
│   │   │   ├── watch_repo.go
│   │   │   ├── analytics_repo.go
│   │   │   ├── ... (one file per repository)
│   │   │   ├── migrate.go             # Schema migrations
│   │   │   └── sqlite.go              # Connection setup, WAL mode, pragmas
│   │   │
│   │   ├── postgres/                  # PostgreSQL connector (QueryExecutor)
│   │   │   ├── connector.go
│   │   │   └── connector_test.go
│   │   │
│   │   └── connector/                 # External data source registry
│   │       ├── registry.go
│   │       └── factory.go
│   │
│   ├── mcp/                           # ← MCP TRANSPORT: Primary interface for coding agents
│   │   ├── server.go                  # MCP server setup, auth, session tracking
│   │   ├── gateway.go                 # Single "opentrace" tool gateway
│   │   ├── handler_logs.go            # Thin: parse args → call LogService → format JSON
│   │   ├── handler_errors.go
│   │   ├── handler_overview.go
│   │   ├── handler_analytics.go
│   │   ├── handler_database.go
│   │   ├── handler_watches.go
│   │   ├── handler_admin.go
│   │   ├── handler_*.go               # One file per tool, each <100 lines
│   │   ├── response.go                # JSONResult, EmptyResult helpers
│   │   ├── args.go                    # ArgString, ArgInt helpers
│   │   ├── suggestions.go             # Suggestion builders
│   │   └── resources.go               # MCP resources
│   │
│   ├── api/                           # ← HTTP TRANSPORT: REST API + log ingestion
│   │   ├── router.go                  # Chi router setup
│   │   ├── middleware.go              # Auth, logging, CORS
│   │   ├── handler_logs.go            # Thin: parse request → call LogService → write JSON
│   │   ├── handler_errors.go
│   │   ├── handler_*.go
│   │   └── ingest.go                  # Log ingestion endpoint
│   │
│   ├── cli/                           # ← CLI TRANSPORT: Commands
│   │   ├── serve.go                   # Start server
│   │   ├── mcp.go                     # Start MCP stdio
│   │   ├── agent.go                   # Start VM agent
│   │   └── seed.go                    # Seed sample data
│   │
│   ├── telemetry/                     # ← SELF-MONITORING: OpenTrace observing itself
│   │   ├── startup.go                 # Startup timing, config validation logs
│   │   ├── store_metrics.go           # Query latency tracking for all store operations
│   │   └── goroutine_health.go        # Background worker health (watcher, healthchecker, logger)
│   │
│   ├── app/                           # ← APPLICATION: Wiring and lifecycle
│   │   ├── app.go                     # Application struct: builds all services + adapters
│   │   ├── config.go                  # Configuration loading
│   │   └── migrate.go                 # Database migration runner
│   │
│   └── testutil/                      # Shared test helpers
│       ├── fakes/                     # In-memory implementations of domain ports
│       │   ├── log_repo.go            # Implements domain/logs.LogRepository
│       │   ├── error_repo.go
│       │   └── ...
│       └── fixtures/                  # Test data builders
│           ├── logs.go
│           └── errors.go
│
├── migrations/
│   └── 001_initial.sql
│
├── pkg/                               # ← PUBLIC: Importable by external consumers
│   └── client/                        # Go SDK client for OpenTrace API
│       ├── client.go
│       └── client_test.go
│
├── go.mod
├── go.sum
├── CLAUDE.md
└── README.md
```

---

## Layer rules

### Domain layer (`internal/domain/`)

```
CAN import:     standard library only (context, time, fmt, errors, sort)
CANNOT import:  database drivers, HTTP frameworks, MCP SDK, any adapter
TESTED with:    in-memory fakes from testutil/fakes/
```

Every domain package has exactly 3-4 files:

| File | Purpose |
|------|---------|
| `model.go` | Types, enums, value objects — no methods with side effects |
| `repository.go` | Interface(s) this domain needs — defined HERE, not in a shared package |
| `service.go` | Business logic methods — takes repositories via struct fields |
| `service_test.go` | Unit tests using fakes — 90%+ coverage target |

**Key difference from current code:** Interfaces are defined next to the code that
needs them (Go best practice), not in a centralized `pkg/store/` package.

```go
// internal/domain/logs/repository.go

package logs

type LogRepository interface {
    Search(ctx context.Context, params SearchParams) ([]LogEntry, error)
    CountByLevel(ctx context.Context, params CountParams) (map[string]int, error)
    CountByService(ctx context.Context, params CountParams) ([]ServiceCount, error)
}
```

```go
// internal/domain/logs/service.go

package logs

type Service struct {
    repo LogRepository
}

func NewService(repo LogRepository) *Service {
    return &Service{repo: repo}
}

func (s *Service) Search(ctx context.Context, params SearchParams) (*SearchResult, error) {
    entries, err := s.repo.Search(ctx, params)
    if err != nil {
        return nil, fmt.Errorf("searching logs: %w", err)
    }
    // ... business logic: aggregate, compute, enrich ...
    return &SearchResult{
        Entries: entries,
        Total:   len(entries),
    }, nil
}
```

### Adapter layer (`internal/adapter/`)

```
CAN import:     domain layer, standard library, database drivers
CANNOT import:  port layer, other adapters
TESTED with:    integration tests using real (in-memory) databases
```

Each adapter implements one or more domain repository interfaces:

```go
// internal/adapter/sqlite/log_repo.go

package sqlite

import "github.com/adham90/opentrace/internal/domain/logs"

// Compile-time check.
var _ logs.LogRepository = (*LogRepo)(nil)

type LogRepo struct {
    db *bun.DB
}

func NewLogRepo(db *bun.DB) *LogRepo {
    return &LogRepo{db: db}
}

func (r *LogRepo) Search(ctx context.Context, params logs.SearchParams) ([]logs.LogEntry, error) {
    // SQL query implementation
}
```

### Transport layers (`internal/mcp/`, `internal/api/`, `internal/cli/`)

```
CAN import:     domain layer, application layer
CANNOT import:  adapter layer directly (receives domain interfaces)
TESTED with:    handler tests using mocked services
```

Handlers are THIN — <100 lines each. They only:
1. Parse transport-specific input (MCP args, HTTP request, CLI flags)
2. Call a domain service method
3. Format the response for the transport

```go
// internal/mcp/handler_logs.go

package mcp

func (h *Handlers) HandleLogsSearch(ctx context.Context, args map[string]any) (*CallToolResult, error) {
    params := logs.SearchParams{
        Query:   ArgString(args, "query"),
        Service: ArgString(args, "service"),
        Level:   ArgString(args, "level"),
        Limit:   ArgInt(args, "limit", 50, 500),
        Since:   GetSinceParam(args, time.Hour),
    }

    result, err := h.logs.Search(ctx, params)
    if err != nil {
        return NewToolResultError(err.Error()), nil
    }

    if result.Total == 0 {
        return EmptyResult("No logs found matching the criteria.")
    }

    return JSONResult(result,
        Suggest("logs", "View log context", map[string]any{"action": "context"}),
    )
}
```

### Application layer (`internal/app/`)

The wiring layer. Creates all services and adapters, connects them together:

```go
// internal/app/app.go

package app

type App struct {
    // Services (domain layer)
    Logs      *logs.Service
    Errors    *errors.Service
    Overview  *overview.Service
    Analytics *analytics.Service
    // ...

    // Cleanup
    db     *bun.DB
    closer []io.Closer
}

func New(cfg Config) (*App, error) {
    db, err := sqlite.Open(cfg.DataDir)
    if err != nil {
        return nil, err
    }

    // Create adapters (implement domain interfaces)
    logRepo := sqlite.NewLogRepo(db)
    errorRepo := sqlite.NewErrorRepo(db)
    // ...

    // Create domain services (pure business logic)
    return &App{
        Logs:     logs.NewService(logRepo),
        Errors:   errors.NewService(errorRepo),
        Overview: overview.NewService(logRepo, errorRepo, watchRepo, healthRepo),
        // ...
        db: db,
    }, nil
}
```

---

## What changes from current structure

### Current → Ideal mapping

| Current | Ideal | Why |
|---------|-------|-----|
| `pkg/store/iface_*.go` (19 files, centralized) | `internal/domain/*/repository.go` (per-domain) | Interfaces next to consumers, not in shared bag |
| `pkg/store/models_*.go` (19 files, centralized) | `internal/domain/*/model.go` (per-domain) | Models live with their business logic |
| `pkg/store/stores.go` (27-field god struct) | `internal/app/app.go` (wiring only) | No god struct passed at runtime |
| `internal/db/*.go` (33 files, one package) | `internal/adapter/sqlite/*.go` (same files, clearer name) | "adapter" communicates role |
| `internal/mcp/tools/*.go` (57 files, business+presentation) | `internal/mcp/handler_*.go` (thin) + `internal/domain/*/service.go` (logic) | Split presentation from business logic |
| `internal/mcp/server.go` + `Deps` struct | `internal/mcp/server.go` + receives `*app.App` | Transport doesn't own business deps |
| `internal/domains/` (80% stubs) | Deleted | Dead code removed |
| `internal/api/` (HTTP handlers) | `internal/api/` (same name, cleaner internals) | Already well-named |
| `internal/connector/` | `internal/adapter/connector/` | It's an adapter to external databases |
| `internal/services/` (new, 2 files) | `internal/domain/` | "domain" is more precise than "services" |
| (doesn't exist) | `internal/telemetry/` | Self-monitoring for store latency, goroutine health |

### The key insight

**Current:** Everything flows through the MCP tools package. Business logic, arg parsing,
JSON building, suggestions — all in one place. The "architecture" is:

```
MCP handler → Store interface → SQLite
    (does everything)
```

**Ideal:** Three distinct layers with clear dependency arrows:

```
Transport (MCP/API/CLI)  →  Domain (services)  →  Adapter (SQLite/Postgres)
     (parse + format)        (business logic)       (data access)
```

The domain layer is the **stable core** that rarely changes. Transports and adapters are
the **volatile edges** that change when you add new interfaces or switch databases.

---

## Dependency graph

```
                    ┌─────────────────┐
                    │   cmd/opentrace │
                    │   (main.go)     │
                    └────────┬────────┘
                             │ creates
                    ┌────────▼────────┐
                    │   internal/app  │
                    │   (wiring)      │
                    └──┬─────┬─────┬──┘
                       │     │     │
              ┌────────▼┐ ┌─▼──┐ ┌▼────────┐
              │   mcp/  │ │api/│ │  cli/    │
              │  (thin  │ │    │ │          │
              │handlers)│ │    │ │          │
              └────┬────┘ └─┬──┘ └───┬──────┘
                   │        │        │
                   ▼        ▼        ▼
              ┌──────────────────────────┐
              │     internal/domain      │
              │     (business logic)     │
              │   - logs/     - errors/  │
              │   - overview/ - watches/ │
              │   - analytics/- code/    │
              │   - deploys/  - auth/    │
              │   - database/ - health/  │
              └────────────┬─────────────┘
                           │ implements interfaces
              ┌────────────▼─────────────┐
              │     internal/adapter     │
              │   - sqlite/              │
              │   - postgres/            │
              │   - connector/           │
              └──────────────────────────┘

              ┌──────────────────────────┐
              │    internal/telemetry    │
              │   (self-monitoring)      │
              │   used by all layers     │
              └──────────────────────────┘
```

Arrows point DOWN only. Domain never imports adapter or transport. Transports never
import adapters directly (they receive domain interfaces). This is enforced by Go's
import system.

---

## What this buys you

| Benefit | How |
|---------|-----|
| **Test domain without infrastructure** | `logs.Service` tested with `fakes.LogRepo` — no SQLite, no MCP |
| **Add REST API without touching business logic** | New `api/handler_logs.go` calls same `logs.Service` |
| **Swap SQLite for Postgres** | New `adapter/postgres/log_repo.go` implements same `logs.LogRepository` |
| **Find any code in <5 seconds** | "Where is error investigation logic?" → `internal/domain/errors/service.go` |
| **New developer onboarding** | Read `domain/` to understand business rules, ignore adapter details |
| **No god objects** | Each service takes 1-3 repository interfaces, not a 27-field struct |
| **Compile-time dependency enforcement** | Go imports prevent domain→adapter violations |
| **Consistent patterns everywhere** | Every domain has a service, every handler is thin — no special cases |

---

## Domain error types

Every domain defines its own sentinel errors. Adapters wrap them. Handlers check them.

```go
// internal/domain/errors.go (shared across all domains)

package domain

import "errors"

var (
    ErrNotFound      = errors.New("not found")
    ErrNotConfigured = errors.New("store not configured")
    ErrInvalidInput  = errors.New("invalid input")
    ErrUnauthorized  = errors.New("unauthorized")
    ErrConflict      = errors.New("conflict")
)
```

```go
// adapter: wraps with context
return fmt.Errorf("sqlite: getting error group %s: %w", fingerprint, domain.ErrNotFound)

// handler: checks programmatically
if errors.Is(err, domain.ErrNotFound) {
    return NewToolResultError("error group not found"), nil
}
```

No more string matching. No more `fmt.Sprintf("failed to X: %v", err)` losing the
error chain. Every error is typed, wrapped, and checkable.

---

## Self-monitoring (`internal/telemetry/`)

OpenTrace monitors other apps but should also monitor itself:

```go
// internal/telemetry/store_metrics.go

// WrapRepo wraps any repository with latency tracking.
func WrapRepo[T any](name string, repo T) T {
    // Uses Go generics to wrap interface methods with timing
}
```

Track:
- **Startup timing** — how long each subsystem takes to initialize
- **Store query latency** — P50/P95/P99 for every repository method
- **Background goroutine health** — watcher evaluator, health checker, activity logger
- **MCP session metrics** — already partially done, formalize it

Exposed via `overview(action: "status")` so the agent can self-diagnose.

---

## Migration path from current to ideal

This is NOT a big-bang rewrite. It's an incremental migration:

1. **Create `internal/domain/` with one domain** (e.g., `logs/`)
   - Move models from `pkg/store/models_logs.go`
   - Define `LogRepository` interface in `domain/logs/`
   - Create `domain/logs/Service` with ALL methods extracted from `mcp/tools/`
   - Make `internal/db/log_store.go` implement `domain/logs/LogRepository`
   - Write service tests with fakes — target 90%+

2. **Update MCP handler to call the new service**
   - `handler_logs.go` becomes thin: parse args → call `logs.Service` → format JSON

3. **Repeat for EVERY domain** — no exceptions, even simple ones:
   - logs → errors → overview → analytics → watches → database →
     deploys → healthchecks → code → auth → notifications

4. **Add `internal/domain/errors.go`** with shared sentinel errors
   - Update all adapters to wrap with `%w`
   - Update all handlers to check with `errors.Is`

5. **Add `internal/telemetry/`** for self-monitoring
   - Store query latency wrappers
   - Background goroutine health

6. **Once all domains are migrated:**
   - Delete `pkg/store/` (interfaces moved to domain packages)
   - Rename `internal/db/` to `internal/adapter/sqlite/`
   - Delete `internal/domains/` stubs
   - Delete `internal/services/` (absorbed into `internal/domain/`)

7. **Create `internal/app/` wiring layer**
   - Move construction logic from `cmd/opentrace/main.go`
   - Replace `Stores` god struct with explicit service construction

8. **Clean up `pkg/`**
   - Move `pkg/server/` into `internal/` (unless it's imported externally)
   - Only keep `pkg/` if publishing a Go SDK client for external consumers

Each step is a standalone PR. The codebase works at every intermediate state.
Every domain gets a service — consistency over cleverness.

---

## File size targets

| Layer | Max file size | Typical |
|-------|--------------|---------|
| `domain/*/service.go` | 300 lines | 100-200 |
| `domain/*/model.go` | 200 lines | 50-100 |
| `domain/*/repository.go` | 50 lines | 20-40 |
| `adapter/sqlite/*.go` | 400 lines | 150-300 |
| `mcp/handler_*.go` | 150 lines | 50-100 |
| `api/handler_*.go` | 150 lines | 50-100 |

Total codebase: roughly same line count, but distributed across clear layers
instead of concentrated in `mcp/tools/`.

---

## References

- [Three Dots Labs — Clean Architecture in Go](https://threedots.tech/post/introducing-clean-architecture/)
- [Three Dots Labs — Wild Workouts DDD Example](https://github.com/ThreeDotsLabs/wild-workouts-go-ddd-example)
- [Three Dots Labs — Repository Pattern in Go](https://threedots.tech/post/repository-pattern-in-go/)
- [Alex Edwards — The Fat Service Pattern](https://www.alexedwards.net/blog/the-fat-service-pattern)
- [Jon Calhoun — Moving Towards DDD in Go](https://www.calhoun.io/moving-towards-domain-driven-design-in-go/)
- [Go Project Structure: Practices & Patterns (2025)](https://dev.to/rosgluk/go-project-structure-practices-patterns-22l5)
- [Hexagonal Architecture in Go](https://dev.to/buarki/hexagonal-architectureports-and-adapters-clarifying-key-concepts-using-go-14oo)
