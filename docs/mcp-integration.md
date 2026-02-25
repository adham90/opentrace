# MCP Server Integration

OpenTrace includes a built-in [Model Context Protocol (MCP)](https://modelcontextprotocol.io) server. This lets AI tools like **Claude Code**, **Claude Desktop**, **Cursor**, **Windsurf**, and other MCP-compatible clients directly query your logs, databases, errors, and health checks — all through natural language.

## Transport Modes

OpenTrace supports two MCP transports:

- **stdio** — For local CLI tools (Claude Code, Cursor). Run `opentrace mcp`.
- **SSE** — For remote access via HTTP. Available at `/mcp/sse` and `/mcp/message` endpoints with Bearer token auth.

## Setup

### Claude Code

Add to your project's `.mcp.json`:

```json
{
  "mcpServers": {
    "opentrace": {
      "type": "stdio",
      "command": "opentrace",
      "args": ["mcp"]
    }
  }
}
```

Or if using a launcher script with environment variables:

```bash
#!/bin/bash
cd /path/to/opentrace
set -a && source .env && set +a
exec ./opentrace mcp
```

```json
{
  "mcpServers": {
    "opentrace": {
      "type": "stdio",
      "command": "/path/to/opentrace/run-mcp.sh"
    }
  }
}
```

### Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS):

```json
{
  "mcpServers": {
    "opentrace": {
      "command": "opentrace",
      "args": ["mcp"]
    }
  }
}
```

Restart Claude Desktop and OpenTrace tools will appear in the tools menu.

### Cursor

In Cursor settings, add an MCP server:

- **Name:** `opentrace`
- **Type:** `stdio`
- **Command:** `opentrace`
- **Args:** `mcp`

### Windsurf

Add to your Windsurf MCP configuration:

```json
{
  "mcpServers": {
    "opentrace": {
      "command": "opentrace",
      "args": ["mcp"]
    }
  }
}
```

### Any MCP Client

OpenTrace's MCP server communicates via **stdio** using JSON-RPC:

```bash
./opentrace mcp
```

Reads from stdin, writes to stdout. All application logs go to stderr to keep the JSON-RPC channel clean.

## Guided Workflows

The MCP server actively guides AI assistants through optimal tool chains:

1. **Handshake instructions** — During the `initialize` call, the server sends instructions explaining which tools to start with based on common intents.

2. **Suggested tools** — Most tool responses include a `suggested_tools` array with pre-filled arguments for the logical next step. For example:
   - `diagnose` suggests `error_detail` with the top error's fingerprint pre-filled
   - `db_query_stats` suggests `explain_query` with the slowest query pre-filled
   - `error_detail` suggests `log_search` with the exception class pre-filled

3. **Entry points** — Which tool to call first depends on what you need:

| Intent | Start with |
|---|---|
| "What's wrong?" | `diagnose` |
| "System health" | `system_overview` |
| "What needs attention?" | `triage_alerts` |
| "Slow queries?" | `db_query_stats` |
| Full investigation | `runbook` |

## Tool Categories

### Overview & Triage
`diagnose`, `system_overview`, `triage_alerts`, `runbook`

### Log Intelligence
`log_search`, `log_context`, `log_stats`, `log_summary`, `list_log_attributes`, `trace_lookup`, `request_performance`, `compare_periods`

### Database Introspection (requires Postgres connector)
`db_query_stats`, `db_table_stats`, `db_activity`, `db_locks`, `db_index_analysis`, `explain_query`, `schema_overview`, `vacuum_report`, `disk_usage`, `pg_config_check`, `checkpoint_stats`, `sequence_health`, `bloat_estimate`, `long_transactions`, `replication_status`, `connection_pool_stats`, `kill_query`, `db_search`

### Errors
`error_groups`, `error_detail`, `investigate_error`, `resolve_error`, `ignore_error`, `error_impact`, `top_errors_by_impact`, `user_errors`

### Uptime & Health
`list_healthchecks`, `uptime_status`, `create_healthcheck`, `delete_healthcheck`

### Watches
`watch_status`, `watch`, `investigate`, `dismiss_watch`

### Analytics & Trends
`trends`, `top_movers`, `web_analytics`, `top_endpoints`, `traffic_heatmap`

### User Journeys
`user_journey`, `path_analysis`, `funnel_analysis`, `request_timeline`, `session_waterfall`

### Incidents
`incident_timeline`

### Server Metrics
`list_servers`, `query_metrics`, `server_health`

### Connectors
`list_connectors`, `get_connector`, `create_connector`, `test_connector`, `update_connector`, `delete_connector`

### Agent Memory
`get_notes`, `add_note`, `delete_note`

### Settings & Admin
`get_settings`, `update_retention`, `list_users`, `update_user_role`, `toggle_user_active`, `delete_user`, `get_audit_log`

## Access Levels

Each tool has an access level:

- **read** — Available to all authenticated users
- **admin** — Requires admin role (create/update/delete operations)

Tools that require a database connector are marked accordingly and will return an error with setup instructions if no connector is configured.
