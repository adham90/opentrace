# Deployment Guide

## Docker (Recommended)

```bash
docker run -d --name opentrace \
  -p 8080:8080 \
  -v opentrace-data:/data \
  ghcr.io/adham90/opentrace:latest
```

## Docker Compose

### Development (builds from source)

```bash
docker compose up -d
```

Starts OpenTrace on `localhost:8080` and PostgreSQL on `localhost:5432`.

### Production (pre-built image + auto-updates)

```bash
docker compose -f docker-compose.prod.yml up -d
```

Pulls from `ghcr.io/adham90/opentrace:latest` and includes [Watchtower](https://containrrr.dev/watchtower/) for automatic hourly image updates.

## One-Click Deploy

| Platform | Deploy | Notes |
|---|---|---|
| **DigitalOcean** | [![Deploy to DO](https://www.deploytodo.com/do-btn-blue.svg)](https://cloud.digitalocean.com/apps/new?repo=https://github.com/adham90/opentrace/tree/main) | App Platform, ~$5/mo |
| **Railway** | [![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/new/template?template=https://github.com/adham90/opentrace) | Free tier available |
| **Render** | [![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/adham90/opentrace) | Free tier, persistent disk |
| **Hetzner** | `./deploy/deploy.sh` | Full VPS with Caddy + automated backups, ~$4/mo |

## Fly.io

```bash
fly launch --copy-config --no-deploy
fly secrets set OPENTRACE_API_KEY=your-api-key
fly deploy
```

## Binary

Download from [GitHub Releases](https://github.com/adham90/opentrace/releases):

```bash
# Linux amd64
curl -L https://github.com/adham90/opentrace/releases/latest/download/opentrace_linux_amd64.tar.gz | tar xz
cp .env.example .env  # edit with your config
./opentrace
```

Binaries are available for Linux, macOS, and Windows (amd64 + arm64).

## Systemd Service

Create `/etc/systemd/system/opentrace.service`:

```ini
[Unit]
Description=OpenTrace
After=network.target

[Service]
Type=simple
User=opentrace
WorkingDirectory=/opt/opentrace
ExecStart=/opt/opentrace/opentrace
EnvironmentFile=/opt/opentrace/.env
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now opentrace
```

## Configuration

All configuration is via environment variables. Copy `.env.example` to `.env` and adjust:

| Variable | Default | Description |
|---|---|---|
| `OPENTRACE_DATA_DIR` | `~/.opentrace` | Directory for SQLite database |
| `OPENTRACE_LISTEN_ADDR` | `:8080` | HTTP server listen address |
| `OPENTRACE_API_KEY` | _(empty)_ | Bearer token for log ingestion API |
| `OPENTRACE_MAX_QUERY_ROWS` | `500` | Max rows returned from SQL queries |
| `OPENTRACE_STATEMENT_TIMEOUT_MS` | `5000` | SQL query timeout in milliseconds |
| `OPENTRACE_METRIC_RETENTION_DAYS` | `7` | Days to retain server metrics before pruning |
| `OPENTRACE_DEV` | `false` | Enable live template reloading |
| `OPENTRACE_TRUSTED_PROXIES` | _(empty)_ | Comma-separated list of trusted proxy IPs |
| `OPENTRACE_TLS_CERT` | _(empty)_ | TLS certificate path (enables HTTPS) |
| `OPENTRACE_TLS_KEY` | _(empty)_ | TLS key path |
| `OPENTRACE_CORS_ORIGINS` | _(empty)_ | CORS allowed origins (comma-separated) |
| `OPENTRACE_MCP_TOKEN` | _(empty)_ | Bearer token for MCP SSE transport |
| `OPENTRACE_MCP_NAME` | _(empty)_ | Custom MCP server name |

## Data Storage

OpenTrace stores all data in a single SQLite database at `~/.opentrace/opentrace.db` (configurable via `OPENTRACE_DATA_DIR`).

- **WAL mode** for concurrent read access
- **FTS5** for full-text log search
- **Single file** — back up this one file to preserve everything

## Updates

OpenTrace checks for new versions automatically. When an update is available, a banner appears at the top of the dashboard with a link to the release notes.

- **Auto-updates (Docker):** The production Docker Compose includes Watchtower — checks hourly and restarts automatically.
- **Manual update (Docker):** `docker compose pull && docker compose up -d`
- **Manual update (binary):** Download the new release and restart the service.

## Architecture

```
                         ┌────────────────────────────┐
  ┌──────────┐           │       Web UI (HTMX)        │         ┌────────────┐
  │ VM Agent │──metrics─▶│  Alerts │ Logs │ Watches    │◀─browse─│  You / CI  │
  └──────────┘           └────────────┬───────────────┘         └────────────┘
                                      │
  ┌──────────┐           ┌────────────▼───────────────┐         ┌────────────┐
  │ Your App │──logs────▶│     HTTP Server (Chi)       │◀─stdio─│Claude/MCP  │
  └──────────┘           │    REST API + MCP Server    │        │  Client    │
                         └────────────┬───────────────┘         └────────────┘
                                      │
             ┌────────────────────────┼────────────────────────┐
             │                        │                        │
   ┌─────────▼──────────┐  ┌─────────▼────────┐    ┌──────────▼─────────┐
   │  Connector Layer    │  │  Watch Engine     │    │  Postgres          │
   │  Logs + DB + Metrics│  │  Threshold Alerts │    │  (your databases)  │
   └─────────┬──────────┘  └─────────┬────────┘    └────────────────────┘
             │                        │
             └────────────┬───────────┘
                          │
           ┌──────────────▼───────────────────┐
           │       SQLite (WAL + FTS5)         │
           │  Logs │ Errors │ Watches │ Alerts │
           │  Metrics │ Sessions │ Journeys    │
           └──────────────────────────────────┘
```

### Key Design Decisions

- **SQLite** — Zero-dependency, single-file database with FTS5 for log search. No external database server required.
- **MCP-first** — Every feature is exposed as an MCP tool with guided suggestions. Not just a REST wrapper.
- **Read-only DB access** — All queries against your Postgres databases are validated SELECT-only via SQL AST parsing, with configurable timeouts and row limits.
- **Single binary** — Web server, MCP server, and VM agent in one binary. No runtime dependencies.
