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
Your Server                              Your Laptop
┌──────────────────────┐                ┌──────────────────────┐
│                      │                │                      │
│  OpenTrace Server    │◄─── MCP ─────│  Claude Code / Cursor │
│                      │   over HTTPS  │                      │
│  Single Go binary    │                │  Reads .mcp.json     │
│  SQLite database     │                │  Auto-connects       │
│                      │                │                      │
└──────┬───────┬───────┘                └──────────────────────┘
       │       │
       │       │
       │       └──── Connects to your Postgres (read-only)
       │
       └──── Receives logs from your app via SDK
```

**The server** ingests logs from your app, connects to your databases, monitors health checks, tracks errors, and runs alert rules.

**The agent** queries all of this through MCP tools — searching logs, investigating errors, explaining slow queries, assessing deploy risk — without you copy-pasting anything.

**The developer** never opens a dashboard. They ask questions in natural language and the agent has the answers.

---

## Get Started

### 1. Deploy the server

Pick one:

<details>
<summary><strong>VPS (Hetzner, DigitalOcean, any Linux server)</strong></summary>

```bash
ssh root@your-server
curl -fsSL https://get.opentrace.dev | bash
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

```
  OpenTrace — connect your project
  Server: https://your-server.com

  Checking server... ok

  No accounts exist yet. Set up your admin account.

  Email: you@company.com
  Password: ********
  Confirm:  ********

  Authenticating... admin account created
  ✓ .mcp.json created
  ✓ .mcp.json added to .gitignore

  Done. Open Claude Code in this project — OpenTrace is connected.
```

**No client install needed.** Just curl and bash. The script creates `.mcp.json` in your project — Claude Code reads this file and connects to OpenTrace automatically.

### 3. Set up the SDK

Open Claude Code and ask:

> "Set up opentrace for my project"

The agent detects your framework, installs the SDK, configures it with the correct API key, and verifies logs are flowing.

| SDK | Platform | Install |
|---|---|---|
| [opentrace](https://github.com/adham90/opentrace_ruby) | Ruby / Rails | `gem 'opentrace'` |
| [@opentrace-sdk/node](https://github.com/adham90/opentrace_node) | Node.js | `npm install @opentrace-sdk/node` |

The SDK sends structured logs, error traces, request performance data, and runtime metrics (memory, GC, threads) to OpenTrace automatically. Your app never blocks or crashes due to OpenTrace — all I/O is async with bounded queues.

### 4. Ask your agent anything

You're done. Start asking:

| Question | What happens |
|---|---|
| *"What errors are happening in production?"* | Agent searches error groups, shows impact and stack traces |
| *"Why is the payments endpoint slow?"* | Agent checks request performance, SQL stats, external API times |
| *"Show me logs from the last hour with level ERROR"* | Agent searches logs with filters |
| *"Is it safe to deploy this change?"* | Agent checks blast radius, code risk scores, recent errors |
| *"Generate tests for the most common production errors"* | Agent creates regression tests from real error data |
| *"Set up a watcher for checkout error rate > 1%"* | Agent creates a threshold alert |
| *"What happened after the last deploy?"* | Agent checks deploy impact, error rate changes |
| *"Invite dev@company.com to opentrace"* | Agent creates a user account |

---

## What Can the Agent Do?

### Search & Debug Logs
Full-text search across all services. Filter by level, service, trace ID, time range. Assemble distributed traces. Compare error rates between time periods.

### Investigate Errors
Errors are automatically grouped by fingerprint. The agent sees occurrence counts, affected users, impact scores, and full stack traces. It can resolve or ignore error groups.

### Query Your Database
Connect your Postgres databases (read-only). The agent runs `EXPLAIN ANALYZE` on slow queries, checks index health, detects lock contention, and identifies N+1 query patterns. All queries are validated SELECT-only via SQL AST parsing.

### Monitor Uptime
Create HTTP health checks that run on a schedule. The agent sees uptime percentages, response times, and gets notified when endpoints go down.

### Set Up Alerts
Create threshold watches on error rate, response time, request volume, SQL count, or cache hit rate. The agent can create watches for code it just deployed — self-monitoring its own changes.

### Assess Code Risk
Every file and endpoint gets a risk score based on error frequency, investigation history, and change velocity. Before modifying a file, the agent checks its production behavior — call volume, error rate, latency percentiles.

### Generate Tests from Real Errors
The agent creates regression tests using actual production error data — real inputs, real stack traces, real edge cases. Every test has a story: when the error happened, how many users it affected.

### Track Deploys
The SDK sends the git commit hash with every log. OpenTrace detects deploys automatically when the commit hash changes. The agent correlates errors to specific commits.

### Manage the Team
Invite users, revoke access, rotate API keys, view audit logs — all through conversation. No admin panel needed.

---

## Adding Team Members

You:
> "Invite dev@company.com to opentrace"

The agent creates the account and gives you a temporary password. Send it to the developer securely.

The developer runs:
```bash
curl -s https://your-server.com/connect | bash
```

Enters their email and temporary password. They're connected. Each developer gets their own `.mcp.json` with a personal token.

To remove someone:
> "Remove dev@company.com from opentrace"

Their tokens are invalidated immediately across all projects.

---

## MCP Tools

OpenTrace exposes 12 tools with 80+ actions via MCP. Each tool returns `suggested_tools` with pre-filled arguments so the agent knows what to call next.

| Tool | Actions | What it does |
|---|---|---|
| **logs** | search, context, stats, summary, performance, trace, compare | Full-text log search, distributed trace assembly, N+1 detection |
| **errors** | list, detail, investigate, impact, ranking, resolve, ignore | Error grouping by fingerprint, user impact scoring, stack traces |
| **database** | queries, explain, tables, activity, locks, indexes, schema, runbook | Postgres introspection, EXPLAIN plans, composite investigation runbooks |
| **watches** | status, create, delete, alerts, dismiss, investigate | Threshold alerts on error rate, latency, request volume |
| **overview** | status, triage, diagnose, timeline, investigate, changes, settings, notes, session_summary | System health, alerts, incident timeline, settings, agent memory |
| **analytics** | traffic, endpoints, heatmap, trends, movers | Traffic patterns, endpoint performance, time-series analysis |
| **code** | risk, fragile, test_gaps, annotate_file, gen_context, deps_risk | Code risk scores, test generation, blast radius, production annotations |
| **deploys** | history, impact, record | Deploy tracking, error rate impact measurement |
| **healthchecks** | list, uptime, create, delete | HTTP endpoint monitoring with uptime tracking |
| **servers** | list, query, health | Server and process metrics (CPU, memory, GC) |
| **admin** | update_retention, users, audit | User management, retention, audit log (admin only) |
| **setup** | status, detect, guide, verify | SDK setup assistant — detects framework, provides config with API key |

---

## Security

| Protection | How |
|---|---|
| **No self-registration** | First `curl .../connect` creates admin. Everyone else needs an invite. |
| **Per-user tokens** | Each developer gets a personal MCP token, stored in their local `.mcp.json`. Revocable independently. |
| **HTTPS via Caddy** | The install script sets up [Caddy](https://caddyserver.com) with automatic Let's Encrypt certificates. OpenTrace listens on localhost only. |
| **Rate limiting** | Auth endpoints are rate-limited — 10 attempts per minute per IP, then blocked. |
| **Read-only DB access** | All queries against your Postgres are validated SELECT-only via SQL AST parsing, with configurable timeouts and row limits. |
| **API key auth** | SDK log ingestion requires a Bearer token. |
| **No telemetry** | Fully self-hosted. No external calls. No tracking. Your data stays on your server. |

---

## Configuration

Server-side environment variables (`.env` file):

| Variable | Default | Description |
|---|---|---|
| `OPENTRACE_LISTEN_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `OPENTRACE_DATA_DIR` | `~/.opentrace` | SQLite database directory |
| `OPENTRACE_API_KEY` | _(auto-generated)_ | Bearer token for SDK log ingestion |
| `OPENTRACE_MAX_QUERY_ROWS` | `500` | Max rows returned from SQL queries |
| `OPENTRACE_STATEMENT_TIMEOUT_MS` | `5000` | SQL query timeout in milliseconds |
| `OPENTRACE_TRUSTED_PROXIES` | _(empty)_ | Comma-separated proxy IPs for rate limiting |
| `OPENTRACE_CORS_ORIGINS` | _(empty)_ | Allowed origins for browser requests |

See [`.env.example`](.env.example) for all options.

---

## Server Commands

Run on the server only:

```
opentrace init      Initialize the database (first-time setup)
opentrace serve     Start the server
opentrace mcp       Start MCP stdio server (for local development)
opentrace seed      Populate sample data (development only)
opentrace backup    Create a SQLite database backup
opentrace restore   Restore from a backup file
```

No client-side install. Connect with `curl`, manage everything through your AI assistant.

---

## How It's Built

- **Go** — single binary, no runtime dependencies, cross-compiled for Linux and macOS
- **SQLite** — zero-dependency database with WAL mode and FTS5 for full-text log search
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
go test -short -race ./...    # unit tests
go vet ./...                  # linting
```

---

## License

[MIT](LICENSE) — use it however you want.
