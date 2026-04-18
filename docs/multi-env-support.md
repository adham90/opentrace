# Multi-Environment Support

A plan to let one OpenTrace instance observe multiple environments (e.g. `staging` and `production`) and scope every agent interaction safely without forcing developers to manage multiple tokens or configs.

---

## Goal

A single OpenTrace deployment that:

- Ingests logs from staging **and** production SDKs in the same store.
- Lets a developer use **one MCP token** that authorizes which envs they can touch, while the agent specifies the target env per-call.
- Keeps watches, alerts, error groups, and connectors scoped to their env, so a "create a watch" call from a staging agent never lands in prod.
- Allows cross-env queries when explicitly needed without contortions.
- **Single-env users never notice this feature exists.**

## Non-goals

- Per-tenant isolation (different customers' data).
- Per-env retention / per-env disk quotas (out of scope, see [§ Storage](#storage-keep-merged-for-v1)).
- A web UI env switcher.
- Multiple tokens per user (deferred — see [§ Out of scope](#out-of-scope--future-work)).

---

## Design decisions

### 1. Environment is a tag, not a tenant boundary

Environment is a column on rows, not a separate database/store. The codebase already treats it that way (see [§ Current state](#current-state-audit)).

**Why:** cross-env queries stay trivial (the dictionary-encoded `env` column makes filtering essentially free), one WAL/seal pipeline, better compression, no fan-out at query time.

### 2. Authorization on the token, selection per-call

A user has **one MCP token**. The token carries `allowed_environments` — the set of envs that token may touch. Every tool call specifies which env to operate on; the server validates `target_env ∈ token.allowed_environments`.

**Why:** decouples "what you can access" (auth) from "which env this specific call is about" (selection). One token to issue, audit, revoke. Cross-env queries are just a tool call with multiple envs in the args. No proliferation of `.mcp.json` entries.

### 3. Single-env tokens auto-fill; multi-env tokens require explicit env

- Token scoped to **one env** (e.g. `["production"]`): `environment` is auto-filled from the token. Agent never thinks about it.
- Token scoped to **multiple envs** (e.g. `["staging","production"]`): `environment` is **required** on every read/write call. Server returns clear error if missing.
- Token scoped to **`["*"]`**: same as multi-env — `environment` required, with one exception (see decision #4 below).

**Why:** preserves the agent-friendly UX in the common case (one env per project) while making multi-env explicit and safe.

### 4. Backward-compat fallback for legacy `["*"]` tokens

Existing tokens (pre-multi-env) are backfilled to `["*"]` so reads still work. But after PR 3, write handlers would suddenly require explicit env, breaking every existing `watches:create` or `connectors:create` call.

**Rule:** if scope is `["*"]` AND `environment` is missing, fall back to `OPENTRACE_DEFAULT_ENV` and emit a `slog.Warn` with `event="env_fallback"` so the admin sees the deprecation. **Only applies to `["*"]`** — explicit multi-env tokens like `["staging","prod"]` still require the arg. Sunset plan: log warnings for 3 releases, then remove the fallback.

**Why:** the only safe way to ship PR 3 without a flag day is to grandfather `["*"]` tokens through the default. Explicit multi-env tokens are new and can have stricter rules from day one.

### 5. Default new-token scope is single-env

When `/connect` mints a new token, it defaults to a single env (`OPENTRACE_DEFAULT_ENV`, default `production`). Multi-env / `*` is opt-in by selecting multiple envs at the prompt.

### 6. Watches are strictly single-env, no fallback, no `*`

A watch's `environment` field is required and cannot be `*`. The `WatchCreate` handler **does not use** the `["*"]` backward-compat fallback — even legacy `*` tokens must pass `environment` explicitly when creating a watch.

**Why:** thresholds differ between envs. A `*` watch would fire on the wrong data and create false alerts. Watch creation is rare enough that an explicit param is fine.

### 7. Connector scope is bidirectionally enforced

Connectors carry their own `environment`. Two scope-mismatch cases:

- **Single-env connector × cross-env (or wider-scope) caller:** caller must specify `environment` matching the connector's env. A `*`-scoped agent querying a `production` connector must pass `environment="production"`.
- **`*`-connector × single-env caller:** allowed, but tool result includes a banner: `note: connector "warehouse" is shared across environments — these results are not scoped to your env (staging)`.

**Why:** prevents accidentally hitting prod database from a staging session, and prevents the agent from misattributing shared-infra results to its current env.

### 8. error_groups primary key becomes (fingerprint, environment)

A crash with the same fingerprint in staging and production produces **two distinct rows**. Today's single-row design causes silent merging — staging and prod share occurrence_count, last_seen_at, status. Splitting by env is the only correct option.

We add a `seen_in_envs` JSON array on each row for cross-env visibility ("this fingerprint also appears in production").

### 9. One log store, env as a column

We do **not** split `data/logs/` by env in v1. The dictionary-encoded `env` column makes query-time filtering essentially free. See [§ Storage](#storage-keep-merged-for-v1).

### 10. Admin actions are env-agnostic

Admin tools (`update_retention`, `users`, `audit`, settings) operate globally regardless of the admin's `allowed_environments`. Env scope only constrains data-access tools (logs, errors, watches, etc.).

**Why:** admin actions are by definition cross-env. An admin scoped to `["staging"]` who calls `users:list` should still see all users — that's an admin function, not a data-access function.

### 11. Ingest accepts envs no token can see

The ingest endpoint doesn't check token scopes — it stores whatever env the SDK sends. If an SDK sends `env="qa"` and no user has `["qa"]` scope, the data accumulates server-side, invisible until an admin grants someone the scope. **This is intentional** — it lets you start ingesting before you set up access.

---

## Single-env deployment story

If you only have one env, the env machinery is invisible. End-to-end:

1. **Install:** admin sets `OPENTRACE_DEFAULT_ENV=production` (or accepts the default).
2. **SDK:** sends `env=production` (or doesn't — server stamps with the default at ingest).
3. **Migration:** all historical `''` rows backfill to `production`.
4. **`/connect`:** prompt is `Environments [production]:` — hit enter.
5. **Agent calls:** `ResolveEnv` sees a single-env scope → auto-fills → tool args look identical to today's.
6. **`setup status`:** returns `env_scope: ["production"], scope_mode: "single"`.
7. **CLI:** `opentrace logs` works without `--env`.
8. **Watches/connectors/healthchecks:** all default to `production`.

The string `"production"` is never hardcoded — it's the default value of `OPENTRACE_DEFAULT_ENV`. A pre-launch team running only `staging` sets that var and everything reads `[staging]` instead.

When they later add a second env, nothing breaks: existing tokens stay single-env. Re-run `/connect` to widen the token; from then on, the agent learns to pass env explicitly from the first error message.

---

## Open questions (decide before implementing)

1. **Free-form env names or constrained enum?**
   Recommend: free-form, no validation. Add an admin-managed allowlist in `app_config` later if typo guardrails become a real need.

2. **What env do existing rows get on the upgrade migration?**
   Recommend: configurable via `OPENTRACE_DEFAULT_ENV` (default `production`). Phase A: one-time SQL UPDATE on every `environment = ''` row. Phase B: ingest layer requires non-empty env going forward (or stamps with the server default).

3. **What env does an admin-created user (no `/connect`) get for `allowed_environments`?**
   Recommend: empty `[]` on column default. Pre-migration users get backfilled to `["*"]` once. New users via admin tool must explicitly pick. New users via `/connect` pick at prompt.

---

## Current state audit

The foundation is **mostly built already**.

### What's already wired

| Layer | What exists | Where |
|---|---|---|
| Schema | `environment` column on `data_sources`, `error_groups`, `watches`, `metric_buckets`, `deploy_markers` | `migrations/000001_init.up.sql:94, 139, 229, 310, 334` |
| Log chunk schema | `env` is a dictionary-encoded column | `internal/logstore/chunk/schema.go:39, 104` |
| Log engine search | Filters by `Env` exact match | `internal/logstore/engine/store.go:443, 789` |
| Log store adapter | Maps `Environment ↔ Env` in `Search` | `internal/logstore/adapter/logstore.go:54-82` |
| Ingest payload | SDK sends `env` field; handler persists it | `internal/ingest/handler.go:67, 266` |
| `LogEntry` model | Has `Environment` field | `pkg/store/models_logs.go:17` |
| `LogSearchParams` | Has `Environment` filter | `pkg/store/models_logs.go:82` |
| `Watch` model | Has `Environment` field | `pkg/store/models_watches.go:134` |
| `CreateWatchParams` | Has `Environment` field | `pkg/store/models_watches.go:160` |
| `ErrorGroup` model | Has `Environment` field | `pkg/store/models_errors.go:23` |
| `ListErrorGroupParams` | Has `Environment` filter | `pkg/store/models_errors.go:58` |
| Analytics models | `MetricBucket`, `DeployMarker`, `TrendQueryParams` all have env | `pkg/store/models_analytics.go:17, 37, 49` |

### What's missing

| Gap | Where | Why it matters |
|---|---|---|
| `users.allowed_environments` column | `migrations/000001_init.up.sql:12-23` | No place to attach env permissions to a token |
| Token validation injects nothing into ctx | `internal/mcp/server.go:167-181` | Request handlers can't see permissions |
| `DataSource` model has no `Environment` field | `pkg/store/models_connectors.go:30-41` | Schema has the column, model doesn't expose it |
| `CreateDataSourceParams`, `ListDataSourceParams` no env | `pkg/store/models_connectors.go:43-60` | Can't filter or set env on connector CRUD |
| `ListWatchParams` no env filter | `pkg/store/models_watches.go:171-178` | Can't list watches scoped to one env |
| `LogCountParams` no env field | `pkg/store/models_logs.go:101-106` | Watcher can't count logs per env |
| `engine.CountByLevel` / `CountByService` ignore env | `internal/logstore/adapter/logstore.go:98, 116` | Even if param existed, engine call doesn't pass it |
| `RequestSummaryAggregateParams` likely no env | `internal/watcher/watch_metrics.go:211` | Watcher aggregations span all envs |
| `WatchMetrics.Measure()` signature | `internal/watcher/watch_metrics.go:24` | Doesn't take env, can't filter downstream |
| `error_groups` PK is just `fingerprint` | `migrations/000001_init.up.sql:137` | Same crash in two envs silently merges |
| `error_impacts` no env | `migrations/000001_init.up.sql:172-184` | Per-user impact mixes envs |
| `error_group_events` no env | `migrations/000001_init.up.sql:162-170` | Audit trail can't distinguish |
| `healthchecks` no env | `migrations/000001_init.up.sql:194-206` | Staging vs prod URLs can't be separated |
| `servers` no env | `migrations/000001_init.up.sql:100-113` | Agent metrics from staging boxes mix with prod |
| `watch_alerts`, `watch_runs` no env | watches table area | Querying "all prod alerts" requires a join |
| `audit_log`, `mcp_activity` no env | `migrations/000001_init.up.sql:44, 407` | Post-hoc audit can't distinguish env-targeted writes |
| Deep capture child tables no env | `sql_captures`, `http_captures`, `email_captures`, `audit_trail`, `file_captures` | Filtering deep-capture data requires joining `logs` |
| Watch notifier payload no Environment | `internal/watcher/watch_notify.go` `WatchWebhookPayload` | Slack alerts from same fingerprint in two envs are indistinguishable |
| Missing indexes for env filters | several tables | Adding `WHERE environment = ?` does table scans |
| MCP tool handlers don't accept `environment` arg | `internal/mcp/tools/*.go` | Agent has no way to specify target env |
| MCP write handlers don't auto-fill env | `internal/mcp/tools/watches.go` etc. | Created watches inherit no env |
| `apiclient.LogEntry` no `Environment` field | `internal/apiclient/types.go:77-88` | CLI can't show env per row |
| CLI no `--env` flag | `cmd/opentrace/cmd_logs.go`, `cmd_status.go` | No way to scope CLI calls |
| Connect script doesn't ask for env | `internal/routes/auth/connect_script.go` | New tokens have no scope set |
| `/api/auth/connect` doesn't return assigned scope | `internal/routes/auth/connect.go` | Connect script can't echo what was set |
| Setup `guide` action doesn't mention `OPENTRACE_ENV` | `internal/mcp/tools/setup.go` | Recommended SDK config silently single-env |

---

## How env scoping works at runtime

### `.mcp.json` — one entry, one token

```json
{
  "mcpServers": {
    "opentrace": {
      "type": "sse",
      "url": "https://opentrace.example.com/mcp-sse",
      "headers": {"Authorization": "Bearer xyz_token"}
    }
  }
}
```

### Server-side scope plumbing

```
SDK request (POST /api/logs)             — env from payload (or default), no auth scope check
   ↓
HTTP handler (MCP req)                    — middleware validates token → loads user
   ↓
ctx.Value(envScopeKey) = EnvScope{...}    — attached for handler use
   ↓
Tool handler                              — calls ResolveEnv → applies WHERE clause
   ↓
Store layer                               — receives Environment in params
```

### Resolution rule (single helper)

```go
// internal/mcp/tools/envresolve.go (NEW)

// ResolveEnv returns the env to operate on, or an error.
//   - If args["environment"] is provided: must be in scope, returned as-is.
//   - If not provided and scope is exactly one non-* env: that env, auto-filled.
//   - If not provided and scope is exactly ["*"]: OPENTRACE_DEFAULT_ENV +
//     deprecation warning (backward compat for legacy tokens).
//   - If not provided and scope is multi-env (e.g. ["staging","prod"]): error.
func ResolveEnv(ctx context.Context, args map[string]any) (string, error) {
    scope := mcp.ScopeFromContext(ctx)
    requested := ArgString(args, "environment")

    if requested != "" {
        if !scope.Allows(requested) {
            return "", fmt.Errorf("token not authorized for environment %q (allowed: %v)",
                requested, scope.Allowed)
        }
        return requested, nil
    }

    if env, ok := scope.SoleEnv(); ok {
        return env, nil
    }

    // Legacy ["*"] backward compat: fall back to default with warning.
    if scope.IsLegacyWildcard() {
        env := config.DefaultEnv()  // OPENTRACE_DEFAULT_ENV
        slog.Warn("env fallback",
            "event", "env_fallback",
            "scope", "*",
            "fallback_env", env,
            "tool", ToolNameFromCtx(ctx),
        )
        return env, nil
    }

    return "", fmt.Errorf("environment required — choose from %v", scope.Allowed)
}

// ResolveEnvStrict is used by HandleWatchCreate (and any other path that
// must NOT use the legacy fallback). Rejects ["*"] without an explicit env.
func ResolveEnvStrict(ctx context.Context, args map[string]any) (string, error) {
    scope := mcp.ScopeFromContext(ctx)
    requested := ArgString(args, "environment")
    if requested == "*" {
        return "", fmt.Errorf("watches require a specific environment, not *")
    }
    // Same as ResolveEnv but skips the legacy fallback branch.
    // ...
}
```

`SoleEnv()` returns `(env, true)` only when `Allowed` has exactly one entry that isn't `*`.
`IsLegacyWildcard()` returns true when `Allowed` is exactly `["*"]`.

---

## The work, broken into PRs

Each PR is independently shippable. PR 1 is no-op-but-safe; PR 2 changes data shape with a one-time backfill; PR 3 changes behavior.

### PR 1 — Token authorization model

**Goal:** add `users.allowed_environments`, plumb scope into request ctx, surface scope to the agent. **No read paths change behavior yet.**

**Files (~7):**

```
migrations/000001_init.up.sql            — add column, backfill existing users to ["*"]
pkg/store/models_users.go                — AllowedEnvironments []string
pkg/store/iface_users.go                 — update Create/Update params
internal/adapter/sqlite/user_store.go    — read/write JSON-encoded column
internal/mcp/envscope.go                 — NEW: EnvScope type + ctx helpers
internal/mcp/server.go                   — attach scope to ctx in Serve + SSE
internal/mcp/tools/setup.go              — add env_scope to setup status
```

**Migration:**

```sql
ALTER TABLE users ADD COLUMN allowed_environments TEXT NOT NULL DEFAULT '[]';

-- One-time backfill: existing users with tokens get full access
UPDATE users SET allowed_environments = '["*"]'
  WHERE allowed_environments = '[]'
    AND mcp_token IS NOT NULL;
```

**Verify before promising:** the MCP Go SDK may or may not allow custom fields in the `Initialize` response. If it does, surface `env_scope` there so the agent learns it before its first tool call. If it doesn't, the agent learns from `setup status` or from the first error message.

**Setup status output:**

```json
{
  "env_scope": ["staging", "production"],
  "scope_mode": "multi",
  "scope_warning": "you must specify environment=... on every call"
}
```

For single-env: `scope_mode: "single"`, `scope_warning: null`. For legacy `["*"]`: `scope_mode: "legacy_wildcard"`, `scope_warning: "this token uses the deprecated wildcard scope; missing env args fall back to <default>"`.

**Tests:** user_store round-trip, envscope helpers, server-side ctx attachment.

**Done when:** users carry env permissions, ctx carries scope, `setup status` reports it. Read paths unchanged.

---

### PR 2 — Schema completion + backfill

**Goal:** add `environment` to all tables that lack it, change `error_groups` PK, add indexes, backfill historical rows.

**Files (~8):**

```
migrations/000001_init.up.sql           — schema additions, PK change, indexes, backfill
pkg/store/models_*.go                   — add Environment to affected models
pkg/store/iface_*.go                    — add Environment to *Params
internal/adapter/sqlite/*_store.go      — read/write env (no filtering yet)
internal/ingest/handler.go              — denormalize env into deep-capture child rows
cmd/opentrace/migrate_logs.go           — NEW: rebuild log chunks with backfilled env
```

**Schema additions:**

```sql
-- Healthchecks
ALTER TABLE healthchecks ADD COLUMN environment TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_healthchecks_env ON healthchecks(environment);

-- Servers
ALTER TABLE servers ADD COLUMN environment TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_servers_env ON servers(environment);

-- Watch alerts + runs (env duplicated from parent watch for query speed)
ALTER TABLE watch_alerts ADD COLUMN environment TEXT NOT NULL DEFAULT '';
ALTER TABLE watch_runs   ADD COLUMN environment TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_watch_alerts_env ON watch_alerts(environment, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_watch_runs_env   ON watch_runs(environment, started_at DESC);

-- Deep capture children (denormalized from logs.env at ingest)
ALTER TABLE sql_captures   ADD COLUMN environment TEXT NOT NULL DEFAULT '';
ALTER TABLE http_captures  ADD COLUMN environment TEXT NOT NULL DEFAULT '';
ALTER TABLE email_captures ADD COLUMN environment TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_trail    ADD COLUMN environment TEXT NOT NULL DEFAULT '';
ALTER TABLE file_captures  ADD COLUMN environment TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_sql_captures_env   ON sql_captures(environment);
CREATE INDEX IF NOT EXISTS idx_http_captures_env  ON http_captures(environment);
CREATE INDEX IF NOT EXISTS idx_email_captures_env ON email_captures(environment);
CREATE INDEX IF NOT EXISTS idx_audit_trail_env    ON audit_trail(environment);
CREATE INDEX IF NOT EXISTS idx_file_captures_env  ON file_captures(environment);

-- Audit trails
ALTER TABLE audit_log    ADD COLUMN environment TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_activity ADD COLUMN environment TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_mcp_activity_env ON mcp_activity(environment, created_at DESC);

-- Indexes for tables that have env but no env-aware index
CREATE INDEX IF NOT EXISTS idx_error_groups_env ON error_groups(environment, status);
CREATE INDEX IF NOT EXISTS idx_watches_env      ON watches(environment, status);
```

**`error_groups` primary key change** (the only non-additive part):

SQLite can't change a PK in place. The dance:

```sql
-- 1. Drop child FKs (they reference the old PK)
PRAGMA foreign_keys = OFF;

-- 2. Rename + recreate error_groups
ALTER TABLE error_groups RENAME TO error_groups_old;
CREATE TABLE error_groups (
    fingerprint      TEXT NOT NULL,
    environment      TEXT NOT NULL DEFAULT '',
    -- ... all existing columns ...
    seen_in_envs     TEXT NOT NULL DEFAULT '[]',  -- JSON array
    PRIMARY KEY (fingerprint, environment)
);
INSERT INTO error_groups
  SELECT fingerprint, environment, /* ... */, json_array(environment) AS seen_in_envs
  FROM error_groups_old;
DROP TABLE error_groups_old;

-- 3. Rebuild error_impacts with composite FK + environment column
ALTER TABLE error_impacts RENAME TO error_impacts_old;
CREATE TABLE error_impacts (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    error_fingerprint TEXT NOT NULL,
    environment       TEXT NOT NULL DEFAULT '',
    user_id           TEXT NOT NULL,
    -- ... existing fields ...
    FOREIGN KEY (error_fingerprint, environment)
        REFERENCES error_groups(fingerprint, environment) ON DELETE CASCADE,
    UNIQUE(error_fingerprint, environment, user_id)
);
INSERT INTO error_impacts SELECT
    id, error_fingerprint,
    (SELECT environment FROM error_groups WHERE fingerprint = error_fingerprint LIMIT 1) AS environment,
    user_id, /* ... */
  FROM error_impacts_old;
DROP TABLE error_impacts_old;

-- 4. Same dance for error_group_events (composite FK + environment)
ALTER TABLE error_group_events RENAME TO error_group_events_old;
CREATE TABLE error_group_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    fingerprint TEXT NOT NULL,
    environment TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    FOREIGN KEY (fingerprint, environment)
        REFERENCES error_groups(fingerprint, environment) ON DELETE CASCADE
);
INSERT INTO error_group_events SELECT
    id, fingerprint,
    (SELECT environment FROM error_groups WHERE fingerprint = error_group_events_old.fingerprint LIMIT 1),
    action, reason, created_at
  FROM error_group_events_old;
DROP TABLE error_group_events_old;
CREATE INDEX idx_error_group_events_fp ON error_group_events(fingerprint, environment, created_at DESC);

PRAGMA foreign_keys = ON;
```

This is the most fragile part of the whole plan. **Take a backup before running this migration.** Test on a copy of a real DB first.

**Phase B backfill — historical `''` rows in SQLite:**

```sql
UPDATE logs              SET environment = ?default? WHERE environment = '';
UPDATE error_groups      SET environment = ?default? WHERE environment = '';
UPDATE watches           SET environment = ?default? WHERE environment = '';
UPDATE data_sources      SET environment = ?default? WHERE environment = '';
UPDATE healthchecks      SET environment = ?default? WHERE environment = '';
UPDATE servers           SET environment = ?default? WHERE environment = '';
UPDATE watch_alerts      SET environment = ?default? WHERE environment = '';
UPDATE watch_runs        SET environment = ?default? WHERE environment = '';
-- ... etc for every env-bearing table
```

`?default?` = `OPENTRACE_DEFAULT_ENV` (default `production`). Logged loudly: `"backfilled 12,400 rows in 'logs' to environment='production'"`.

**Logs in the segmented store are not in SQLite.** They need a separate backfill via a dedicated tool:

```bash
opentrace migrate rebuild-logs --default-env production
```

This rewrites chunk metadata for any chunk where rows have `env=''`, replacing them with the configured default. It's a one-time admin action documented in the upgrade notes. Until run, the engine read path treats `env=''` rows as wildcard-matching (transitional read-side fallback) so nothing disappears mid-upgrade.

**Ingest path change** (in same PR): `handler.go` writes deep-capture child rows tagged with the parent log's env. Done inline in `HandleIngestLogs` where deep_capture rows are created.

**Model + params updates:**

Add `Environment` field to: `DataSource`, `Healthcheck`, `Server`, `WatchRun`, `WatchAlert`, `ErrorImpact`, `ErrorGroupEvent`, `MCPActivity`, `AuditEntry`, plus `seen_in_envs []string` on `ErrorGroup`.

Add `Environment` to: `CreateDataSourceParams`, `ListDataSourceParams`, `CreateHealthcheckParams`, `ListHealthcheckParams`, `RegisterServerParams`, `ListServerParams`, `ListWatchParams`, `LogCountParams`, `RequestSummarySearchParams`, `RequestSummaryAggregateParams`, `LogHistogramParams`, `ImpactQueryParams`.

For tools needing multi-env in one call (cross-env compare actions), use slice: `Environments []string`. Empty = no filter; single element = single-env; multiple = `IN (?, ...)`.

**Stores:** update CRUD to read/write the new column. Do **not** add WHERE filters yet — that's PR 3.

**Tests:** migration round-trip, backfill correctness, model field round-trips, FK rebuild correctness for error_impacts/events.

**Done when:** every relevant table has env, `error_groups` is keyed by `(fingerprint, environment)`, FKs rebuilt, historical rows backfilled, indexes exist, deep-capture children get env at ingest. Behavior still unchanged.

---

### PR 3 — Read filter propagation + write handlers + watcher

**Goal:** wire env into every read path and every write path. Watcher subsystem becomes env-aware.

**Files (~28):**

#### 3a. Engine API additions

`internal/logstore/engine/store.go`:

```go
func (s *Store) CountByLevel(since, until time.Time, service, environment string) (map[string]int, error)
func (s *Store) CountByService(since, until time.Time, environment string) (map[string]int, error)
```

Both filter by env using the dictionary-encoded column. Adapter at `internal/logstore/adapter/logstore.go:98, 116` passes `params.Environment` through.

#### 3b. ResolveEnv helper + tool wiring

Add `internal/mcp/tools/envresolve.go` (sketched in [§ Resolution rule](#resolution-rule-single-helper) above). Every read-path handler:

```go
env, err := tools.ResolveEnv(ctx, args)
if err != nil {
    return tools.NewToolResultError(err.Error()), nil
}
params.Environment = env
```

Tools to update: `logs`, `errors`, `watches`, `healthchecks`, `overview`, `analytics`, `code`, `deep_capture`, `database`, `servers`, `connectors`, `setup`.

For tools that genuinely need multiple envs in one call (`logs.compare`, `errors.compare`), accept `environments: ["a","b"]` array; validate each is in scope.

#### 3c. Write-path handlers

`HandleWatchCreate` uses `ResolveEnvStrict` (no fallback, no `*`):

```go
env, err := tools.ResolveEnvStrict(ctx, args)
if err != nil {
    return tools.NewToolResultError(err.Error()), nil
}
params.Environment = env
```

Other write tools (`HandleHealthcheckCreate`, `HandleConnectorCreate`, `errors.resolve`, `errors.ignore`) use `ResolveEnv` (with backward-compat fallback for legacy `["*"]` tokens). For `errors.resolve`/`ignore`, accept `environment` to disambiguate which env's group to act on (PK is now composite).

#### 3d. Connector scope checks

When a tool calls a connector, before executing the query:

```go
caller_env, _ := tools.ResolveEnv(ctx, args)  // resolved env for this call
if connector.Environment != "*" && connector.Environment != caller_env {
    return errorf("connector %q is scoped to %q, but this call targets %q",
        connector.Name, connector.Environment, caller_env)
}
if connector.Environment == "*" && caller_env != "" {
    result.Warning = fmt.Sprintf(
        "connector %q is shared across environments — these results are not scoped to %q",
        connector.Name, caller_env)
}
```

#### 3e. Watcher subsystem

```go
// internal/watcher/watch_metrics.go
func (m *WatchMetrics) Measure(
    ctx context.Context,
    metric store.WatchMetric,
    service, endpoint, environment string,  // NEW
    window time.Duration,
) (float64, error)
```

Each `measure*` method passes `environment` to `LogCountParams` / `RequestSummaryAggregateParams`. The scheduler reads `watch.Environment` and threads through. Alerts inherit env from the watch.

#### 3f. Watch notifier payload

```go
// internal/watcher/watch_notify.go
type WatchWebhookPayload struct {
    AlertID        string  `json:"alert_id"`
    WatchID        string  `json:"watch_id"`
    Environment    string  `json:"environment"`  // NEW
    // ... rest unchanged
}
```

Slack messages include the env in their summary line: `[production] error_rate spiked on /checkout`.

#### 3g. CLI

```bash
opentrace logs --env staging --service api
opentrace status --env production
```

Pass `?env=staging` to existing CLI API endpoints. `apiclient.LogEntry` gets `Environment string`.

#### 3h. Connect script + endpoint

Update `internal/routes/auth/connect_script.go`:

```
  OpenTrace — connect your project
  Server: https://opentrace.example.com

  Email: dev@example.com
  Password: ************
  Environments for this token (comma-separated, or * for all) [production]: staging,production

  ✓ .mcp.json created (token scoped to staging, production)
```

POSTs `{"email":..., "password":..., "environments":["staging","production"]}` to `/api/auth/connect`.

`/api/auth/connect` response gains an `assigned_environments` field so the script can echo back what was actually set (server may clamp/default):

```json
{
  "token": "xyz...",
  "assigned_environments": ["staging", "production"],
  "scope_mode": "multi"
}
```

#### 3i. Setup guide includes OPENTRACE_ENV

`HandleSetupGuide` output adds `OPENTRACE_ENV=production` to the recommended SDK config snippet (Ruby: `config.environment = ENV.fetch('OPENTRACE_ENV', 'production')`; Node: equivalent). The agent reads this when setting up SDKs in new projects.

#### 3j. Admin tools bypass scope

`HandleAdminUsers`, `HandleAdminAudit`, `HandleAdminUpdateRetention`: **do not** call `ResolveEnv`. They operate globally regardless of the caller's scope. Add a comment in each handler explaining why (so future contributors don't add scope-checks "for consistency" and break this).

**Tests:** for each tool: scope=`["prod"]` filters to prod; scope=`["staging","prod"]` requires `environment=` arg; scope=`["*"]` falls back to default with warning (legacy compat); denied env returns clear error. Watch metrics tests: `Measure` only counts logs for specified env. Connector mismatch tests. Notifier payload includes env.

**Done when:** every read scopes; every write requires/auto-fills env; watcher evaluates per-env; CLI has `--env`; connect script lets you pick scope; admin tools work cross-env.

---

## Storage: keep merged for v1

```
data/logs/
  2026-04-04T10/
    chunk_000.col       ← env is column 11 of 45, dictionary-encoded
    chunk_000.idx
    meta.json
  2026-04-04T11/
    active.wal
```

Both prod and staging rows live in same hourly chunks. Filtering by env at query time is a bitmap intersect on the dictionary column — basically free.

### When to revisit splitting

Split into `data/logs/<env>/...` only if **one of these becomes true**:

1. You want different retention per env (keep prod 30d, staging 3d).
2. One env's volume is 100× the other and you want hot/cold separation.
3. You need per-env disk quotas.

Migration is a one-time replay. Add an option to `engine.NewStore`:

```go
engine.NewStore(rootDir, opts)
// opts.PartitionBy = "env"  → data/logs/<env>/<hour>/...
// opts.PartitionBy = ""     → data/logs/<hour>/...   (current behavior)
```

This is a future PR, not v1.

---

## Migration & rollout

### Order of operations

1. **Ship PR 1.** Existing users keep working (backfilled to `["*"]`); admin can grant env-scoped tokens to new users; agent surfaces scope via `setup status`.
2. **Ship PR 2.** Schema completes; SQLite backfill runs; `error_groups` PK changes; FKs rebuilt; deep-capture children gain env. Reads/writes still env-blind.
3. **Run `opentrace migrate rebuild-logs`** once on each deployment that has pre-env log data. Until run, engine read-path treats `env=''` rows as wildcard-matching (no data hidden).
4. **Ship PR 3.** Filtering activates everywhere. Watch notifier payloads gain env. Behavior changes: a `["staging"]` token now sees only staging data. Legacy `["*"]` tokens get backward-compat fallback with deprecation warnings.

### Rollback

- PR 1: revert code, leave column. Safe.
- PR 2: column ALTERs are forward-compatible; the `error_groups` PK change is **not** trivially reversible. **Take a backup before this migration.** Restore from backup if rollback needed.
- PR 3: revert code; data shape unchanged.

### Release notes (write these when shipping)

- **Breaking schema change:** `error_groups` rows are now keyed by `(fingerprint, environment)`. Same crash in two envs produces two distinct rows. Existing rows migrate 1:1 (no data loss).
- **Backward compatibility:** existing tokens are auto-set to `["*"]`. Reads behave identically. Writes from `["*"]` tokens auto-fill env from `OPENTRACE_DEFAULT_ENV` with a deprecation warning. Migrate to scoped tokens within 3 releases.
- **SDK requirement:** SDKs should send `env`. Current Ruby + Node SDKs already do. Legacy logs without `env` get stamped with `OPENTRACE_DEFAULT_ENV` at ingest.
- **Backfill default:** historical rows with `environment=''` rename to `OPENTRACE_DEFAULT_ENV` (default `production`) on upgrade. Set this env var before running migrations if you need a different default.
- **Required post-upgrade step:** run `opentrace migrate rebuild-logs` once to backfill log chunks. Until run, the engine treats unscoped legacy log rows as visible to all queries.
- **CLI:** `opentrace logs --env <name>` is new. Without it, queries use the token's default if single-env, or fail if multi.
- **Watch alerts:** Slack/webhook notifications now include `environment` in the payload. Update downstream parsers if they pin to the existing schema.

---

## Examples

### Example 1: single-env user (the silent path)

```bash
$ curl -s https://opentrace.example.com/connect | bash
  ...
  Environments for this token (comma-separated, or * for all) [production]: <enter>
  ✓ .mcp.json created (token scoped to production)
```

```
You:    Why is checkout slow?
Agent:  [calls logs(action: "performance", path: "/checkout")]
        → ResolveEnv: scope ["production"], no arg → auto-fill "production"

        Avg duration on /checkout is 920ms (baseline 240ms)…
```

### Example 2: multi-env user

```bash
$ curl -s https://opentrace.example.com/connect | bash
  ...
  Environments for this token [production]: staging,production
  ✓ .mcp.json created (token scoped to staging, production)
```

```
You:    Why is checkout slow?
Agent:  [calls setup(action: "status")]
        → env_scope: ["staging","production"], scope_mode: "multi"

        Your token covers staging and production. Which env?

You:    prod
Agent:  [calls logs(action: "performance", path: "/checkout", environment: "production")]
        → ResolveEnv: arg "production" ∈ scope → ok

        Avg duration on /checkout in prod is 920ms…
```

### Example 3: cross-env comparison

```
You:    Did this slowdown happen in staging?
Agent:  [calls logs(action: "compare", path: "/checkout",
                    environments: ["staging","production"])]

        Prod: 920ms, 4.2% errors
        Staging: 240ms, 0.3% errors
        Prod-only.
```

### Example 4: watch creation enforces single-env

```
You:    [from a multi-env token]
        Watch error rate above 1% on /checkout for 1h.
Agent:  [calls watches(action: "create", metric: "error_rate", value: 0.01,
                       service: "api", endpoint: "/checkout", duration: "1h")]
        → ResolveEnvStrict: no env arg, scope is multi → error

        Which env should this watch monitor — staging or production?

You:    prod
Agent:  [retries with environment: "production"]
        Watch created (id watch_a1b2c3, env: production).
```

### Example 5: legacy `*` token (backward compat)

```
You:    [from a token with allowed_environments=["*"] from before the migration]
        Create a watch for error_rate > 1% on api.
Agent:  [calls watches(action: "create", metric: "error_rate", ...)]
        → ResolveEnvStrict: no env arg, scope is "*" → error
          (watches NEVER use the legacy fallback)

        I can't create a watch with the wildcard token — pick an env.

You:    prod
Agent:  [retries] → ok, but server logs:
        slog.Warn event=token_legacy_wildcard tool=watches action=create
```

### Example 6: scope-denied error

```
You:    [from a ["staging"] token]
        Show me production errors for the last hour.
Agent:  [calls errors(action: "list", environment: "production")]
        → ResolveEnv: "production" ∉ ["staging"] → error

        I'm not authorized for production from this token (allowed: staging).
        Re-run /connect and select production if you want both.
```

---

## Out of scope / future work

- **Per-env retention.** `OPENTRACE_RETENTION_<ENV>=30d` env vars; retention job iterates per env.
- **Per-env disk quotas.** Requires the storage split sketched in [§ Storage](#storage-keep-merged-for-v1).
- **Per-env SDK ingest keys.** Per-env API keys (`api_key_staging_xyz`) auto-tagging ingested logs regardless of payload.
- **Env name allowlist.** Admin-managed `app_config.allowed_environments` for typo prevention.
- **Web UI env selector.** When/if a UI exists beyond the agent.
- **Per-env code intel risk scores.** Today's `code` tool aggregates across envs — defer.
- **Multiple tokens per user.** If single-token-per-user becomes limiting (e.g. one CI token + one local-dev token), add an `mcp_tokens` table later.
- **Session-bound active env.** Stateful "set active env per session" — fragile, deferred.
- **Removing the legacy `["*"]` fallback.** Currently grandfathered through `ResolveEnv` with a warning; remove after 3 releases.

---

## Punch list

```
[x] PR 1 — Token authorization model
    [x] migration: users.allowed_environments + backfill existing to ["*"]
    [x] User model + bun JSON marshaling
    [x] envscope package: EnvScope, With/From/FromOK, SoleEnv, IsLegacyWildcard
    [x] wire scope into ctx (Serve + SSE handler)
    [x] surface env_scope in setup status (Initialize meta deferred to PR 3)
    [x] tests

[ ] PR 2 — Schema completion + backfill
    [ ] add env column + index to: healthchecks, servers, watch_alerts, watch_runs,
        sql_captures, http_captures, email_captures, audit_trail, file_captures,
        audit_log, mcp_activity
    [ ] error_groups PK change to (fingerprint, environment) + seen_in_envs
    [ ] FK rebuild dance for error_impacts and error_group_events
    [ ] missing env-aware indexes on error_groups, watches
    [ ] one-time SQL backfill: '' → OPENTRACE_DEFAULT_ENV across env-bearing tables
    [ ] add Environment field to: DataSource, Healthcheck, Server, WatchAlert,
        WatchRun, ErrorImpact, ErrorGroupEvent, MCPActivity, AuditEntry,
        seen_in_envs to ErrorGroup
    [ ] add Environment to all *Params lacking it
    [ ] update SQLite stores to read/write the new column (no filtering)
    [ ] ingest path denormalizes env into deep-capture child rows
    [ ] cmd/opentrace/migrate_logs.go — rebuild-logs tool for chunk backfill
    [ ] migration test + backfill test + FK rebuild test

[ ] PR 3 — Read filter propagation + write handlers + watcher
    [ ] engine.CountByLevel/CountByService accept env
    [ ] adapter passes params.Environment through
    [ ] tools.ResolveEnv + ResolveEnvStrict helpers
    [ ] every read tool calls ResolveEnv → params.Environment
    [ ] every write tool except watches calls ResolveEnv (with legacy fallback)
    [ ] HandleWatchCreate uses ResolveEnvStrict (single-env required, no *)
    [ ] errors.resolve/ignore accept environment (PK is composite)
    [ ] connector scope check: single-env connector × wider caller
    [ ] connector "*" banner in tool results for single-env caller
    [ ] WatchMetrics.Measure takes environment
    [ ] every measure* method passes env to LogCountParams
    [ ] scheduler / stream evaluator pass watch.Environment through
    [ ] WatchWebhookPayload + log notifier include Environment
    [ ] admin tools (users, audit, update_retention) bypass scope checks
        (with comments explaining why)
    [ ] CLI: --env flag on logs / status
    [ ] apiclient.LogEntry adds Environment
    [ ] connect script: prompt for environments, post in body, store on user
    [ ] /api/auth/connect response includes assigned_environments + scope_mode
    [ ] setup:guide output mentions OPENTRACE_ENV in SDK config
    [ ] tests per tool: single-env, multi-env, *, denied, legacy wildcard fallback
```
