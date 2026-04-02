# OpenTrace Cleanup Plan

Codebase audit: ~85k lines of Go across 34 SQLite tables, 18 store interfaces, 12 MCP tools with 80+ actions, and extensive background infrastructure. This plan identifies overengineered systems and low-value features, then provides a phased removal/simplification strategy.

---

## Phase 1: Remove Investigation Session Intelligence Layer

**What:** The entire investigation session tracking, tool transition ranking, context injection, recurrence detection, and workflow template system.

**Why:** This is a recommendation engine for MCP tool sequencing that adds ~5,000+ lines of code across 6 store interfaces, 5 runtime services, and 8 DB tables — all to predict which tool the LLM should call next. The LLM already decides tool ordering. The static `suggested_tools` in responses are sufficient. This system has zero user-facing value and adds significant complexity to the MCP server initialization path (see `server.go:190-265`, where 75 lines wire up stores into SessionTracker alone).

**Estimated removal: ~5,000–6,000 lines**

### Files to delete entirely

| File | Lines | Purpose |
|------|-------|---------|
| `internal/mcp/session_tracker.go` | ~660 | Session lifecycle, step recording, intent classification, close hooks |
| `internal/mcp/session_tracker_test.go` | ~520 | Tests |
| `internal/mcp/ranking.go` | ~245 | RankingService: transition-based tool suggestion ranking with TTL cache |
| `internal/mcp/ranking_test.go` | ~210 | Tests |
| `internal/mcp/context_injector.go` | ~635 | ContextInjector: enriches responses with 11 parallel context queries |
| `internal/mcp/context_injector_test.go` | ~670 | Tests |
| `internal/mcp/recurrence.go` | ~318 | RecurrenceDetector: links recurring investigations |
| `internal/mcp/recurrence_test.go` | ~170 | Tests |
| `internal/mcp/workflow_templates.go` | ~?? | Seeds default workflow templates |
| `internal/mcp/session_context.go` | ~?? | Session context builder |
| `internal/adapter/sqlite/investigation_session_store.go` | 788 | InvestigationSessionStore implementation |
| `internal/adapter/sqlite/investigation_session_store_test.go` | 595 | Tests |
| `internal/adapter/sqlite/tool_transition_store.go` | ~?? | ToolTransitionStore implementation |
| `internal/adapter/sqlite/tool_transition_store_test.go` | ~?? | Tests |
| `internal/adapter/sqlite/workflow_template_store.go` | ~?? | WorkflowTemplateStore implementation |
| `internal/adapter/sqlite/workflow_template_store_test.go` | ~?? | Tests |
| `internal/adapter/sqlite/query_memory_store.go` | ~?? | QueryMemoryStore implementation |
| `internal/adapter/sqlite/query_memory_store_test.go` | ~?? | Tests |
| `internal/adapter/sqlite/runbook_effectiveness_store.go` | ~?? | RunbookEffectivenessStore implementation |
| `internal/adapter/sqlite/runbook_effectiveness_store_test.go` | ~?? | Tests |
| `internal/adapter/sqlite/test_correlation_store.go` | ~?? | TestCorrelationStore implementation |
| `internal/adapter/sqlite/test_correlation_store_test.go` | ~?? | Tests |

### Store interfaces to delete

| File | Interface |
|------|-----------|
| `pkg/store/iface_investigations.go` | `InvestigationSessionStore` |
| `pkg/store/iface_workflows.go` | `ToolTransitionStore`, `WorkflowTemplateStore` |
| `pkg/store/iface_queries.go` | `QueryMemoryStore`, `RunbookEffectivenessStore` |

### Models to delete

| File | Models |
|------|--------|
| `pkg/store/models_investigations.go` | `InvestigationSession`, `CreateInvestigationSessionParams`, etc. |
| `pkg/store/models_workflows.go` | `ToolTransition`, `WorkflowTemplate`, etc. |
| `pkg/store/models_queries.go` | `QueryMemory`, `RunbookEffectiveness`, etc. |

### Files to edit

| File | Change |
|------|--------|
| `pkg/store/stores.go` | Remove 6 fields: `InvestigationSessionStore`, `ToolTransitionStore`, `WorkflowTemplateStore`, `QueryMemoryStore`, `RunbookEffectivenessStore`, `TestCorrelationStore` |
| `internal/adapter/sqlite/stores.go` | Remove corresponding store constructors |
| `internal/mcp/server.go` | Remove `SessionTracker`, `RecurrenceDetector`, `RankingService`, `ContextInjector` fields from `Deps`. Remove the 75-line wiring block (lines 190-265). Remove cleanup in Serve() |
| `internal/mcp/server_tools.go` | Remove session tracking integration, context injection calls |
| `internal/mcp/resources.go` | Remove `activeInvestigationsHandler` resource |
| `internal/mcp/tools/database.go` | Remove `SessionTracking` interface, `QueryMemoryStore`, `RunbookEffectivenessStore` from `DatabaseDeps` |
| `internal/mcp/tools/database_queries.go` | Remove query memory lookup (lines ~219-220) |
| `internal/mcp/tools/database_runbooks.go` | Remove `RunbookEffectivenessStore.RecordExecution` calls |
| `internal/mcp/tools/code.go` | Remove `TestCorrelationStore`, `InvestigationSessionStore` from `CodeDeps` |
| `internal/mcp/tools/code_intel.go` | Remove `TestCorrelationStore`, `InvestigationSessionStore` from `CodeIntelDeps`. Remove `test_gaps` and `test_priority` actions |
| `internal/mcp/tools/code_intel_context.go` | Remove investigation session lookups |
| `internal/mcp/tools/test_gen.go` | Remove `TestCorrelationStore` from `TestGenDeps` |
| `internal/mcp/tools/overview.go` | Remove `session_summary` action |
| `internal/app/app.go` | Remove telemetry fields (Phase 4 overlap) |
| `internal/testutil/mocks/*.go` | Remove mock implementations for deleted stores |
| `cmd/opentrace/background_jobs.go` | Remove cleanup jobs for `investigation_sessions`, `tool_transitions`, `query_memory`, etc. |

### DB tables to remove (add DOWN migration)

- `investigation_sessions`
- `tool_transitions`
- `workflow_templates`
- `query_memory`
- `runbook_effectiveness`
- `uncovered_error_paths` (TestCorrelationStore)

---

## Phase 2: Remove Journey Store

**What:** The entire user session / journey / funnel system.

**Why:** 766 lines of store implementation + 552 lines of tests + interface + models for user session reconstruction, path analysis, and funnel creation. **Not exposed through any MCP tool or HTTP endpoint.** Completely dead code — only referenced by its own implementation, tests, and the store constructor.

**Estimated removal: ~1,500 lines**

### Files to delete entirely

| File | Lines |
|------|-------|
| `internal/adapter/sqlite/journey_store.go` | 766 |
| `internal/adapter/sqlite/journey_store_test.go` | 552 |
| `pkg/store/iface_journeys.go` | 33 |
| `pkg/store/models_journeys.go` | ~?? |

### Files to edit

| File | Change |
|------|--------|
| `pkg/store/stores.go` | Remove `JourneyStore` field |
| `internal/adapter/sqlite/stores.go` | Remove JourneyStore construction |
| `internal/testutil/mocks/misc_stubs.go` | Remove JourneyStore mock |

### DB tables to remove

- `user_sessions`
- `funnels`

---

## Phase 3: Remove Database Runbooks

**What:** The pre-built investigation playbooks: `slow_database`, `connection_exhaustion`, `disk_pressure`, `replication_lag`, `error_spike`.

**Why:** 422 lines of canned pg_stat queries that the agent can compose itself from existing database tool actions (`queries`, `explain`, `activity`, `locks`, `connections`, `indexes`, `schema`, `storage`). The runbooks are a less flexible, hard-to-maintain copy of what the agent does naturally. The `RunbookEffectivenessStore` tracking is already removed in Phase 1.

**Estimated removal: ~450 lines**

### Files to delete

| File | Lines |
|------|-------|
| `internal/mcp/tools/database_runbooks.go` | 422 |

### Files to edit

| File | Change |
|------|--------|
| `internal/mcp/tools/database.go` | Remove `runbook` case from switch, remove `RunbookDeps`, remove `HandleRunbookAction` |
| MCP tool description/catalog | Remove "runbook" from available database actions |
| `internal/mcp/server.go` | Remove "runbook" from mcpInstructions |

---

## Phase 4: Remove Telemetry Package

**What:** `internal/telemetry/` — startup timing, goroutine health tracker, store query metrics.

**Why:** ~250 lines of self-monitoring infrastructure. Created in `internal/app/app.go` but never read or exposed anywhere meaningful. For a single-binary SQLite tool, the existing `slog` logging and Prometheus metrics endpoint (`/debug/metrics`) already cover observability. This package adds complexity without being consumed.

**Estimated removal: ~250 lines**

### Files to delete entirely

| File | Lines |
|------|-------|
| `internal/telemetry/startup.go` | 67 |
| `internal/telemetry/goroutine_health.go` | 92 |
| `internal/telemetry/store_metrics.go` | 86 |
| `internal/telemetry/telemetry_test.go` | ~?? |

### Files to edit

| File | Change |
|------|--------|
| `internal/app/app.go` | Remove `Startup`, `StoreStats`, `Workers` fields and their construction in `New()`. Remove `telemetry` import |

---

## Phase 5: Simplify Code Intelligence Tool

**What:** Reduce the `code` MCP tool from 14 sub-actions down to the core useful ones.

**Why:** The code tool currently has: `risk`, `fragile`, `context`, `test_gaps`, `test_priority`, `annotate_file`, `annotate_function`, `hotspots`, `gen_context`, `gen_suggest`, `gen_coverage`, `deps_service`, `deps_blast`, `deps_risk`. Many of these depend on `CodeEntityStore` data that is only populated through background aggregation from error counts — the underlying data is thin. After Phase 1 removes `TestCorrelationStore` and `InvestigationSessionStore`, several actions (`test_gaps`, `test_priority`, `context`) lose their data sources.

### Actions to remove

| Action | Why |
|--------|-----|
| `test_gaps` | Depends on `TestCorrelationStore` (removed in Phase 1) |
| `test_priority` | Depends on `TestCorrelationStore` (removed in Phase 1) |
| `context` | Depends on `InvestigationSessionStore` (removed in Phase 1) |
| `gen_coverage` | Depends on `TestCorrelationStore` |

### Actions to keep

`risk`, `fragile`, `annotate_file`, `annotate_function`, `hotspots`, `gen_context`, `gen_suggest`, `deps_service`, `deps_blast`, `deps_risk`

### Files to edit

| File | Change |
|------|--------|
| `internal/mcp/tools/code.go` | Remove entries from `actionMap` |
| `internal/mcp/tools/code_intel.go` | Remove handler functions for deleted actions |
| `internal/mcp/tools/test_gen.go` | Remove `HandleTestGenCoverage` |

---

## Phase 6: Simplify Circuit Breaker (Optional)

**What:** Replace the full circuit breaker (closed/open/half-open states) with simple retry-once-then-fail.

**Why:** 153 lines implementing the circuit breaker pattern for database connectors. In a tool where the human decides when to retry (by asking the agent again), the half-open/reset-timeout logic adds complexity for a marginal edge case. Every connector (Postgres, MySQL, Redis, Turso) wraps operations with it. A simpler "fail fast with a clear error" is sufficient.

**Risk:** Low — the circuit breaker never saves state across restarts, so removing it doesn't change observable behavior for most users. The only case it helps is rapid repeated queries to a down database, which is unlikely via MCP (one query at a time, human-driven).

**Estimated change: ~100 lines net reduction**

### Alternative: keep it
The circuit breaker is self-contained, well-tested, and doesn't impose maintenance burden on the rest of the codebase. It could stay as-is if the reduction isn't worth the churn.

---

## Phase 7: Assess Incomplete Connector Parity (Not a removal — a decision)

**What:** MySQL, Redis, and Turso connectors.

**Current state:**
- **Postgres**: Full introspection — `pg_stat_activity`, `pg_stat_statements`, `pg_locks`, `pg_stat_user_tables`, `pg_stat_replication`, EXPLAIN, schema, indexes, storage, connections, kill_query. All 12 database actions work.
- **MySQL**: Can connect, execute read queries, and basic info_schema lookups. No equivalent of the deep pg_stat introspection.
- **Redis**: Can connect and run INFO commands. No query introspection.
- **Turso**: LibSQL/Turso connector. Can connect and execute queries.

**Decision needed:** Either invest in parity (add MySQL equivalents for the Postgres introspection) or explicitly document these as "basic connectivity" connectors vs Postgres as the first-class citizen. No code removal needed — just a product decision.

---

## Execution Order

```
Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5
```

Phases 1-5 should be done in order because Phase 5 depends on Phase 1 (removing stores that code actions depend on). Phases 6 and 7 are independent and optional.

**After each phase:**
1. Run `go build ./...` to verify compilation
2. Run `go test -short -race ./...` to verify tests pass
3. Run `go vet ./...` for linting
4. Run `go mod tidy` if imports changed
5. Create a migration `000002_cleanup.up.sql` with `DROP TABLE IF EXISTS` for removed tables (can be batched at the end)

## Estimated Total Cleanup

| Phase | Lines removed (est.) | Tables removed |
|-------|---------------------|----------------|
| 1. Investigation intelligence | ~5,000–6,000 | 6 |
| 2. Journey store | ~1,500 | 2 |
| 3. Database runbooks | ~450 | 0 |
| 4. Telemetry package | ~250 | 0 |
| 5. Code tool simplification | ~300 | 0 |
| 6. Circuit breaker (optional) | ~100 | 0 |
| **Total** | **~7,500–8,600** | **8** |

This reduces the codebase from ~85k to ~77k lines, removes 8 of 34 tables, and eliminates 6 of 18 store interfaces — making the remaining code significantly easier to understand, test, and maintain.
