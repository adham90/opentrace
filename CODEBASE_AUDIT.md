# OpenTrace Codebase Audit

**Date:** 2026-03-30
**Scope:** ~84K lines of Go across 200+ files
**Build status:** Clean (`go build` and `go vet` pass, zero TODO/FIXME/HACK comments)

---

## Table of Contents

1. [Dead Code & Cleanup](#1-dead-code--cleanup)
2. [Code Duplication](#2-code-duplication)
3. [Simplification Opportunities](#3-simplification-opportunities)
4. [Maintainability Improvements](#4-maintainability-improvements)
5. [Testability Improvements](#5-testability-improvements)
6. [Architecture & Structure](#6-architecture--structure)

---

## 1. Dead Code & Cleanup

### 1.1 Deleted `internal/storage/` package — CLEAN
- **Files:** `internal/storage/local.go`, `local_test.go`, `storage.go` (git-deleted)
- **Status:** No remaining imports found. Deletion is clean. Can be committed as-is.

### 1.2 Duplicate compat.go — two copies of the same helper functions
- **Files:** `internal/mcp/compat.go` (84 lines) and `internal/mcp/tools/compat.go` (63 lines)
- **Problem:** Both define identical `NewToolResultText`, `NewToolResultError`, `GetArguments`, `MakeCallToolRequest`. The tools/ copy also adds redundant type aliases (`CallToolRequest`, `CallToolResult`). If one is updated, the other goes stale.
- **Recommendation:** Delete `internal/mcp/tools/compat.go`. Have all tool files import these helpers from the parent `internal/mcp` package. The parent copy already has the extra `SchemaProperty` and `ToolSchema` that the tools package needs.

### 1.3 Monolithic mock stubs file (932 lines)
- **File:** `internal/testutil/mocks/simple_stubs.go`
- **Problem:** 22 stub implementations crammed into one file. Hard to navigate or find a specific stub.
- **Recommendation:** Split into focused files by domain: `error_stubs.go`, `deploy_stubs.go`, `analytics_stubs.go`, etc. One file per logical group of 2-4 related stubs.

---

## 2. Code Duplication

### 2.1 Duplicate mock implementations — mocks exist in two places
- **Files:** `internal/api/mock_test.go` (980 lines, 16 mocks) AND `internal/testutil/mocks/` (1500 lines, 5 files + simple_stubs.go)
- **Problem:** `mockDataSourceStore` in mock_test.go is identical to the exported `DataSourceStore` in testutil/mocks/. Other mocks in mock_test.go (LogStore, UserStore, SettingsStore) also overlap with testutil versions. Fixing a bug in one won't fix the other.
- **Recommendation:** Consolidate all mocks into `internal/testutil/mocks/` as exported types. Delete the unexported duplicates from `mock_test.go`. Import the shared mocks where needed.

### 2.2 Repeated dynamic SQL WHERE-clause construction
- **Files:** `internal/adapter/sqlite/error_group_store.go` (lines 127-190), `log_store.go` (lines 186-352), and 3-4 other store files
- **Problem:** Each store method manually builds `[]string` conditions + `[]any` args, joins with `" AND "`, appends LIMIT/OFFSET. Same boilerplate repeated 5+ times.
- **Recommendation:** Extract a small `QueryBuilder` helper:
  ```go
  qb := NewQueryBuilder()
  qb.Where("status = ?", status)
  query, args := qb.Build("SELECT ... FROM error_groups")
  ```

### 2.3 Repeated type conversion helpers across MCP tools
- **Files:** `internal/mcp/tools/database_helpers.go` (`toInt64`, `toFloat64`, `toString`) and `internal/mcp/tools/logs_helpers.go` (`logsToInt`)
- **Problem:** Two separate files define overlapping type conversion functions with different naming conventions (`toInt64` vs `logsToInt`).
- **Recommendation:** Consolidate into a single set of helpers in one file (e.g., `internal/mcp/tools/convert.go`) with consistent names: `ToInt`, `ToInt64`, `ToFloat64`, `ToString`.

### 2.4 Repeated limit normalization across domain services
- **Files:** `internal/domain/analytics/service.go`, `internal/domain/logs/service.go`, `internal/domain/deploys/service.go`
- **Problem:** Each service independently clamps limit values (e.g., `if limit <= 0 { limit = 50 }; if limit > 500 { limit = 500 }`).
- **Recommendation:** Extract to a shared utility:
  ```go
  func ClampLimit(limit, defaultVal, max int) int
  ```

### 2.5 Repeated nil-check + ErrNotConfigured boilerplate in domain services
- **Files:** Most domain services (auth, connectors, deploys, logs, errors, overview, setup)
- **Problem:** Every service method starts with `if s.repo == nil { return ..., ErrNotConfigured }`. Repeated across dozens of methods.
- **Recommendation:** Extract to a helper or use a wrapper:
  ```go
  func (s *Service) ensureConfigured() error {
      if s.repo == nil { return ErrNotConfigured }
      return nil
  }
  ```

### 2.6 Inconsistent time parameter naming in MCP tools
- **Files:** `internal/mcp/tools/since.go`
- **Problem:** The `since.go` helper accepts three different parameter names — `since`, `time_range`, `timeframe` — because different tools use different names. This creates an inconsistent API surface for users.
- **Recommendation:** Standardize on one name (e.g., `since`) across all tools. Add the others as deprecated aliases if backward compatibility is needed.

---

## 3. Simplification Opportunities

### 3.1 SettingsStore interface — 16 methods for key-value pairs
- **File:** `pkg/store/iface_settings.go` (lines 12-29)
- **Problem:** 8 Get/Set pairs (Retention, APIKey, CORSOrigins, MaxQueryRows, StatementTimeout, MCPName, SamplingRules, TelegramConfig). This is really just a key-value store with typed wrappers. Every new setting requires adding two methods to the interface AND updating every mock.
- **Recommendation:** Replace with a generic typed approach:
  ```go
  type SettingsStore interface {
      GetSetting(ctx context.Context, key string) (string, error)
      SetSetting(ctx context.Context, key, value string) error
  }
  ```
  Then provide typed wrapper functions in the domain layer. This cuts the interface from 16 methods to 2 and eliminates mock maintenance burden.

### 3.2 InvestigationSession model — 57 fields
- **File:** `pkg/store/models_investigations.go` (lines 29-73)
- **Problem:** One struct with 57 fields covering identity, client info, classification, outcomes, metrics, timing, subsystem links, and related data. Hard to construct, hard to test, hard to evolve.
- **Recommendation:** Break into composed structs:
  ```go
  type InvestigationSession struct {
      Core       InvestigationCore       // ID, status, timestamps
      Client     InvestigationClient     // client type, model, token count
      Metrics    InvestigationMetrics    // tool calls, error count, duration
      Links      InvestigationLinks      // error group IDs, watcher IDs, deploy IDs
  }
  ```

### 3.3 WatchStore interface — 19 methods mixing three concerns
- **File:** `pkg/store/iface_watches.go`
- **Problem:** Single interface handles watch definitions CRUD, watch run execution tracking, AND alert management. Consumers that only need to read alerts must depend on the full 19-method interface.
- **Recommendation:** Split into `WatchStore` (definition CRUD), `WatchRunStore` (execution tracking), and `AlertStore` (alert management). Implementations can still share the underlying struct.

### 3.4 InvestigationStore — 14 methods mixing two concerns
- **File:** `pkg/store/iface_investigations.go`
- **Problem:** Mixes investigation session management (8 methods) with MCP activity logging (6 methods). These are distinct responsibilities.
- **Recommendation:** Split into `InvestigationSessionStore` and `MCPActivityStore`.

### 3.5 Oversized MCP tool files
- **Files:** `internal/mcp/tools/setup.go` (511 lines), `internal/mcp/tools/overview_diagnose.go` (368 lines)
- **Problem:** `setup.go` implements 5 different actions (status, detect, guide, db_guide, verify) in one file. `overview_diagnose.go` has the handler plus 5 collector functions.
- **Recommendation:** Split `setup.go` into `setup.go` (status/detect/verify) + `setup_guide.go` (guide) + `setup_db.go` (db_guide). Split `overview_diagnose.go` into handler + `overview_collectors.go`.

### 3.6 Config comma-separated parsing duplication
- **File:** `internal/config/config.go` (lines 112-140)
- **Problem:** `parseTrustedProxies` and `parseCORSOrigins` are identical logic — split a string on commas and trim whitespace.
- **Recommendation:** Replace both with a single `parseCommaSeparated(val string) []string` helper.

---

## 4. Maintainability Improvements

### 4.1 main.go does too many things (485 lines)
- **File:** `cmd/opentrace/main.go`
- **Problem:** Handles CLI command routing, app initialization, HTTP server setup, background job registration, and job handler implementations all in one file.
- **Recommendation:** Extract background job definitions into `cmd/opentrace/jobs.go`. Move job handler logic into domain services. Keep main.go focused on startup orchestration only.

### 4.2 No single place showing the complete HTTP route tree
- **Files:** `cmd/opentrace/main.go`, `cmd/opentrace/routes.go`
- **Problem:** `routes.go` only lists 4 domain modules, but actual routing includes middleware chains, page routes, admin routes, API routes, and static file serving — all scattered across main.go. You can't see the full picture in one place.
- **Recommendation:** Create `cmd/opentrace/http.go` that builds and documents the complete router structure with all middleware, route groups, and module mounts in one readable file.

### 4.3 Initialization sequence spread across multiple files
- **Files:** `cmd/opentrace/main.go`, `internal/app/app.go`, `pkg/server/deps.go`
- **Problem:** Config loading, DB setup, migrations, store creation, service wiring, and server setup are spread across three files with no documented order. If you change store initialization, you may need to update all three files.
- **Recommendation:** Create a `Bootstrap()` function in a single file that owns the full init sequence: config -> database -> stores -> services -> server. Document the order.

### 4.4 Background job handlers are anonymous closures capturing app
- **File:** `cmd/opentrace/main.go` (lines 360-485)
- **Problem:** Job handlers are anonymous functions that capture the `app` variable from the enclosing scope. You can't see what services each job needs without reading the closure body. Hard to test in isolation.
- **Recommendation:** Define job handler structs with explicit dependencies:
  ```go
  type SessionCleanupHandler struct { store store.SessionStore }
  func (h *SessionCleanupHandler) Run(ctx context.Context, payload json.RawMessage) error { ... }
  ```

### 4.5 Store struct embeds all 32 interfaces in Deps
- **File:** `pkg/server/deps.go` (embeds `store.Stores`), `internal/adapter/sqlite/stores.go`
- **Problem:** Every module receiving Deps can access every store. Adding a new store automatically exposes it everywhere. No way to tell which stores a module actually uses.
- **Recommendation:** Don't embed `store.Stores` in Deps. List each needed store as an explicit field, or group them (e.g., `LoggingStores`, `ErrorStores`). This makes dependencies visible.

### 4.6 Inconsistent transaction patterns in SQLite stores
- **Files:** `internal/adapter/sqlite/trace_store.go` (manual BeginTx/Rollback/Commit) vs `internal/adapter/sqlite/error_group_store.go` (RunInTx callback)
- **Problem:** Two different transaction management approaches coexist. The manual pattern is more error-prone (possible defer/commit ordering bugs).
- **Recommendation:** Standardize on the `RunInTx` callback pattern everywhere. It auto-handles rollback on error and is more idiomatic.

### 4.7 MCP tool response structure inconsistency
- **Files:** `internal/mcp/tools/overview_status.go` (uses typed `StatusReport` struct), `internal/mcp/tools/overview_diagnose.go` (uses untyped `map[string]any`)
- **Problem:** Some tools return typed structs, others use raw maps. Inconsistent makes it harder to understand what each tool returns.
- **Recommendation:** Pick one pattern and stick with it. Typed structs are safer and self-documenting — prefer those for complex responses.

### 4.8 Config fields lack documentation
- **File:** `internal/config/config.go` (lines 13-26)
- **Problem:** The Config struct has 8 fields with no comments explaining which system uses each one or what happens when they're unset.
- **Recommendation:** Add a one-line comment to each field: what it controls, which command/subsystem needs it, and the default.

---

## 5. Testability Improvements

### 5.1 Zero test coverage on `pkg/store/` (40 source files)
- **Directory:** `pkg/store/`
- **Problem:** The store package defines 19 interfaces and all model types — the core of the data layer — with zero tests. Model validation, parameter construction, and enum behavior are untested.
- **Recommendation:** Add tests for model construction, enum validity, and any helper functions in the store package. This is the highest-priority testing gap.

### 5.2 Flaky time.Sleep in worker tests
- **File:** `internal/jobs/worker_test.go` (line 71)
- **Problem:** Uses `time.Sleep(300ms)` to wait for async job processing. Will flake on slow CI. The test at line 34 already shows the correct pattern (timeout + polling), but line 71 uses a raw sleep.
- **Recommendation:** Replace the 300ms sleep with the same deadline+polling pattern used elsewhere in the file.

### 5.3 Light test coverage on infrastructure packages
- **Packages:** `internal/app` (1 test), `internal/httpclient` (1 test), `internal/logger` (1 test), `internal/metrics` (1 test), `internal/notify` (1 test), `internal/retry` (1 test)
- **Problem:** Each of these has only a single test file. Edge cases and error paths are likely uncovered.
- **Recommendation:** Add targeted tests for error paths and edge cases. Priority: `notify` (external HTTP calls), `retry` (backoff logic), `httpclient` (timeout/redirect behavior).

### 5.4 Only 2 files use `testing.Short()` guards
- **Files:** Only `internal/connector/database_test.go` and `database_cache_test.go` use short guards
- **Problem:** Other tests that depend on timing (jobs, watcher, healthcheck schedulers) don't have guards. They may be slow or flaky in `-short` mode.
- **Recommendation:** Audit all test files that use `time.Sleep`, goroutines, or schedulers. Add `testing.Short()` skip guards to any that need real timing or external resources.

### 5.5 Repeated test DB setup not using shared helpers
- **File:** `internal/jobs/worker_test.go` (local `setupTestDB`)
- **Problem:** Defines its own `setupTestDB()` instead of importing from `internal/testutil`. If the setup pattern changes, this local copy won't be updated.
- **Recommendation:** Replace all local `setupTestDB` functions with `testutil.SetupTestBunDB(t)`.

---

## 6. Architecture & Structure

### 6.1 Confusingly similar package names (not actually redundant)
These package pairs look like duplicates but serve different architectural layers. Listing them here to document the intended separation:

| Infrastructure (how) | Domain (what) | Relationship |
|---|---|---|
| `internal/connector/` | `internal/domain/connectors/` | Low-level interfaces vs business CRUD |
| `internal/healthcheck/` | `internal/domain/healthchecks/` | Runtime checker/scheduler vs domain service |
| `internal/notify/` | `internal/mcp/notifications/` | Telegram delivery adapter vs dispatch framework |

**Recommendation:** These are correctly separated. Add a brief comment at the top of each package doc explaining its role vs the related package. This prevents future developers from trying to "clean up the duplication."

### 6.2 Six large store interfaces (>10 methods each)
| Interface | Methods | File |
|---|---|---|
| SettingsStore | 16 | `pkg/store/iface_settings.go` |
| InvestigationStore | 14 | `pkg/store/iface_investigations.go` |
| TraceStore | 13 | `pkg/store/iface_traces.go` |
| ErrorStore | 12 | `pkg/store/iface_errors.go` |
| DeployStore | 11 | `pkg/store/iface_deploys.go` |
| QueryStore | 11 | `pkg/store/iface_queries.go` |

**Total:** 19 interfaces, 204 methods across the store layer.

**Recommendation:** The biggest wins come from splitting SettingsStore (item 3.1), WatchStore (item 3.3), and InvestigationStore (item 3.4). The others (Trace, Error, Deploy, Query) are at acceptable sizes for their domain complexity.

### 6.3 Thin domain services that are pure pass-throughs
| Service | What it does |
|---|---|
| `auth/service.go` | Delegates to UserStore with no logic |
| `connectors/service.go` | CRUD delegation to DataSourceStore |
| `deploys/service.go` | CRUD delegation + limit clamp |
| `healthchecks/service.go` | CRUD delegation to HealthCheckStore |
| `admin/service.go` | Settings + audit delegation |
| `analytics/service.go` | Store queries + limit clamp |

**Problem:** These services add an indirection layer with almost no business logic. Every store call goes through a service method that just calls the store method.

**Recommendation:** This is a judgment call. Thin services are fine if you expect business logic to grow (validation, authorization, events). But if a service has been thin since creation and hasn't grown, consider whether the HTTP handler can call the store directly. At minimum, don't add new thin services without a clear reason — put the logic where it actually lives.

---

## Priority Summary

### Do First (high impact, low effort)
| # | Item | Effort |
|---|---|---|
| 1.2 | Delete duplicate `tools/compat.go` | 10 min |
| 2.1 | Consolidate mock implementations | 1-2 hr |
| 2.4 | Extract shared `ClampLimit` helper | 15 min |
| 2.5 | Extract `ensureConfigured` helper | 30 min |
| 3.6 | Extract `parseCommaSeparated` config helper | 10 min |

### Do Next (high impact, medium effort)
| # | Item | Effort |
|---|---|---|
| 3.1 | Simplify SettingsStore to generic Get/Set | 2-3 hr |
| 2.2 | Extract SQL QueryBuilder helper | 1-2 hr |
| 4.1 | Extract job handlers from main.go | 2-3 hr |
| 4.6 | Standardize transaction patterns | 1-2 hr |
| 5.1 | Add pkg/store tests | 3-4 hr |

### Do When Convenient (medium impact)
| # | Item | Effort |
|---|---|---|
| 1.3 | Split simple_stubs.go into focused files | 1 hr |
| 2.3 | Consolidate MCP type conversion helpers | 30 min |
| 3.2 | Break up InvestigationSession (57 fields) | 2-3 hr |
| 3.3 | Split WatchStore interface | 2-3 hr |
| 3.5 | Split oversized MCP tool files | 1 hr |
| 4.2 | Create http.go showing full route tree | 1 hr |
| 4.3 | Create Bootstrap() init function | 2-3 hr |
| 4.5 | Stop embedding store.Stores in Deps | 2-3 hr |
| 5.2 | Fix flaky time.Sleep in worker tests | 15 min |
| 5.4 | Add testing.Short() guards | 1 hr |

### Nice to Have (low impact, improves clarity)
| # | Item | Effort |
|---|---|---|
| 2.6 | Standardize MCP time parameter names | 1 hr |
| 4.4 | Define job handler structs with explicit deps | 2 hr |
| 4.7 | Standardize MCP tool response types | 2-3 hr |
| 4.8 | Document Config struct fields | 30 min |
| 5.3 | Expand infra package test coverage | 3-4 hr |
| 5.5 | Replace local setupTestDB with testutil | 30 min |
| 6.1 | Add package-level doc comments for similar names | 30 min |
| 6.3 | Evaluate thin service necessity | design review |

---

## What's Working Well

Not everything needs fixing. These areas are solid:

- **SQL safety:** All queries use `?` placeholders. Zero injection risks found.
- **Migration schema:** Comprehensive indexing, FTS5 for log search, proper CASCADE/CHECK constraints.
- **Security boundary:** `internal/guardrail/sql_generic.go` is thorough and well-tested.
- **Job system:** `internal/jobs/` (queue + worker + scheduler) is well-designed with transactional claiming, exponential backoff, and dead-letter support.
- **Telemetry:** Startup timing, goroutine health, and store metrics are clean and useful.
- **Infrastructure packages:** cryptoutil, httpclient, retry, logger, version are all focused and well-implemented.
- **Module pattern:** `pkg/server/Module` for route registration is clean and scalable.
- **No dead config:** All config fields are actively used.
- **No TODO/FIXME debt:** Zero TODO, FIXME, HACK, or XXX comments in the codebase.
- **Clean build:** `go build` and `go vet` pass with zero warnings.
