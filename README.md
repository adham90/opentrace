<p align="center">
  <strong>OpenTrace</strong><br/>
  <em>Give your AI coding agent eyes into production</em>
</p>

<p align="center">
  <a href="https://github.com/adham90/opentrace/releases"><img src="https://img.shields.io/github/v/release/adham90/opentrace?style=flat-square" alt="Release"></a>
  <a href="https://github.com/adham90/opentrace/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/adham90/opentrace/ci.yml?branch=main&style=flat-square&label=CI" alt="CI"></a>
  <a href="https://github.com/adham90/opentrace/blob/main/LICENSE"><img src="https://img.shields.io/github/license/adham90/opentrace?style=flat-square" alt="License"></a>
</p>

<p align="center">
  <a href="#how-it-works">How It Works</a> ·
  <a href="#get-started">Get Started</a> ·
  <a href="#what-can-the-agent-do">Features</a> ·
  <a href="#mcp-tools">MCP Tools</a> ·
  <a href="#security">Security</a>
</p>

---

Your AI coding assistant can read your code — but it's **blind to production**. When something breaks, you copy-paste stack traces, dig through log dashboards, and manually feed context into chat.

OpenTrace fixes that. It's a self-hosted server that connects your AI agent directly to your production observability data via [MCP](https://modelcontextprotocol.io). No dashboards, no UI — **the agent is the interface**.

```
You:    "Why is the checkout endpoint throwing 500s?"

Agent:  Error rate on POST /api/checkout spiked from 0.1% to 4.2%
        starting 8 minutes ago. 23 errors, 12 affected users.

        Root cause: NilPointerError at payments_controller.rb:87
        — customer.address is nil when charging.

        This started after the latest deploy (commit a1b2c3d by
        dev@company.com). The commit modified payments_controller.rb
        lines 82-95.

        Want me to fix it?
```

---

## How It Works

```
Your App                                Your Server
┌──────────────────────┐               ┌──────────────────────────────────┐
│                      │               │         OpenTrace Server          │
│  SDK (Ruby / Node)   │── POST ──────>│                                  │
│  Thin, async, non-   │  /api/v2/logs │  Ingest Pipeline                 │
│  blocking. Just      │  flat JSON    │    PII scrub → fingerprint →     │
│  serialize & send.   │               │    expand in-request logs         │
│                      │               │                 │                 │
└──────────────────────┘               │                 ▼                 │
                                       │  Segmented Log Store             │
Your Laptop                            │    Binary WAL → hourly seal →    │
┌──────────────────────┐               │    columnar chunks + FTS index   │
│                      │               │    45 columns, 6 encoding types  │
│  Claude Code / Cursor│◄── MCP ──────│    ~260KB runtime memory          │
│                      │  over HTTPS   │                                  │
│  Reads .mcp.json     │               │  SQLite (platform data)          │
│  Auto-connects       │               │    Users, watches, error groups  │
│                      │               │                                  │
└──────────────────────┘               │  Connects to your Postgres       │
                                       │    (read-only)                   │
                                       └──────────────────────────────────┘
```

**The SDK** captures logs, request performance, SQL queries, external API calls, emails, file operations, and audit trails — then sends everything as flat JSON. Your app never blocks or crashes due to OpenTrace.

**The server** ingests logs into a custom columnar storage engine (no Elasticsearch, no ClickHouse — just files on disk), monitors health checks, tracks errors, and runs alert rules. Runs on a **$4/month VM**.

**The agent** queries all of this through MCP tools — searching logs, investigating errors, explaining slow queries, assessing deploy risk — without you copy-pasting anything.

---

## Storage Engine

OpenTrace uses a custom **segmented columnar log store** instead of SQLite or Elasticsearch for log data:

- **Write path**: SDK sends flat JSON → server appends to binary WAL → fsync. No indexes on write. **200-500K entries/sec**.
- **Seal**: Every hour, the WAL is sealed into compressed columnar chunks (45 columns, 6 encoding types: dictionary, sparse, delta, bitpack, varint, zstd). **3-5MB peak memory**.
- **Query**: Parallel column scans across segments + custom inverted index for full-text search. Most queries complete in **5-50ms**.
- **Pruning**: `rm -rf` old segment directories. **Instant** — no DELETE queries, no VACUUM.
- **Storage**: ~76MB/hour at 1M logs/hr (vs ~500GB with SQLite). **Fits on a $4 VM**.

```
data/logs/
  2026-04-04T10/
    chunk_000.col    3MB     ← 45 compressed columns
    chunk_000.idx    1MB     ← inverted index for FTS
    meta.json        2KB     ← pre-computed histograms
  2026-04-04T11/
    ...
  2026-04-04T12/
    active.wal       12MB    ← current hour, accumulating
```

Every entry captured by the SDK flows through the full pipeline — PII scrubbing, error fingerprinting, in-request log expansion — and lands in the store as a single row with 45 searchable columns plus an opaque body blob for deep details (SQL queries, stack traces, timeline, etc.).

---

## Get Started

### 1. Deploy the server

Pick one:

<details>
<summary><strong>VPS (Hetzner, DigitalOcean, any Linux server)</strong></summary>

```bash
ssh root@your-server
curl -fsSL https://raw.githubusercontent.com/adham90/opentrace/main/scripts/install.sh | bash
```

The installer:
- Downloads the latest binary
- Initializes the database
- Sets up a systemd service
- Optionally installs [Caddy](https://caddyserver.com) for automatic HTTPS
- Prints the connect command when done

</details>

<details>
<summary><strong>Docker</strong></summary>

```bash
docker run -d --name opentrace \
  -p 8080:8080 \
  -v opentrace-data:/data \
  -e OPENTRACE_LISTEN_ADDR=0.0.0.0:8080 \
  ghcr.io/adham90/opentrace:latest
```

</details>

<details>
<summary><strong>Docker Compose</strong></summary>

```bash
docker compose -f docker-compose.prod.yml up -d
```

</details>

<details>
<summary><strong>One-click platforms</strong></summary>

| Platform | |
|---|---|
| **Railway** | [![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/new/template?template=https://github.com/adham90/opentrace) |
| **Render** | [![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/adham90/opentrace) |
| **DigitalOcean** | [![Deploy to DO](https://www.deploytodo.com/do-btn-blue.svg)](https://cloud.digitalocean.com/apps/new?repo=https://github.com/adham90/opentrace/tree/main) |

</details>

### 2. Connect your project

In your project directory, run the connect command the installer printed:

```bash
curl -s https://your-server.com/connect | bash
```

**No client install needed.** Just curl and bash. The script creates `.mcp.json` in your project — Claude Code reads this file and connects to OpenTrace automatically.

### 3. Set up the SDK

Open Claude Code and ask:

> "Set up opentrace for my project"

The agent detects your framework, installs the SDK, configures it with the correct API key, and verifies logs are flowing.

| SDK | Platform | Install |
|---|---|---|
| [opentrace](https://github.com/adham90/opentrace-ruby) | Ruby / Rails | `gem 'opentrace'` |
| [@opentrace-sdk/node](https://github.com/adham90/opentrace_node) | Node.js | `npm install @opentrace-sdk/node` |

The SDK captures structured logs, request lifecycle data (SQL queries, external API calls, cache metrics, view rendering, email delivery), error traces with stack traces, and runtime metrics — all sent as flat JSON with async I/O. Your app never blocks.

**Any other stack — no adapter needed.** The SDKs are convenience, not a
requirement: ingestion is one HTTP endpoint taking flat JSON, and the full
contract is published by your own server at `GET /spec`. Ask your agent:

> "Read https://your-server.com/spec and write an OpenTrace client for this app"

It writes ~100 lines against the spec, then checks its work in a loop against
the server's dry-run endpoint:

```bash
curl -X POST 'https://your-server.com/api/v2/logs?validate=1' \
  -H 'Authorization: Bearer <key>' -d '{"level":"info","msg":"hello"}'
```

```json
{"valid": false, "results": [{
  "errors": ["message: required and must be non-empty"],
  "warnings": ["service: not sent — every query groups by service"],
  "unknown_fields": [{"field": "msg", "did_you_mean": "message"}]
}]}
```

Nothing is stored, every problem in the payload comes back at once, and unknown
fields get a suggested correction — so a client for a stack nobody has written
an SDK for is a loop, not a guess. Drop the parameter when it reports
`"valid": true`.

### 4. Ask your agent anything

You're done. Start asking:

| Question | What happens |
|---|---|
| *"What errors are happening in production?"* | Agent searches error groups, shows impact and stack traces |
| *"Why is the payments endpoint slow?"* | Agent checks request performance — duration, SQL count, external API time, N+1 detection |
| *"Show me logs from the last hour with level ERROR"* | Agent searches logs with columnar filters |
| *"Is it safe to deploy this change?"* | Agent checks blast radius, code risk scores, recent errors |
| *"Generate tests for the most common production errors"* | Agent creates regression tests from real error data |
| *"Set up a watcher for checkout error rate > 1%"* | Agent creates a threshold alert |
| *"What happened after the last deploy?"* | Agent checks deploy impact, error rate changes |

---

## What Can the Agent Do?

### Search & Debug Logs
Full-text search across all services via custom inverted index. Filter by level, service, trace ID, time range, handler, status code, error class. Assemble distributed traces. Compare error rates between time periods.

### Deep Request Capture
Every HTTP request captured by the middleware includes: SQL queries with durations and EXPLAIN plans, external API calls, cache hits/misses, view rendering times, email deliveries, file operations, audit trail, and a waterfall timeline — all in one log entry.

### Investigate Errors
Errors are automatically grouped by fingerprint (hash of error class + source file + line). The agent sees occurrence counts, affected users, impact scores, and full stack traces. It can resolve or ignore error groups.

### Query Your Database
Connect your Postgres databases (read-only). The agent runs `EXPLAIN ANALYZE` on slow queries, checks index health, detects lock contention, and identifies N+1 query patterns. All queries are validated SELECT-only via SQL AST parsing.

### Monitor Uptime
Create HTTP health checks that run on a schedule. The agent sees uptime percentages, response times, and gets notified when endpoints go down.

### Set Up Alerts
Create threshold watches on error rate, response time (mean or p95), log volume, error count, SQL count, cache hit rate, or service heartbeat. The agent can create watches for code it just deployed — self-monitoring its own changes.

Watch and health-check alerts can be delivered to **Slack** or **Telegram** — see [`docs/notifications.md`](docs/notifications.md) for setup.

### Assess Code Risk
Every file and endpoint gets a risk score based on error frequency, investigation history, and change velocity. Before modifying a file, the agent checks its production behavior.

### Track Deploys
The SDK sends the git commit hash with every log. OpenTrace records a deploy the first time it sees a new commit for a service and environment, so `since: "last_deploy"` works as a time window on every tool that takes one — no wall-clock guessing.

### Catch Up After Being Away
`overview(action: "catchup")` returns every new error, alert, and deploy since **your** last catch-up, then advances your cursor. The cursor is per person, so on a small team one member draining the queue does not hide the incident from everyone else. `peek: true` reads without advancing.

### Answer "Customer X Says It's Broken"
`users(action: "timeline", user_id: "...")` returns everything one person hit — requests, errors, timings. `tenant_id` does the same for a B2B account, and `impact` runs it the other way: who is this error actually hurting.

### On-Call While You Sleep
When an alert fires, OpenTrace can run **your own agent CLI** against it and send you a diagnosis instead of a threshold. It shells out to `claude -p` (or `codex exec`, or anything reading stdin), so OpenTrace never holds a model credential and is not tied to one vendor — and on a Claude Pro/Max subscription it costs nothing extra.

Optionally it files each diagnosis as a GitHub **issue**, one per error fingerprint per environment, reopening and commenting on recurrence rather than filing duplicates. Issues, never pull requests: the observability box has your production data, not your code.

Off by default — turning it on sends log excerpts to a model provider. See [`.env.example`](.env.example).

### Notice When OpenTrace Itself Dies
Set `OPENTRACE_HEARTBEAT_URL` and the server pings it every minute. If the box dies, the pings stop and your external monitor tells you. Without it, a dead OpenTrace looks exactly like an OpenTrace with nothing to report.

---

## MCP Tools

OpenTrace exposes 14 tools with 90+ actions via MCP. Each tool returns `suggested_tools` with pre-filled arguments so the agent knows what to call next.

| Tool | Actions | What it does |
|---|---|---|
| **logs** | search, context, attributes, stats, summary, performance, trace, compare | Full-text log search, distributed trace assembly, N+1 detection |
| **errors** | list, detail, investigate, impact, user_errors, ranking, resolve, ignore, reopen, new | Error grouping by fingerprint, user impact scoring, stack traces |
| **database** | queries, explain, tables, activity, locks, connections, indexes, schema, storage, kill_query, long_transactions | Postgres introspection, EXPLAIN plans, lock and connection analysis |
| **watches** | status, create, delete, alerts, dismiss, acknowledge, investigate | Threshold alerts on error rate, latency, request volume |
| **overview** | status, triage, catchup, diagnose, timeline, investigate, changes, settings, notes, delete_note | System health, what you missed since last visit, incident timeline, settings, agent memory |
| **users** | timeline, errors, impact | What one customer hit, which errors they saw, who an error affects |
| **analytics** | traffic, endpoints, heatmap, trends, movers | Traffic patterns, endpoint performance, time-series analysis |
| **code** | risk, fragile, annotate_file, annotate_function, hotspots, gen_context, gen_suggest, deps_service, deps_blast, deps_risk | Code risk scores, test generation, blast radius, production annotations |
| **deep_capture** | request_capture, sql_captures, http_captures, email_captures, audit_trail, search_audit, search_sql, file_captures, get_pii_config, update_pii_config, get_retention, update_retention | Per-request deep capture: SQL, HTTP, emails, audit trail, file ops, PII config |
| **healthchecks** | list, uptime, create, delete | HTTP endpoint monitoring with uptime tracking |
| **servers** | list, query, health | Server and process metrics (CPU, memory, GC) |
| **connectors** | list, get, create, test, update, delete | Manage database connectors (Postgres, MySQL, etc.) |
| **setup** | status, detect, guide, verify | SDK setup assistant — detects framework, provides config with API key |
| **admin** | update_retention, users, update_role, toggle_active, delete_user, audit | User management, retention, audit log (admin only) |

---

## Security

| Protection | How |
|---|---|
| **No self-registration** | First `curl .../connect` creates admin. Everyone else needs an invite. |
| **Per-user tokens** | Each developer gets a personal MCP token, stored in their local `.mcp.json`. Revocable independently. |
| **HTTPS via Caddy** | The install script sets up [Caddy](https://caddyserver.com) with automatic Let's Encrypt certificates. |
| **PII scrubbing** | Credit cards, emails, phone numbers, SSNs, and configurable sensitive fields are scrubbed from request bodies before storage. |
| **Rate limiting** | Auth endpoints are rate-limited — 10 attempts per minute per IP. |
| **Read-only DB access** | All queries against your Postgres are validated SELECT-only via SQL AST parsing. |
| **API key auth** | SDK log ingestion requires a Bearer token. |
| **No telemetry** | Fully self-hosted. No external calls. No tracking. Your data stays on your server. The on-call agent is the one exception, and it is off by default. |
| **Prompt-injection boundary** | Alert data reaches the on-call agent on stdin, below an explicit untrusted-data marker, and the agent is pointed at read-only tools. Error messages come from your users. |

---

## Configuration

Server-side environment variables (`.env` file):

| Variable | Default | Description |
|---|---|---|
| `OPENTRACE_LISTEN_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `OPENTRACE_DATA_DIR` | `~/.opentrace` | Data directory (SQLite + log segments) |
| `OPENTRACE_API_KEY` | _(auto-generated)_ | Bearer token for SDK log ingestion |
| `OPENTRACE_MAX_QUERY_ROWS` | `500` | Max rows returned from SQL queries |
| `OPENTRACE_STATEMENT_TIMEOUT_MS` | `5000` | SQL query timeout in milliseconds |
| `OPENTRACE_TRUSTED_PROXIES` | _(empty)_ | Comma-separated proxy IPs for rate limiting |
| `OPENTRACE_ALERT_WEBHOOK_URL` | _(empty)_ | Generic JSON webhook for watch + health-check alerts |
| `OPENTRACE_HEARTBEAT_URL` | _(empty)_ | Liveness ping sent every 60s, for an external monitor to alarm on |
| `OPENTRACE_ONCALL_ENABLED` | `false` | Run your own agent CLI against alerts and deliver a diagnosis |
| `OPENTRACE_ONCALL_CMD` | `claude -p --permission-mode dontAsk` | The agent command; prompt on stdin, diagnosis on stdout |
| `OPENTRACE_ONCALL_GITHUB_REPO` | _(empty)_ | `owner/name` — file each diagnosis as a deduped GitHub issue |

See [`.env.example`](.env.example) for all options.

Alert delivery to Slack and Telegram is configured at runtime, not via environment variables — see [`docs/notifications.md`](docs/notifications.md).

---

## How It's Built

- **Go** — single binary, no runtime dependencies, cross-compiled for Linux and macOS
- **Custom columnar storage** — 45-column format with 6 encoding types (dictionary, sparse, delta, bitpack, varint, zstd). Binary WAL for writes, hourly seal into compressed chunks, custom inverted index for FTS.
- **SQLite** — for platform data (users, watches, error groups, health checks). Not used for log storage.
- **MCP** — native Model Context Protocol with Streamable HTTP and SSE transports
- **Pure Go** — no CGO, no system dependencies, `go build` and ship

---

## Development

```bash
git clone https://github.com/adham90/opentrace.git && cd opentrace
cp .env.example .env
go build -o opentrace ./cmd/opentrace
./opentrace serve
```

```bash
go test -short -race ./...    # unit tests (44 packages)
go vet ./...                  # linting
```

---

## License

[MIT](LICENSE) — use it however you want.
