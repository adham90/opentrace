# Plan — OpenTrace for solo founders and small teams

Working plan from the design discussion. Ordered by value ÷ effort. Nothing here
adds a dependency, a dashboard, or a second storage engine.

> **Status (2026-08-31):** §1, §2, §3, §5, §6, §7 are implemented and tested.
> §4 is **blocked on missing data, not on effort** — see the section for what
> the verification turned up. Every section below keeps its original design
> notes; implementation notes are marked ✅.

## Principle

A team of one to five has no ops rotation, no one on call, and nobody staring at
a dashboard. The agent — OpenTrace's only interface — exists solely when someone
has Claude Code open. **Everything worth knowing happens while nobody is
looking.** So the work is not "more data"; 13 tools and 90+ actions is already
plenty. The work is making absence survivable:

1. Notice when OpenTrace itself goes quiet (§1)
2. Tell them what they missed instead of waiting to be asked (§2)
3. Triage without them (§6), and leave the diagnosis somewhere durable (§7)

Non-goals are listed at the end. They matter as much as the goals.

---

## 1. Dead man's switch

**Problem.** If the OpenTrace box dies, the founder hears silence and reads it as
"all good." Worst possible failure mode for a self-hosted monitor — and a solo
founder is exactly who won't notice. We already watch their app's heartbeat
(`WatchMetricHeartbeat`); nothing watches ours.

**Build.** `OPENTRACE_HEARTBEAT_URL` + a 60s schedule that GETs it. Points at
healthchecks.io / cronitor free tier — they own the external observer, we just
ping.

- `internal/jobs/scheduler.go` — the `Schedule{Name, JobType, Payload, Interval}`
  struct and `Add()` already exist; register one more in
  `cmd/opentrace/main.go:465` where `jobs.NewScheduler(jobQueue)` is wired.
- Worker handler: GET the URL with `internal/httpclient`, log failures at WARN,
  never retry (the next tick is the retry).
- Fires once at startup for free — `run()` already calls `maybeEnqueue` before
  the first tick.

**Edge.** Don't gate the ping on internal health checks. A ping that only fires
when everything is fine degrades into "no news" again; the point is proving the
process is alive. Liveness only.

**Effort.** ~20 lines + one test asserting the schedule is registered.

**✅ Implemented.** `internal/jobs/heartbeat.go` (`PingHeartbeat`,
`HeartbeatInterval`), registered in `startJobQueue` only when the URL is set.
Tests cover 2xx/4xx/5xx, an unreachable endpoint, and a cancelled context.
Routing it through the job queue is deliberate: a wedged SQLite stops the pings,
and an OpenTrace that cannot write is an OpenTrace that cannot alert.

---

## 2. `overview.catchup`

**Problem.** Triage today answers "what is broken now." Nobody is asking at 3am.
The first question of the morning is "what happened while I was gone," and for a
team of three, "what has nobody looked at yet."

**Build.** One `last_catchup_at` column on `users`. `overview.catchup` returns
every alert, new error group, and deploy since *that user's* cursor, then
advances it.

- Migration `000003_*.up.sql`: `ALTER TABLE users ADD COLUMN last_catchup_at TEXT;`
  (RFC3339 TEXT, per house style).
- Reuse the collectors in `internal/mcp/tools/overview_triage.go` — same shape,
  filtered `> cursor` instead of "currently unresolved."
- Env scope: `ResolveEnv(ctx, args)` exactly as `HandleTriage` does. A
  production-scoped token must not catch up on staging.

**Design notes.**

- Advance the cursor *after* a successful response, not on entry — a failed call
  must not silently swallow a night's events.
- Support `peek: true` (read without advancing) so an agent can re-read after a
  context compaction without losing the window.
- Per-user cursor is the whole point for teams: two people don't re-triage the
  same incident, and it doubles as a handoff.

**Effort.** One column, one action, ~120 lines. Highest value per line on this
list.

**✅ Implemented.** Migration `000003`, `UserStore.CatchupCursor` /
`SetCatchupCursor` (forward-only, so a concurrent drain cannot rewind the
window), and `overview(action: "catchup")` in
`internal/mcp/tools/overview_catchup.go`.

Caller identity rides on `envscope.EnvScope.UserID` — both transports already
build the scope from the same `*store.User`, so no new ctx plumbing. Windows are
clamped at both ends: a first-ever call looks back 24h rather than replaying all
history, and a stale cursor is clamped to 7 days so coming back from holiday
yields a queue rather than an archive. Collectors fetch `cap+1` so a single
overflowing source still trips the truncation notice — a full page returned
silently would read as "that was everything".

---

## 3. `users` tool — timeline / errors / impact

**Problem.** A founder spends more time on "customer X says checkout is broken"
than on aggregate error rate, and today that is unanswerable. For B2B, the
question is "which customer is down."

**Build.** New tool `users` with three actions, over columns we already store —
`user_id`, `tenant_id`, `session_id` (`internal/logstore/chunk/schema.go:48-50`)
plus audit trail, emails, and request captures.

| Action | Returns |
|---|---|
| `timeline` | Every request, error, email, audit entry for one `user_id`, newest first |
| `errors` | Error groups this user hit — wraps `ErrorImpactStore.GetUserErrors` |
| `impact` | Which users/tenants a fingerprint is hurting — wraps `GetAffectedUsers` |

**Design notes.**

- Zero new storage. It is a filter and a sort over the existing columnar scan.
- `tenant_id` gets the same treatment as `user_id` — same code path, different
  column. That is the B2B version and it costs one parameter.
- PII: this tool exists to look at one person's data, so it is the sharpest tool
  in the box. Reuse the existing scrubbing; do not add a bypass. Log every call
  to the audit log with the target user id.
- Cap `limit` at 500 per house rules.

**Effort.** ~200 lines, mostly a new `internal/mcp/tools/users.go` plus catalog
registration.

**✅ Implemented.** `internal/mcp/tools/users.go` with all three actions.

One thing the plan got slightly wrong: `user_id`/`tenant_id` were *stored* and
the engine had always filtered on them (`engine.SearchParams`), but they were
not reachable from `store.LogSearchParams` — so the filter was two fields of
plumbing, not zero. Added there and in the logstore adapter.

`impact` checks the error group is in the caller's env scope before listing
affected users; without that check it would be a way to enumerate users of an
environment the token cannot otherwise read. Audit is free — the MCP activity
log already records tool arguments, so every lookup is attributed to a caller
and a target.

---

## 4. AI spend

**Problem.** New solo-founder apps all call LLM APIs, and cost-per-user is the
number that kills them. "This user cost $40 on a $20 plan" is the screenshot
people share.

**⛔ Blocked — verified, and the data does not exist.** Do not start this.

Two things were confirmed against the running schema and the wire format:

1. **None of the deep-capture tables exist.** A migrated database has no
   `http_captures`, `sql_captures`, `email_captures`, `audit_trail`, or
   `file_captures`. They appear only in a test fixture
   (`deep_capture_test.go:72`). That means 7 of the `deep_capture` tool's 12
   actions query tables that are not there and fail at runtime — a live bug,
   independent of this feature, and worth its own fix. Deciding whether those
   tables should exist or whether the tool should read the body blob is a design
   call, not a mechanical one.

2. **The wire format carries no per-call HTTP data.** `flat_handler.go`'s `body`
   is free-form JSON whose documented well-known keys are `backtrace`,
   `exception_causes`, `handled`, `source_context`, `params`, `queries`. External
   calls appear only as the aggregates `ext_count` / `ext_ms` and
   `http_slowest_host`. There is nowhere a token count could be hiding.

So AI spend cannot be computed from data OpenTrace currently receives, at any
effort. Building the five columns and the parser now would be scaffolding for a
payload no SDK sends — the definition of speculative.

**What it would actually take**, in order:

1. Agree a wire-format key, e.g. `body.ai: [{vendor, model, input_tokens,
   output_tokens}]`. That is an SDK change in `opentrace-ruby` and
   `opentrace_node`, outside this repo.
2. Extract at ingest into sparse columns (`ai_vendor`, `ai_model`,
   `ai_tokens_in`, `ai_tokens_out`, `ai_cost_usd`) — sparse encoding means
   near-zero cost for the rows that are not LLM calls.
3. Confirm the chunk reader tolerates columns added after a segment was written
   (old segments must read back null, not error). Still unverified.
4. `analytics.ai_spend` then becomes a plain column scan, groupable by
   `user_id` / `path` / `day` with the filters that already exist.
5. Price table as a config file, not a constant. A wrong price is worse than no
   price.

**Build (assuming body blob).** Extract at ingest, not at query time:

- Add sparse columns: `ai_vendor`, `ai_model`, `ai_tokens_in`, `ai_tokens_out`,
  `ai_cost_usd`. Sparse encoding means near-zero cost for the 99% of rows that
  are not LLM calls.
- In the ingest pipeline, when an external call's host matches a known provider,
  parse the `usage` block and price it from a small static table.
- `analytics.ai_spend` then becomes a plain column scan — groupable by
  `user_id`, `path`, `day` with the filters that already exist.

**Design notes.**

- Parsing at query time means decompressing every body blob in the window. Do
  not. Extract once.
- The price table goes stale. Make it a config file, not a constant, and let it
  be overridden — a wrong price is worse than no price.
- **Open question:** does the chunk format tolerate segments written before a
  column existed? Old segments must read back as null, not error. Confirm in
  `internal/logstore/chunk/` before committing to a schema change.

**Effort.** Small on the server; the gate is the SDK change in step 1.

---

## 5. `since: "last_deploy"`

**Problem.** "Since the last deploy" is the actual question ~80% of the time,
and today it has to be translated into a wall-clock guess.

**Build.** `ParseSince` (`internal/mcp/tools/since.go`) is a pure function with
no store access, so this needs a resolver wrapper:

```go
func ResolveSince(ctx context.Context, d Deps, s string) (time.Time, error) {
    if s == "last_deploy" { /* look up */ }
    return ParseSince(s)
}
```

**The catch.** Deploys are not stored — they are *derived* at query time by
scanning logs for the first sighting of each `commit_hash`
(`internal/mcp/tools/overview_timeline.go:195-210`). Resolving `last_deploy`
that way means a full scan on every call.

**So: a `deploys` table.** At ingest, when a `(service, env)` pair's commit hash
changes, insert `(commit_hash, service, environment, first_seen_at)`. Then
`last_deploy` is one indexed lookup, deploy markers in
`analytics_trends.go:178` stop being a scan, and "compare this deploy to the
previous one" becomes free.

**Effort.** One table, one ingest hook, one resolver — then swap `ParseSince`
for `ResolveSince` at each call site.

**✅ Implemented, and more cheaply than planned.** Migration `000004` plus
`internal/adapter/sqlite/deploy_store.go`, recorded in `processAfterInsert`
(deduped within the batch; `INSERT OR IGNORE` absorbs repeats across batches).

The resolver did *not* need `ParseSince` changed at eleven call sites. It is one
middleware — `wrapWithDeployWindow` in `internal/mcp/deploy_window.go`, wired
into `wrapHandler` — which rewrites `since`/`time_range`/`timeframe` from
`last_deploy` into an RFC3339 timestamp before any handler runs. Every tool that
takes a window understands the token, and `ParseSince` stays a pure string
parser. The rewrite lands in the raw request payload, because `GetArguments`
re-unmarshals on every call and a mutated map would be discarded.

The env scope decides which deploy the caller sees: a staging-scoped token
asking for `last_deploy` must not get production's.

**Fixed along the way:** `mcpDeps()` in `internal/api/mcp_sse.go` was building
`store.Stores` field by field and had silently omitted `TraceStore` and
`CodeEntityStore`. Anything depending on them was nil over the HTTP/SSE
transports while working over stdio.

---

## 6. The on-call agent

**Problem.** Every competitor sends a graph. We can send a diagnosis — we are
the only tool where the entire triage capability is already an MCP endpoint.
What is missing is a caller when the human is asleep.

### Where it runs

**On the OpenTrace server, not their laptop.** The alert pipeline is
`internal/watcher/watch_notify.go`, on the VM. The laptop is closed at 3am —
that is the whole point.

### How it connects to Claude / Codex

Shell out to the founder's own CLI. We never hold an LLM key, never make an
outbound model call, and stay provider-agnostic for free.

```
OPENTRACE_ONCALL_CMD="claude"        # or: codex, gemini, any CLI
OPENTRACE_ONCALL_ENABLED=true        # default false
OPENTRACE_ONCALL_MAX_PER_DAY=10
OPENTRACE_ONCALL_TIMEOUT=5m
```

**Auth — this is the good part: it works on a Claude Max/Pro subscription.**

1. On their laptop, where a browser exists: `claude setup-token`
   ("Set up a long-lived authentication token (requires Claude subscription)").
2. On the server, in the systemd unit:
   `Environment=CLAUDE_CODE_OAUTH_TOKEN=...` — or `EnvironmentFile=` at mode 600.
3. `claude -p` picks it up. No browser on the VM, ever.

Marginal cost to the founder: zero, on a plan they already pay for. That is what
makes this default-on-worthy rather than a nice-to-have.

`claude install` ships a native build, so the VM does not need Node.

**Invocation:**

```go
cmd := exec.CommandContext(ctx, cfg.OnCallCmd,
    "-p", triagePrompt,
    "--mcp-config", "/etc/opentrace/mcp.json",
    "--allowedTools", "mcp__opentrace__logs mcp__opentrace__errors mcp__opentrace__overview mcp__opentrace__analytics mcp__opentrace__code",
    "--permission-mode", "dontAsk",
    "--output-format", "json",
)
cmd.Stdin = bytes.NewReader(alertJSON)   // NOT argv — see below
```

**Flag notes, verified against CLI 2.1.228:**

- Without `--permission-mode` it blocks forever on the first tool approval. This
  is the failure everyone hits once.
- **Do not use `--bare`.** Its help text says Anthropic auth is strictly
  `ANTHROPIC_API_KEY` — OAuth and keychain are never read. It looks like the
  right flag for a lean server run and silently defeats the subscription path.
- **There is no `--max-turns`** in current builds. Bound spend with
  `OPENTRACE_ONCALL_MAX_PER_DAY` plus the `CommandContext` timeout and kill.
- `--mcp-config` — do not depend on the daemon's cwd containing `.mcp.json`.
  Point at a file; the existing `curl /connect | bash` script generates it. Setup
  is literally "run our own connect script on the server."

### What it receives

`WatchWebhookPayload` (`internal/watcher/watch_notify.go:33`) — already built,
already marshalled — plus `alert.EvidenceJSON`, the `WatchEvidenceBundle` we
already assemble in `watch_evidence.go` (recent errors, new errors, affected
endpoints, relevant logs). Handing over the evidence means the agent starts with
it instead of burning three tool calls re-fetching what we had in memory.

Do not invent a third payload shape.

### Where it plugs in

A fourth notifier next to webhook / log / Slack / Telegram. Implement both
interfaces so health-check outages get the same treatment:

- `watcher.WatchAlertNotifier` — `NotifyWatchAlert(ctx, alert, watch)`
- `healthcheck.HealthCheckAlertNotifier` — `NotifyHealthCheckAlert(ctx, alert)`

No new plumbing; those interfaces are already the seam.

**✅ Implemented.** `internal/oncall/` — `config.go` (env parsing, invalid values
are startup errors rather than silent fallbacks), `prompt.go` (payload +
injection boundary), `runner.go` (execution, caps, status), `notifier.go`
(adapters for both alert interfaces), `github.go` (§7). Wired in
`cmd/opentrace/oncall.go`, appended last to both notifier lists so a slow agent
delays a diagnosis and never an alert.

`OPENTRACE_ONCALL_CMD` is a full argv, split without a shell — the default is
`claude -p --permission-mode dontAsk`, and stdout is treated as the diagnosis
verbatim so `codex exec` or anything else works without OpenTrace knowing its
output envelope. Health-check recovery ("it came back") is not triaged: running
the agent on recovery is how a flapping endpoint burns a day's quota by lunch.

### Security — not optional

- **Stdin, never argv.** `summary` embeds error messages from their app, which
  contain whatever a user typed into a form. On a command line that is a quoting
  hazard; in a prompt it is prompt injection.
- **Read-only tools only.** With `--permission-mode dontAsk`, an injected
  instruction reaches whatever we allow. `database.kill_query`,
  `errors.resolve`, and `admin.delete_user` must not be on the list. The agent's
  job is to explain and report; write access buys nothing.
- **The OAuth token is their Claude account** sitting in an env file on a VPS —
  same blast radius as their password. Never let it surface in
  `overview.settings`; follow the redaction pattern already tested in
  `setup_guide_secret_test.go`.
- **It expires, and then triage stops silently.** Record
  `last_oncall_success_at` and surface it in `overview.status`. Same silence
  problem as §1, same fix.

### The honest cost

Turning this on ships log excerpts to a model provider. Off by default, and say
so in the README in plain words rather than burying it — "no telemetry, fully
self-hosted" is part of why people pick us.

---

## 7. GitHub — issues, never PRs

**Why not PRs.** The on-call agent runs where the observability data is and the
code is not. Opening a PR needs a clone, a repo token, a toolchain, dependencies,
and a test runner — for every language our users write in. That is a second
product. **OpenTrace never clones a repo.**

**Instead:** diagnosis at 3am on the VM, fixes where the code lives.

```
gh issue create --title "[opentrace] NilPointerError in payments_controller.rb:87" --body "$DIAGNOSIS"
```

Body carries the diagnosis, the evidence bundle, the suspect commit, and
`file:line` — plus a ready-made fix prompt, since `code.gen_context` and
`gen_suggest` already produce exactly that. At 9am the founder opens Claude Code
where the repo and the tests are and says "fix #47." The hard part is done.

Token scope: issues, not write-to-repo.

**If they want an actual PR**, the mover is a workflow in *their* repo triggered
by the issue (`claude-code-action`), with repo context, CI, and branch
protection. We emit a well-formed trigger; their pipeline does the work. Zero
code-fixing machinery here.

**Human gate.** The issue body contains error text from their users' users. If an
Action auto-fires on issue creation, injected text flows into a code-writing
agent with repo write access. Require a label or comment to trigger the fix
workflow — one human between untrusted input and generated code.

### Dedupe — the part that decides whether this is usable

The identity already exists: `error_groups` is keyed `(fingerprint, environment)`
(`pkg/store/models_errors.go:24-25`). Same crash, same env, one issue, forever.

```sql
ALTER TABLE error_groups ADD COLUMN issue_url TEXT;
```

```go
// ponytail: single-process notifier, so read-then-write is safe.
//   Add a conditional UPDATE ... WHERE issue_url IS NULL if this ever runs multi-node.
eg, _ := d.ErrorGroupStore.Get(ctx, fp, env)
if eg.IssueURL != "" {
    return commentOnIssue(eg.IssueURL, diagnosis)   // recurrence → comment
}
url, err := createIssue(diagnosis)
if err != nil { return err }                        // nothing claimed; retry is clean
return d.ErrorGroupStore.SetIssueURL(ctx, fp, env, url)
```

Write the URL **after** GitHub succeeds. Claiming first and failing marks that
fingerprint as filed forever and it silently never files again — the worst
possible bug in a monitoring tool.

**Marker in the body**, so a restored DB backup does not cause double-filing:

```
<!-- opentrace-fp:a1b2c3d4 env:production -->
```

Costs nothing now; makes `gh issue list --search "opentrace-fp:..."` recovery
possible later. Write the marker now, defer the search until someone hits it.

**Non-error alerts have no fingerprint.** Latency and volume watches key on
`watch_id` instead. `watch_evaluator.go:178` already suppresses while a watch is
still triggered, so this only bites on flap — a 24h cooldown per `watch_id`
covers it: inside the window, comment; outside, new issue.

**Free lifecycle mirroring.** `ErrorGroupStore.Resolve` / `Reopen` already exist.
Hook them: resolved → close the issue; reopened → reopen and comment. The GitHub
side stops drifting from reality without anyone maintaining it.

**Do not** get clever about near-duplicate fingerprints. Exact match, one issue,
comment on recurrence.

**✅ Implemented.** Migration `000005`, `ErrorGroupStore.IssueURL` /
`SetIssueURL` (conditional `UPDATE … WHERE issue_url IS NULL`, so two filers
cannot orphan each other's issue), and `internal/oncall/github.go`.

**One hole the plan missed, now closed:** commenting on a recurrence is not
enough. Once the operator closes the issue, the dedupe works perfectly and the
recurrence lands as a comment on a closed issue nobody is watching — the alert
is invisible and the system looks healthy. Recurrences now `gh issue reopen`
first; reopening an already-open issue errors harmlessly and is ignored, which
is cheaper than a round trip to ask.

**One behaviour softened:** if the error group row does not exist yet (ingest
has not upserted it, or it was pruned), the issue is still filed and the failure
to link is logged rather than returned. The issue is real and sitting in the
tracker; reporting the run as broken would be a lie. Recurrences then fall back
to the cooldown.

Resolve → close is deliberately *not* automated. The operator closing an issue
they have read is the normal flow, and the dangerous direction — a closed issue
hiding a live recurrence — is handled by the reopen above.

---

## Suggested order

| # | Item | Status |
|---|------|--------|
| 1 | Dead man's switch | ✅ done, tested |
| 2 | `overview.catchup` | ✅ done, tested |
| 6 | On-call agent | ✅ done, tested |
| 7 | GitHub issues + dedupe | ✅ done, tested |
| 3 | `users` tool | ✅ done, tested |
| 5 | `last_deploy` + deploys table | ✅ done, tested |
| 4 | AI spend | ⛔ blocked — the data does not exist yet |

Migrations `000003`–`000005`. `go build ./...`, `go vet ./...`, and
`go test -short -race ./...` all pass.

## Follow-ups this work uncovered

Neither is in scope for the plan; both are real.

1. **`deep_capture` queries tables that do not exist.** `http_captures`,
   `sql_captures`, `email_captures`, `audit_trail`, and `file_captures` have no
   DDL outside a test fixture, so 7 of the tool's 12 actions fail at runtime.
   Either the tables were dropped when deep capture moved into the log body blob
   and the tool was left behind, or the DDL was never written. Needs a design
   call before a fix.

2. **Deep-capture data has no home in the wire format.** The `body` blob's
   documented keys cover errors and queries but not external HTTP calls, so
   anything per-call (AI spend, third-party latency attribution, egress cost) is
   blocked on an SDK-side format decision.

## Verification results

- **Where do external HTTP captures live?** Nowhere. All five deep-capture
  tables are absent from the migrated schema, and the wire format has no
  per-call HTTP data. §4 is blocked; the `deep_capture` tool has a live bug.
- **Does anything already write a deploy record?** No — it was derived-only,
  reconstructed by scanning logs. Now recorded at ingest (§5).
- **Does the chunk reader tolerate columns added later?** Still unverified. Only
  matters for §4, which is blocked anyway.

## Non-goals

Named explicitly so they stay decided:

- **No web dashboard.** The moment there are charts we compete with Grafana on
  its turf and the one differentiator is gone. (Possible exception: a public
  status page — one static file from health-check data, and it is a sales asset.)
- **No OTel ingestion, StatsD, or browser/session-replay SDK.** Each doubles the
  maintenance surface to chase a user with two services and no ops team.
- **No on-call rotations or escalation policies.** For five people, Telegram is
  the escalation policy.
- **No RBAC beyond admin/member. No clustering, HA, or multi-region.** The $4 VM
  is the pitch.
- **No PR-opening, no repo clones.** §7.

## One more thing

90+ actions is already heavy for an agent's context budget. The audit log knows
which ones are actually called. Deleting the dead ones will likely improve agent
behaviour more than anything on this list.
