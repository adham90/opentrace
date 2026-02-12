# Plan 001: Webhook Notifiers (Slack, Discord, Generic)

## Overview

Extend the existing notifier system to support real-world alert delivery channels beyond the dashboard. The `Notifier` interface and `WebhookNotifier` already exist — this plan adds **Slack**, **Discord**, and **generic webhook** formatting, plus a UI for configuring them per monitor.

**Effort**: Low | **Impact**: High

---

## Current State

- `Notifier` interface: `Send(ctx, alert) error`
- `DashboardNotifier`: no-op (alert already in DB)
- `WebhookNotifier`: sends raw JSON POST to a URL
- `ParseNotifiers(json.RawMessage) []Notifier` reads from watcher's `notify` column
- `SendAll()` dispatches to all notifiers, logs errors, continues on failure
- Notify config stored as `json.RawMessage` in watcher/monitor row

---

## Goals

1. Slack-formatted alerts via incoming webhook URL
2. Discord-formatted alerts via webhook URL
3. Improved generic webhook with configurable headers and template
4. UI in monitor create/edit form to configure notification channels
5. "Test notification" button to verify webhook connectivity

---

## Phase 1: Slack Notifier

### 1.1 Implementation — `internal/watcher/notify_slack.go`

```go
type SlackNotifier struct {
    WebhookURL string
    Channel    string // optional override
}
```

- Format alert as Slack Block Kit payload:
  - Header block: monitor title + severity emoji (🔴 critical, 🟡 warning, ℹ️ info)
  - Section block: alert summary
  - Context block: environment, timestamp, monitor ID
  - Action block: "View in OpenTrace" link (if base URL configured)
- POST to Slack incoming webhook URL
- Timeout: 10 seconds
- Retry: 1 retry on 5xx with 2s backoff

### 1.2 Config Format

```json
{
  "type": "slack",
  "webhook_url": "https://hooks.slack.com/services/T.../B.../xxx",
  "channel": "#alerts"
}
```

### 1.3 Tests — `internal/watcher/notify_slack_test.go`

- Unit test: verify payload format matches Slack Block Kit schema
- Unit test: httptest server returning 200 → no error
- Unit test: httptest server returning 500 → retry once, then error
- Unit test: timeout handling

---

## Phase 2: Discord Notifier

### 2.1 Implementation — `internal/watcher/notify_discord.go`

```go
type DiscordNotifier struct {
    WebhookURL string
}
```

- Format as Discord embed:
  - Color: red (critical), yellow (warning), blue (info)
  - Title: alert title
  - Description: summary
  - Fields: environment, severity, timestamp
  - Footer: "OpenTrace Monitor"
- POST to Discord webhook URL with `{"embeds": [...]}`
- Same timeout/retry as Slack

### 2.2 Config Format

```json
{
  "type": "discord",
  "webhook_url": "https://discord.com/api/webhooks/..."
}
```

### 2.3 Tests — `internal/watcher/notify_discord_test.go`

- Same pattern as Slack tests

---

## Phase 3: Enhanced Generic Webhook

### 3.1 Improvements to existing `WebhookNotifier`

- Add optional custom headers (e.g., `Authorization: Bearer xxx`)
- Add optional payload template (Go `text/template` with alert fields)
- Default template: current `WebhookPayload` JSON for backwards compatibility

### 3.2 Config Format

```json
{
  "type": "webhook",
  "url": "https://example.com/alerts",
  "headers": {
    "Authorization": "Bearer token123"
  },
  "template": "Monitor {{.WatcherTitle}} fired: {{.Summary}}"
}
```

If `template` is set, send as `text/plain`; otherwise send as `application/json` with default payload.

### 3.3 Tests

- Existing tests still pass (backwards compat)
- Custom headers sent correctly
- Template rendering with all alert fields

---

## Phase 4: `ParseNotifiers` Update

Update `ParseNotifiers()` in `notify.go` to dispatch by `type` field:

```go
switch typ {
case "slack":    → SlackNotifier
case "discord":  → DiscordNotifier
case "webhook":  → WebhookNotifier (enhanced)
case "dashboard": → DashboardNotifier
default:         → log warning, skip
}
```

Backwards compatibility: bare string `"dashboard"` still works.

---

## Phase 5: Dashboard UI

### 5.1 Monitor Create/Edit Form

Add "Notifications" section to the monitor form:

- "Add notification channel" button
- Channel type dropdown: Dashboard, Slack, Discord, Webhook
- Conditional fields based on type:
  - Slack: webhook URL, optional channel
  - Discord: webhook URL
  - Webhook: URL, headers (key-value pairs), optional template
- Multiple channels supported (add/remove rows)
- Serialize to `notify` JSON on save

### 5.2 Test Notification

- `POST /api/watchers/{id}/test-notify` endpoint
- Sends a test alert through all configured notifiers
- Returns success/failure per channel
- UI button: "Send test notification" with result feedback

---

## Phase 6: MCP Integration

Update `create_monitor` MCP tool to accept notification config:

```json
{
  "notify": [
    {"type": "slack", "webhook_url": "..."},
    {"type": "discord", "webhook_url": "..."}
  ]
}
```

---

## File Changes Summary

| File | Change |
|------|--------|
| `internal/watcher/notify_slack.go` | New — Slack notifier |
| `internal/watcher/notify_slack_test.go` | New — tests |
| `internal/watcher/notify_discord.go` | New — Discord notifier |
| `internal/watcher/notify_discord_test.go` | New — tests |
| `internal/watcher/notify.go` | Update ParseNotifiers dispatch, enhance WebhookNotifier |
| `internal/watcher/notify_test.go` | Update existing tests |
| `internal/web/watchers.go` | Add test-notify endpoint |
| `internal/web/server.go` | Register test-notify route |
| `internal/web/templates/watchers_form.html` | Add notification channel UI |
| `internal/mcp/server.go` | Accept notify param in create_monitor |

---

## Out of Scope

- Email notifications (requires SMTP config — separate plan)
- PagerDuty / OpsGenie (future plan)
- Notification rate limiting / deduplication (future)
- Notification history/audit log (future)
