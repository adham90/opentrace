# VM Metrics Agent

OpenTrace includes a lightweight agent that runs on your servers, collects system metrics, and pushes them to the dashboard. It's the same binary — just a different subcommand.

## Quick Start

```bash
OPENTRACE_SERVER_URL=http://your-opentrace-host:8080 \
OPENTRACE_API_KEY=your-api-key \
opentrace agent
```

The agent auto-registers with the server on first run, then pushes metrics at a configurable interval.

## Collected Metrics

| Category | Metrics |
|---|---|
| **CPU** | `cpu.usage_percent`, `cpu.user_percent`, `cpu.system_percent`, `cpu.idle_percent` |
| **Memory** | `memory.total_bytes`, `memory.used_bytes`, `memory.available_bytes`, `memory.usage_percent` |
| **Swap** | `swap.total_bytes`, `swap.used_bytes`, `swap.usage_percent` |
| **Disk** | `disk.total_bytes`, `disk.used_bytes`, `disk.usage_percent` (per mount) |
| **Network** | `network.bytes_sent_per_sec`, `network.bytes_recv_per_sec` (per interface) |
| **Load** | `load.1m`, `load.5m`, `load.15m` |
| **System** | `process.count`, `uptime.seconds` |

Metrics are collected via [gopsutil](https://github.com/shirou/gopsutil).

## Configuration

| Variable | Default | Description |
|---|---|---|
| `OPENTRACE_SERVER_URL` | _(required)_ | URL of your OpenTrace server |
| `OPENTRACE_API_KEY` | _(empty)_ | API key for authentication |
| `OPENTRACE_AGENT_INTERVAL` | `30s` | Metrics collection interval |

## Install Script

OpenTrace provides a one-line install script for setting up the agent on remote servers:

```bash
curl -sSL http://your-opentrace-host:8080/api/agent/install.sh | bash
```

## Running as a Systemd Service

Create `/etc/systemd/system/opentrace-agent.service`:

```ini
[Unit]
Description=OpenTrace Agent
After=network.target

[Service]
Type=simple
Environment=OPENTRACE_SERVER_URL=http://your-opentrace-host:8080
Environment=OPENTRACE_API_KEY=your-api-key
ExecStart=/usr/local/bin/opentrace agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now opentrace-agent
```

## Querying Metrics via MCP

Once the agent is pushing data, these MCP tools become available:

- `list_servers` — List all monitored servers with online/offline status
- `query_metrics` — Query time-series metrics for a server (e.g., CPU over the last hour)
- `server_health` — Current health snapshot for a server
