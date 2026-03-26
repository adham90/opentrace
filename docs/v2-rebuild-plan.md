# OpenTrace v2 — Rebuild Plan

> If we were building OpenTrace from scratch, knowing what we know now:
> agent-first debugging tool, MCP is the primary interface, SQLite per tenant,
> cloud version wraps the OSS binary.

This document is the blueprint for a clean v2 rewrite or for guiding
major refactors on the existing codebase. Not everything needs to happen
at once — each phase is independently shippable.

---

## Guiding Principles

1. **MCP-first, web-second.** Coding agents (Claude Code, Codex, Cursor) are the primary users. The web UI is for human operators who want to see what agents see.
2. **One tool call, one answer.** Every MCP tool should return everything the agent needs in a single response. Pre-compute aggressively. Never make the agent chain 5 calls for one question.
3. **sqlc is the data layer.** Write SQL, generate Go. No hand-written row scanning. One model layer, not two.
4. **SQLite is the product.** The portable DB file is the competitive advantage. Each tenant gets their own process + their own DB. Export = download the file.
5. **Ship less, ship right.** 15 consolidated MCP tools, not 77. One job system, not three. One migration file, not 45.

---

## Phase 0: Foundation

**Goal:** Clean project scaffold with the right patterns from the start.

### Project structure

```
cmd/opentrace/
  main.go                  # single binary, subcommands: serve, mcp, agent, seed, backup
  domains.go               # domain module registration

pkg/
  server/                  # Module, Deps — importable by cloud repo
  store/                   # interfaces only (no models — sqlc generates those)

internal/
  db/                      # sqlc-generated code + store implementations
  web/                     # HTTP server, middleware, auth
  mcp/                     # MCP server + tools
  ingest/                  # log ingestion pipeline
  watcher/                 # unified watch/alert engine
  domains/                 # domain handlers (web routes)
  config/                  # env + config file parsing
  connector/               # external DB connections
  testutil/                # shared test helpers

migrations/
  embed.go
  000001_init.up.sql       # ONE migration — the complete schema

queries/                   # sqlc SQL files, one per domain
```

### Key decisions

- **sqlc from day one.** Models are generated, not hand-written. `pkg/store/` has interfaces only. `internal/db/models.go` is the single source of truth for types.
- **No `pkg/store/models_*.go`.** The sqlc-generated models in `internal/db/` are the models. Store interface methods accept and return generated types. Cloud repo imports `pkg/store/` for interfaces and `internal/db/` types via the `pkg/` re-export.
- **Config file support.** Add optional `opentrace.yaml` alongside env vars. Self-hosted users shouldn't need 20 env vars.

### Database schema

One migration file with the final schema. No evolutionary history. Git blame tells the story.

Tables (grouped by purpose):

**Core logging:**
- `logs` — structured log entries with FTS5 index
- `request_summaries` — per-request performance metrics (linked to logs)
- `ingest_batches` — deduplication tracking

**Error tracking:**
- `error_groups` — fingerprinted error aggregation
- `error_group_events` — lifecycle events (resolve, ignore, reopen)
- `error_impacts` — per-user impact tracking

**Monitoring:**
- `watches` — metric/query/log threshold monitors
- `watch_runs` — execution history
- `watch_alerts` — fired alerts with evidence
- `healthchecks` — HTTP endpoint probes
- `healthcheck_results` — probe results

**Infrastructure:**
- `data_sources` — connector configs
- `servers` — registered VM/container agents
- `metrics` — time-series server metrics

**Analytics (pre-computed for agent speed):**
- `metric_buckets` — time-bucketed aggregates
- `endpoint_stats` — per-endpoint performance
- `traffic_heatmap` — day/hour traffic patterns
- `deploy_markers` — first-seen commit hashes

**Tracing & journeys:**
- `trace_status` — distributed trace reassembly
- `user_sessions` — session reconstruction
- `funnels` — user-defined conversion funnels

**Investigation memory:**
- `investigation_sessions` — full session lifecycle with subsystem links
- `mcp_activity` — tool invocation audit trail
- `tool_transitions` — tool-to-tool transition stats for suggestions
- `workflow_templates` — curated/learned workflow steps

**Intelligence:**
- `agent_notes` — persistent AI agent notes per entity
- `query_memory` — historical query analysis findings
- `runbook_effectiveness` — playbook resolution rates
- `code_entities` — source code risk tracking
- `deploys` — deployment events with impact measurement
- `events` — generic CI/CD events
- `uncovered_error_paths` — test gap analysis

**Platform:**
- `users` — authenticated users
- `sessions` — browser sessions
- `app_config` — key/value settings
- `audit_log` — admin action audit trail
- `jobs` — background job queue

---

## Phase 1: MCP Server (the product)

**Goal:** Ship the MCP server with 15 consolidated tools. No web UI yet.

### Tool design

Each tool uses an `action` parameter to consolidate related operations:

| Tool | Actions | Purpose |
|---|---|---|
| `diagnose` | (no actions — single entry point) | System-wide health check: errors, performance, alerts, recent deploys |
| `logs` | search, context, attributes, stats, summary, performance, trace, compare | Full-text log search and analysis |
| `errors` | list, detail, resolve, ignore, impact, affected_users | Error tracking and triage |
| `database` | queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill | Database introspection and management |
| `watches` | list, create, delete, status, alerts, dismiss, acknowledge | Alert management |
| `healthchecks` | list, create, delete, status, uptime | Endpoint monitoring |
| `overview` | status, triage, timeline | System overview and incident timeline |
| `analytics` | summary, top_endpoints, heatmap, trends | Traffic and performance analytics |
| `deploys` | history, impact, record | Deployment tracking and correlation |
| `traces` | lookup, list | Distributed trace assembly |
| `journeys` | sessions, paths, funnels | User session reconstruction |
| `code_intel` | context, risk, fragile, test_gaps | Source code risk analysis |
| `notes` | add, get, list, delete | Persistent agent memory |
| `runbook` | slow_database, connection_exhaustion, disk_pressure, error_spike | Composite diagnostic playbooks |
| `admin` | settings, users, connectors, sampling | Administration (write operations) |

**15 tools, each with 3-10 actions. ~80 total operations.**

### Suggestion chains

Every tool response includes `suggested_tools` with pre-filled args:

```
diagnose
  → errors(action: "detail", fingerprint: "abc")
  → logs(action: "search", level: "error", service: "api")
  → watches(action: "status")

errors(action: "detail")
  → logs(action: "search", exception_class: "NoMethodError")
  → errors(action: "resolve", fingerprint: "abc")

logs(action: "search")
  → logs(action: "context", log_id: 42)
  → errors(action: "detail", fingerprint: "xyz")
  → traces(action: "lookup", trace_id: "abc-123")
```

### MCP instructions

Sent during `initialize` handshake. Tells the agent exactly where to start:

```
- Investigating issues → diagnose
- System health → overview(action: "status")
- What needs attention → overview(action: "triage")
- Slow queries → database(action: "queries")
- Full playbook → runbook(playbook: "slow_database")
- Always follow suggested_tools — args are pre-filled
```

### Deliverable

```bash
./opentrace mcp    # stdio MCP server, ready for Claude Code
```

Agent connects, calls `diagnose`, follows suggestions. Full debugging session.

---

## Phase 2: Ingestion & Storage

**Goal:** Accept logs from client libraries and populate the database.

### Endpoints

```
POST /api/logs          # batch log ingestion (gzip supported)
POST /api/servers       # agent registration + metrics
POST /api/deploys       # deploy webhook
POST /api/events        # generic CI/CD events
GET  /healthz           # liveness
GET  /readyz            # readiness (DB check)
```

### Ingestion pipeline

```
HTTP request
  → validate + decompress
  → apply sampling rules
  → async queue (bounded, non-blocking)
  → batch insert (SQLite WAL)
  → upsert error groups
  → update trace status
  → evaluate watch stream (trigger alerts)
  → update code entity risk scores
```

### Background jobs (one unified system)

```
jobs.Register("cleanup:sessions", sessionCleanup, 15*time.Minute)
jobs.Register("cleanup:stale_servers", staleServerCleanup, 60*time.Second)
jobs.Register("retention:prune", dataPrune, 1*time.Hour)
jobs.Register("aggregate:metrics", metricAggregation, 5*time.Minute)
jobs.Register("aggregate:endpoints", endpointAggregation, 5*time.Minute)
jobs.Register("aggregate:heatmap", heatmapUpdate, 5*time.Minute)
jobs.Register("aggregate:sessions", sessionBuild, 5*time.Minute)
jobs.Register("healthcheck:run", healthCheckRun, dynamic)  // per-check interval
jobs.Register("watch:evaluate", watchEvaluate, dynamic)     // per-watch interval
jobs.Register("deploy:measure", deployImpact, 15*time.Minute)
jobs.Register("risk:recompute", riskRecompute, 1*time.Hour)
```

One queue, one worker pool, one scheduler. No separate healthcheck.Scheduler or watcher.WatchScheduler.

### Deliverable

```bash
./opentrace            # HTTP server accepting logs + serving MCP
```

Client libraries send logs, agents query via MCP. System is functional.

---

## Phase 3: Web UI (for human operators)

**Goal:** Add a web dashboard for humans who want to see what agents see.

### Approach

The web UI consumes the same data the MCP tools use. No separate query paths.

```
internal/domains/dashboard/    # calls the same aggregation the MCP overview tool uses
internal/domains/logs/         # same filters the MCP logs tool uses
internal/domains/errors/       # same grouping the MCP errors tool uses
```

### Pages

| Page | What it shows |
|---|---|
| `/` | Dashboard — system health, attention items, recent activity |
| `/logs` | Log explorer with FTS search, filters, real-time polling |
| `/errors` | Error groups with fingerprinting, resolve/ignore |
| `/errors/:fp` | Error detail with histogram, affected users, logs |
| `/watchers` | Active watches, alerts, execution history |
| `/watchers/:id` | Watch detail with runs, evidence, baseline |
| `/servers` | Registered servers, metrics, agent install scripts |
| `/traces` | Recent traces, reassembly status |
| `/trends` | Time-series analytics with deploy markers |
| `/connectors` | External DB connector management |
| `/healthchecks` | Endpoint probes, uptime summaries |
| `/settings` | Retention, API keys, CORS, sampling rules |
| `/tools` | MCP tool catalog (what agents can do) |
| `/sessions` | Investigation session history (what agents did) |

### Stack

- Chi router + HTMX + templ (same as current — it works)
- Tailwind CSS (build via `make css`)
- No JavaScript frameworks, no SPA, no build step for HTML

### Deliverable

Full product: agents debug via MCP, humans observe via web UI.

---

## Phase 4: Cloud Readiness

**Goal:** Prepare the OSS binary for the managed SaaS platform.

### What the binary needs

```
OPENTRACE_MANAGED=true          # disable self-admin features
OPENTRACE_TRUST_PROXY_AUTH=true # trust X-Forwarded-User headers
OPENTRACE_MAX_DB_SIZE=500MB     # resource budget enforcement
OPENTRACE_WEBHOOK_URL=https://platform.internal/hooks/acct-123
```

### Managed mode

When `OPENTRACE_MANAGED=true`:
- Disable: onboarding wizard, user management UI, API key management UI
- Enable: usage reporting endpoint (`GET /api/admin/usage`)
- Trust: proxy auth headers for user identity
- Report: lifecycle events to webhook URL (alert fired, DB near capacity)

### Usage endpoint

```json
GET /api/admin/usage
{
  "logs_total": 142857,
  "logs_last_24h": 3200,
  "db_size_bytes": 52428800,
  "users": 3,
  "connectors": 2,
  "watchers_active": 5,
  "healthchecks": 3,
  "oldest_log": "2025-11-15T...",
  "newest_log": "2026-03-26T..."
}
```

### Cloud repo structure

```
github.com/adham90/opentrace         ← public (MIT)
github.com/adham90/opentrace-cloud   ← private (proprietary)

opentrace-cloud/
  cmd/opentrace-cloud/
    main.go           # imports public opentrace + adds cloud modules
    domains.go        # OSS domains + cloud domains
  internal/
    sso/              # OAuth, SAML
    teammanagement/   # org/team/invite
    usage/            # metering + billing webhook
    proxyauth/        # trust platform auth headers
```

### Export/portability

User clicks "Export" in platform → platform stops process → copies SQLite file → user downloads it → runs `./opentrace` with it → everything works, all history intact.

---

## Phase 5: sqlc Migration (current codebase)

**Goal:** Eliminate the dual-model problem in the existing codebase.

### Current state (the problem)

```
pkg/store/models_*.go     ← hand-written models (store.User, store.LogEntry)
internal/db/models.go     ← sqlc-generated models (db.User, db.Log)
internal/db/converters.go ← 500+ lines converting between them
```

Two representations of the same data. Every query result gets converted. Every new field must be added in two places.

### Target state

```
pkg/store/iface_*.go      ← interfaces only (no models)
internal/db/models.go     ← sqlc-generated models (THE models)
internal/db/*.sql.go      ← sqlc-generated query functions
```

sqlc-generated types are used everywhere. `pkg/store/` only exports interfaces. Consumers use `db.User`, `db.LogEntry` directly.

### Migration steps

1. Configure sqlc type overrides to produce clean Go types:
   - `time.Time` instead of `string` for timestamp columns
   - `bool` instead of `int64` for boolean columns
   - `map[string]any` instead of `string` for JSON columns
   - `uuid.UUID` instead of `string` for UUID columns

2. Move all sqlc queries from `queries/` to cover 100% of store operations (currently ~80%)

3. Delete `pkg/store/models_*.go` (18 files)

4. Delete `internal/db/converters.go`

5. Update all consumers to use `db.*` types instead of `store.*` types

6. `pkg/store/` becomes interfaces-only — cloud repo imports interfaces, implements against `db.*` types

### Risk

This is a breaking change for the cloud repo's import contract. Do it before the cloud repo has significant code.

---

## Phase 6: Unified Job System

**Goal:** Replace three separate schedulers with one.

### Current state

```
internal/watcher/watch_scheduler.go    ← watcher-specific scheduler
internal/healthcheck/scheduler.go      ← healthcheck-specific scheduler
internal/jobs/scheduler.go             ← generic job scheduler
internal/jobs/worker.go                ← generic job worker
```

### Target state

```
internal/jobs/
  queue.go        # persistent SQLite-backed queue
  worker.go       # goroutine pool processing jobs
  scheduler.go    # cron-like recurring job registration
  registry.go     # job type → handler mapping
```

Watches and health checks register as job types:

```go
jobs.Register("watch:evaluate", watcher.EvaluateJob, jobs.DynamicInterval)
jobs.Register("healthcheck:probe", healthcheck.ProbeJob, jobs.DynamicInterval)
jobs.Register("cleanup:sessions", cleanup.SessionJob, 15*time.Minute)
jobs.Register("aggregate:metrics", aggregate.MetricsJob, 5*time.Minute)
```

One system to monitor, one table to query, one place to add new recurring work.

---

## What NOT to Change

These decisions are correct and should survive any refactor:

- **SQLite + WAL mode** — the portable DB file is the product
- **Chi router** — lightweight, idiomatic, fast
- **HTMX + templ** — server-rendered, no JS build step
- **Module pattern** — `server.Module` with `mount()` for domain isolation
- **Store interfaces** — essential for testing and cloud repo
- **Pre-computed analytics** — agents need sub-100ms responses
- **Investigation sessions** — agent memory across sessions is critical
- **Suggested tools** — the killer MCP feature that makes agents efficient
- **Pure Go, no CGO** — cross-compilation, single binary deployment
- **Env-based config** — works for containers, compose, systemd

---

## Priority Order

If doing this incrementally on the existing codebase:

| Priority | Phase | Effort | Impact |
|---|---|---|---|
| 1 | Phase 5 (sqlc types) | Medium | Eliminates dual-model problem |
| 2 | Phase 6 (unified jobs) | Small | Simplifies operations |
| 3 | Phase 4 (cloud readiness) | Small | Unlocks SaaS revenue |
| 4 | Phase 1 (tool consolidation) | Medium | Better agent experience |
| 5 | Phase 3 (web UI cleanup) | Large | Better human experience |

Phases 0-2 only apply to a full rewrite. For the existing codebase, phases 4-6 are the high-value refactors.
