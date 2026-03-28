# OpenTrace Code Cleanup & Refactoring Plan

## Current State

### The problem

The `internal/mcp/tools/` package is **18,000 lines** across 30 source files. Four files
exceed 600 lines. Every handler mixes three concerns: arg parsing, business logic, and
JSON response building. There is no service layer — handlers call stores directly and
return untyped `map[string]any` responses.

### By the numbers

| Metric | Count |
|---|---|
| Total source lines (non-test) | 18,000 |
| Files over 500 lines | 7 |
| `map[string]any` occurrences | 490+ |
| `json.Marshal` calls | 90+ |
| `args[...].(string)` extractions | 200+ |
| Duplicate helpers (`truncate`, `minInt`, `ParseTimeRange` vs `ParseTimeframe`) | 6 |
| Test coverage (`mcp/tools`) | 37.9% |

### Worst offenders

| File | Lines | `map[string]any` | Functions | Problem |
|---|---|---|---|---|
| `database_schema.go` | 1,555 | 60 | 9 | Tables + schema + storage + connections + kill all in one file |
| `overview.go` | 1,497 | 90 | 20 | Status, triage, diagnose, timeline, investigate, notes all in one file |
| `errors.go` | 1,000 | 43 | 13 | List through ranking, each 50-200 lines |
| `analytics.go` | 621 | 24 | 12 | 5 action handlers + 7 helper functions |
| `logs_search.go` | 556 | 17 | 5 | Search + attribute analysis in one handler |
| `admin.go` | 552 | 16 | 11 | Settings, users, audit, retention all mixed |
| `code_intel.go` | 527 | 24 | 10 | Risk, fragile, context, task builders |

---

## Architecture Target

Follow the **fat service pattern** (Alex Edwards) adapted for MCP tools:

```
Handler (thin)           Service (business logic)      Store (data access)
internal/mcp/tools/      internal/services/             pkg/store/ + internal/db/
                         or internal/<domain>/

Parse MCP args      -->  Typed method call          -->  DB queries
Format JSON response <-- Return typed struct        <--  Return models
Add suggestions          Aggregate, compute, validate    Already exists
```

### Principles

1. **Handlers are glue** — parse args, call service, format response. No business logic.
2. **Services return structs, not maps** — typed responses with JSON tags.
3. **One file, one responsibility** — no file over 400 lines.
4. **Shared helpers eliminate boilerplate** — arg parsing, response building, time ranges.
5. **Test the service, not the handler** — services are pure logic, trivially testable.

---

## Phase 1: Shared Helpers (Week 1)

**Goal:** Eliminate repeated boilerplate without changing any business logic.

### 1.1 Create `args.go` — typed argument extraction

Replace 200+ inline `args["key"].(string)` patterns.

```go
// internal/mcp/tools/args.go

func ArgString(args map[string]any, key string) string
func ArgStringDefault(args map[string]any, key, def string) string
func ArgInt(args map[string]any, key string, def, max int) int
func ArgFloat(args map[string]any, key string, def float64) float64
func ArgBool(args map[string]any, key string) bool
func ArgStringSlice(args map[string]any, key string) []string
func ArgDuration(args map[string]any, key string, def time.Duration) time.Duration
```

### 1.2 Create `response.go` — response building

Replace 90+ `json.Marshal` + `NewToolResultText` combos.

```go
// internal/mcp/tools/response.go

// JSONResult marshals resp, attaches suggestions, returns CallToolResult.
func JSONResult(resp any, suggestions ...ToolSuggestion) (*CallToolResult, error)

// JSONResultRanked marshals resp with ranked suggestions.
func JSONResultRanked(resp any, ranker SuggestionRanker, suggestions ...ToolSuggestion) (*CallToolResult, error)

// EmptyResult returns a "no data found" text result.
func EmptyResult(msg string) (*CallToolResult, error)
```

### 1.3 Consolidate duplicate helpers

| Current (duplicated) | Target (single location) |
|---|---|
| `truncate()` in errors.go | `helpers.go` |
| `truncateMsg()` in overview.go | Remove, use `truncate()` |
| `minInt()` in overview.go | Remove (use built-in `min` from Go 1.21) |
| `maxInt()` in analytics.go | Remove (use built-in `max` from Go 1.21) |
| `ParseTimeRange()` in analytics.go | `time_helpers.go` |
| `ParseTimeframe()` in overview.go | Remove, use `ParseTimeRange()` |
| `round2()`, `absFloat()` in analytics.go | `math_helpers.go` or inline `math.Round` |

### 1.4 Adopt helpers across all handlers

Mechanically replace boilerplate in all handler files. No logic changes.

**Expected impact:** ~1,500-2,000 lines removed. Every handler shrinks by 5-15 lines.

---

## Phase 2: Split Fat Files (Week 1-2)

**Goal:** No file over 400 lines. Pure file moves — no logic changes.

### 2.1 Split `overview.go` (1,497 lines -> 7 files)

| New file | Functions | Est. lines |
|---|---|---|
| `overview.go` | `OverviewHandler` (dispatch) + `OverviewDeps` | 70 |
| `overview_status.go` | `HandleOverviewStatus` | 130 |
| `overview_triage.go` | `HandleTriage` + `triageEntry` + `sevOrder` | 180 |
| `overview_diagnose.go` | `HandleDiagnose` + 5 `collect*` helpers + `buildDiagnoseSuggestions` | 280 |
| `overview_timeline.go` | `HandleTimeline` | 270 |
| `overview_investigate.go` | `HandleOverviewInvestigate` | 190 |
| `overview_changes.go` | `HandleChanges` | 140 |
| `overview_notes.go` | `HandleOverviewNotes` + `HandleOverviewDeleteNote` + `HandleOverviewSettings` + `ParseTimeframe` | 120 |

### 2.2 Split `errors.go` (1,000 lines -> 5 files)

| New file | Functions | Est. lines |
|---|---|---|
| `errors.go` | `ErrorsHandler` (dispatch) + `ErrorsDeps` + `ErrorsCatalogInfo` | 80 |
| `errors_list.go` | `ErrorsList` | 90 |
| `errors_detail.go` | `ErrorsDetail` | 130 |
| `errors_investigate.go` | `ErrorsInvestigate` | 270 |
| `errors_impact.go` | `ErrorsImpact` + `ErrorsUserErrors` + `ErrorsRanking` | 250 |
| `errors_resolve.go` | `ErrorsResolve` + `ErrorsIgnore` + `HandleNewErrors` + `truncate` | 120 |

### 2.3 Split `database_schema.go` (1,555 lines -> 4 files)

| New file | Functions | Est. lines |
|---|---|---|
| `database_tables.go` | `HandleTables` | 150 |
| `database_schema.go` | `HandleSchema` + `schemaTableDetail` + `checkConfigWarning` | 350 |
| `database_storage.go` | `HandleStorage` | 260 |
| `database_connections.go` | `HandleConnections` + `HandleKillQuery` + `HandleLongTransactions` | 400 |

### 2.4 Split `analytics.go` (621 lines -> 3 files)

| New file | Functions | Est. lines |
|---|---|---|
| `analytics.go` | `AnalyticsHandler` (dispatch) + `AnalyticsDeps` | 40 |
| `analytics_traffic.go` | `HandleTraffic` + `HandleEndpoints` | 160 |
| `analytics_trends.go` | `HandleHeatmap` + `HandleTrends` + `HandleMovers` | 350 |

### 2.5 Split remaining files over 500 lines

- `admin.go` (552) -> `admin.go` (dispatch) + `admin_settings.go` + `admin_users.go` + `admin_audit.go`
- `logs_search.go` (556) -> `logs_search.go` + `logs_attributes.go`
- `code_intel.go` (527) -> `code_intel.go` (dispatch) + `code_intel_risk.go` + `code_intel_context.go`

**Expected impact:** Max file size drops from 1,555 to ~400. Each file is independently
readable and testable.

---

## Phase 3: Typed Response Structs (Week 2-3)

**Goal:** Replace `map[string]any` with typed structs. Compile-time safety, better docs,
easier testing.

### Approach

Do this incrementally — one handler at a time, as you touch it. Don't do a big-bang
rewrite.

### Example: `ErrorsList`

```go
// BEFORE
resp := map[string]any{
    "total_unresolved": unresolvedCount,
    "returned":         len(summaries),
    "error_groups":     summaries,
}

// AFTER
type ErrorListResponse struct {
    TotalUnresolved int            `json:"total_unresolved"`
    Returned        int            `json:"returned"`
    ErrorGroups     []ErrorSummary `json:"error_groups"`
}
```

### Where to put response types

Option A (simpler): In the same file as the handler, above the function.
Option B (if reused): In `internal/mcp/tools/types.go` grouped by domain.

Start with Option A. Only move to B if you find yourself importing types across files.

### Priority order for conversion

1. `overview_status.go` — most-called handler, returns complex nested map
2. `errors_list.go` — returned in many tests
3. `analytics_traffic.go` — clean data shape, good proof of concept
4. Everything else — as you touch it

**Expected impact:** ~490 `map[string]any` occurrences reduced. Type-safe responses.
Tests can check struct fields directly instead of parsing JSON.

---

## Phase 4: Service Layer Extraction (Week 3-4)

**Goal:** Separate business logic from MCP presentation. Services are reusable and
independently testable.

### When to extract vs. when to leave

Extract a service when:
- The same logic is needed from both MCP tools and REST API/web handlers
- The handler function exceeds ~80 lines after Phase 1-3 cleanup
- You need to test aggregation/computation logic without MCP plumbing

Leave in handler when:
- It's a simple CRUD passthrough (fetch from store, return)
- Only called from one place (MCP tool)
- Logic is <40 lines

### Service placement

```
internal/
  services/
    overview/
      service.go        # OverviewService struct + methods
      service_test.go   # Pure unit tests — no MCP, no HTTP
    errors/
      service.go
    analytics/
      service.go
    database/
      service.go
```

Each service takes store interfaces via constructor injection:

```go
type OverviewService struct {
    logs   store.LogStore
    errors store.ErrorGroupStore
    // ...
}

func New(logs store.LogStore, errors store.ErrorGroupStore, ...) *OverviewService {
    return &OverviewService{logs: logs, errors: errors}
}

func (s *OverviewService) Status(ctx context.Context) (*StatusReport, error) {
    // pure business logic, returns typed struct
}
```

The MCP handler becomes:

```go
func HandleOverviewStatus(ctx context.Context, svc *overview.OverviewService) (*CallToolResult, error) {
    report, err := svc.Status(ctx)
    if err != nil {
        return NewToolResultError(err.Error()), nil
    }
    return JSONResult(report,
        Suggest("errors", "Check errors", Args{"action": "list"}),
    )
}
```

### Priority order for extraction

1. **Overview** — most complex, 6 store dependencies, most to gain
2. **Errors** — investigate/impact have complex aggregation
3. **Database** — schema introspection logic is reusable
4. **Analytics** — clean computation, good candidate

Don't extract: `healthchecks`, `deploys`, `servers`, `setup` — they're thin CRUD
wrappers that don't benefit from a service layer.

---

## Phase 5: Test Coverage Push (Ongoing, parallel with Phases 3-4)

**Goal:** Reach 70%+ coverage on `mcp/tools`, 80%+ on services.

### Strategy

- **Service tests** (new, high value): Test business logic with seeded mock data.
  These are the tests that were hard to write before because logic was tangled with
  MCP response building.
- **Handler tests** (existing, update): After service extraction, handler tests become
  trivial — mock the service, verify arg parsing and response shape.
- **Mock improvements**: Update `internal/testutil/mocks/` stores to support
  configurable return data (most already do, just need seeding).

### Coverage targets

| Package | Current | Target | How |
|---|---|---|---|
| `internal/services/overview` | new | 85%+ | Pure struct tests |
| `internal/services/errors` | new | 85%+ | Pure struct tests |
| `internal/mcp/tools` | 37.9% | 65%+ | Handler tests with mocked services |
| `internal/mcp/notifications` | 61.4% | 75%+ | Already mostly done |
| `internal/mcp` | 62.1% | 75%+ | Wiring tests |

---

## Execution Order

```
Phase 1 (helpers)          Phase 2 (split files)
  1.1 args.go         -->    2.1 overview.go
  1.2 response.go     -->    2.2 errors.go
  1.3 dedup helpers    -->    2.3 database_schema.go
  1.4 adopt everywhere -->   2.4 analytics.go
                             2.5 remaining 500+ files
      |                          |
      v                          v
Phase 3 (typed responses)  Phase 4 (services)
  One handler at a time      Start with overview
  As you touch it            Then errors, analytics
      |                          |
      v                          v
Phase 5 (tests) — runs in parallel with 3 and 4
  Service tests as services are created
  Handler tests updated as handlers slim down
```

### Rules of engagement

1. **One phase at a time.** Don't start Phase 4 before Phase 2 is done.
2. **Every change compiles and passes tests** before moving to the next file.
3. **No logic changes during file splits** (Phase 2). Pure moves only.
4. **Commit after each sub-step** (e.g., after splitting each file).
5. **Phases 3 and 4 are incremental** — do them per-handler as you work on features.
   Don't do a big-bang rewrite.

### What NOT to do

- Don't introduce interfaces for services until you have 2+ consumers.
- Don't create `internal/services/` directories for simple CRUD handlers.
- Don't abstract the `map[string]any` suggestion system — it works fine as-is.
- Don't rename packages or move store interfaces — that's churn with no value.
- Don't try to do all of this in one PR. Each phase is a separate PR.

---

## Success Criteria

After all phases:

- No source file in `internal/mcp/tools/` exceeds 400 lines
- `map[string]any` usage in handlers drops by 80%+
- Test coverage: `mcp/tools` >= 65%, services >= 85%
- Every handler is <50 lines (parse args, call service, format response)
- Business logic is testable without MCP/JSON plumbing
- New developers can find any handler in <10 seconds by filename
