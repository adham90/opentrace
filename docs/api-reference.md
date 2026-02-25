# API Reference

OpenTrace exposes a REST API for all operations. All endpoints require session-based authentication unless noted otherwise.

## Authentication

- **Web UI:** Session cookie (`opentrace_session`) via login form
- **Log ingestion:** Bearer token via `Authorization: Bearer <API_KEY>` header
- **MCP SSE:** Bearer token via `Authorization: Bearer <MCP_TOKEN>` header

## Connectors

```
POST   /api/connectors           # Create a connector
GET    /api/connectors           # List all connectors
GET    /api/connectors/{id}      # Get a specific connector
POST   /api/connectors/{id}/test # Test connectivity
DELETE /api/connectors/{id}      # Delete a connector
```

## Logs

```
POST   /api/logs                 # Ingest log entries (API key auth)
GET    /api/logs/{id}            # Get a specific log entry
GET    /api/logs/poll            # Poll for new logs (long-polling)
```

### Ingesting Logs

```bash
curl -X POST http://localhost:8080/api/logs \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "service": "payment-api",
    "level": "error",
    "message": "Payment gateway timeout after 30s",
    "environment": "production",
    "event_type": "payment.failed",
    "metadata": {"transaction_id": "tx_abc123"}
  }'
```

Supports gzip-compressed request bodies (`Content-Encoding: gzip`) and batch deduplication.

### Log Fields

| Field | Type | Required | Description |
|---|---|---|---|
| `service` | string | yes | Service name (e.g., `payment-api`) |
| `level` | string | yes | Log level: `debug`, `info`, `warn`, `error` |
| `message` | string | yes | Log message |
| `environment` | string | no | Environment name (e.g., `production`) |
| `event_type` | string | no | Event type (e.g., `payment.failed`) |
| `metadata` | object | no | Arbitrary key-value pairs |
| `trace_id` | string | no | Distributed trace ID |
| `request_id` | string | no | Request ID |
| `commit_hash` | string | no | Git commit hash for deploy tracking |
| `exception_class` | string | no | Exception class name |
| `backtrace` | string | no | Stack trace |
| `source_file` | string | no | Source file location |
| `source_line` | integer | no | Source line number |

## Watches

```
GET    /api/watches              # List all watches
GET    /api/watches/{id}         # Get a specific watch
POST   /api/watches              # Create a watch
PUT    /api/watches/{id}         # Update a watch
DELETE /api/watches/{id}         # Delete a watch
POST   /api/watches/{id}/pause   # Pause a watch
POST   /api/watches/{id}/resume  # Resume a watch
GET    /api/watches/{id}/runs    # List run history
GET    /api/watches/{id}/alerts  # List alerts for a watch
```

### Watch Metrics

| Metric | Description |
|---|---|
| `error_rate` | Error count / total log count |
| `error_count` | Number of error-level logs |
| `log_count` | Total log count |
| `heartbeat` | Presence check (alerts if no logs seen) |
| `response_time` | Average response time (from request summaries) |
| `p95_response` | 95th percentile response time |
| `sql_count` | Average SQL queries per request |
| `cache_hit_rate` | Cache hit ratio |

### Watch Scheduling

Watches support two scheduling modes:

**Simple intervals** (default) — set via `time_range`:
```json
{"time_range": "15m"}
```

**Cron expressions** — set via `schedule` for precise control:
```json
{
  "time_range": "1h",
  "schedule": "0 9 * * 1-5"
}
```

When `schedule` is set, it controls **when** the watch runs. The `time_range` field becomes just the log lookback window.

| Format | Example | Description |
|---|---|---|
| Simple interval | `5m`, `1h`, `30s` | Run every N seconds/minutes/hours |
| 5-field cron | `0 9 * * *` | Daily at 9:00 AM UTC |
| Weekday cron | `0 9 * * 1-5` | Weekdays at 9:00 AM UTC |
| Business hours | `*/15 8-17 * * 1-5` | Every 15 min, Mon-Fri 8am-5pm |
| Predefined | `@hourly`, `@daily`, `@weekly` | Standard intervals |
| Every duration | `@every 30s` | Run every 30 seconds |

## Alerts

```
GET    /api/watches/alerts/count     # Get unread alert count
GET    /api/watches/{id}/alerts      # List alerts for a watch
GET    /api/watches/alerts/{alertId} # Get a specific alert
POST   /api/watches/alerts/{id}/read    # Mark alert as read
POST   /api/watches/alerts/{id}/dismiss # Dismiss an alert
```

## Errors

```
GET    /api/errors                        # List error groups
GET    /api/errors/{fingerprint}          # Get error group detail
POST   /api/errors/{fingerprint}/resolve  # Resolve an error group
POST   /api/errors/{fingerprint}/ignore   # Ignore an error group
GET    /api/errors/impact/top             # Top errors by user impact
GET    /api/errors/{fingerprint}/impact   # Impact for a specific error
GET    /api/errors/{fingerprint}/affected-users  # Affected users list
GET    /api/errors/user/{userID}          # Errors for a specific user
```

## Health Checks

```
GET    /api/healthchecks                  # List health checks
POST   /api/healthchecks                  # Create a health check
DELETE /api/healthchecks/{id}             # Delete a health check
GET    /api/healthchecks/{id}/results     # Health check results
GET    /api/healthchecks/uptime           # Uptime summary
```

## Servers

```
POST   /api/servers/register     # Register a server (agent self-registration)
POST   /api/servers/{id}/metrics # Push metric batch from agent
GET    /api/servers              # List all servers
GET    /api/servers/{id}         # Get server details
DELETE /api/servers/{id}         # Remove a server
GET    /api/servers/{id}/metrics # Query metrics (time range, metric name)
```

## Analytics

```
GET    /api/trends                  # Time-series trend data
GET    /api/trends/deploys          # Deploy markers
GET    /api/analytics/summary       # Analytics summary
GET    /api/analytics/endpoints     # Top endpoints
GET    /api/analytics/heatmap       # Traffic heatmap
```

## Journeys

```
GET    /api/journeys/sessions                       # List sessions
GET    /api/journeys/sessions/{sessionID}           # Get session
GET    /api/journeys/sessions/{sessionID}/requests  # Session requests
GET    /api/journeys/sessions/{sessionID}/timeline  # Session timeline
GET    /api/journeys/user/{userID}                  # User journey
GET    /api/journeys/paths                          # Path analysis
GET    /api/journeys/funnels                        # List funnels
GET    /api/journeys/funnels/{funnelID}             # Analyze funnel
GET    /api/journeys/timeline/{logID}               # Request timeline
```

## Settings

```
GET    /api/settings/retention         # Get retention settings
PUT    /api/settings/retention         # Update retention
GET    /api/settings/api-key           # Get API key
PUT    /api/settings/api-key           # Update API key
GET    /api/settings/cors              # Get CORS origins
PUT    /api/settings/cors              # Update CORS origins
GET    /api/settings/query-guardrails  # Get query guardrails
PUT    /api/settings/query-guardrails  # Update query guardrails
GET    /api/settings/mcp-name          # Get MCP server name
PUT    /api/settings/mcp-name          # Update MCP server name
GET    /api/audit-log                  # View audit log (admin)
```

## Health

```
GET    /healthz                  # Health check (no auth required)
GET    /api/version              # Version info
GET    /api/version/check        # Check for updates
```

Returns:
```json
{"status": "ok", "version": "0.13.0"}
```
