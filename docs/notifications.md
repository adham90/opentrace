# Notifications

OpenTrace delivers alerts to chat channels. Two are built in — **Slack** (incoming webhook) and **Telegram** (bot) — plus an always-on `slog` fallback and an optional generic webhook.

Channels are configured at runtime through the MCP `admin` tool and stored in the `app_config` table. Config is read on every send, so **enabling, disabling, or changing a channel takes effect on the next alert — no restart**.

## What triggers a notification

| Event | Source |
|---|---|
| Watch rule fires | Threshold watches on error rate, response time, log volume, error count, SQL count, cache hit rate, service heartbeat |
| Health check transitions | Any up ⇄ down status change on a scheduled HTTP check |

Error spikes and new error groups reach chat only if a watch rule covers them — nothing else calls a notifier.

---

## Slack

### 1. Create an incoming webhook

1. Go to [api.slack.com/apps](https://api.slack.com/apps) → **Create New App** → **From scratch**.
2. Name it (e.g. `OpenTrace`) and pick your workspace.
3. Open **Incoming Webhooks** and toggle it **On**.
4. **Add New Webhook to Workspace** → choose the channel that should receive alerts.
5. Copy the URL — it has the shape `https://hooks.slack.com/services/<team-id>/<webhook-id>/<secret>`.

The webhook URL *is* the credential — anyone holding it can post to that channel. Treat it like a token. OpenTrace never echoes it back in a tool response and redacts it from error messages.

### 2. Configure OpenTrace

From an agent connected to your OpenTrace MCP server:

```
admin(action: "notifications", params: {provider: "slack", webhook_url: "https://hooks.slack.com/services/T.../B.../..."})
```

Setting the URL auto-enables the channel. Then verify:

```
admin(action: "notifications", params: {provider: "slack", test: true})
```

A ✅ test message should appear in the channel within a second.

### Managing the channel

| Command | Effect |
|---|---|
| `admin(action: "notifications")` | List all channels and their status |
| `admin(action: "notifications", params: {provider: "slack"})` | Show Slack status only |
| `admin(action: "notifications", params: {provider: "slack", test: true})` | Send a test message |
| `admin(action: "notifications", params: {provider: "slack", enabled: false})` | Mute without discarding the URL |
| `admin(action: "notifications", params: {provider: "slack", enabled: true})` | Unmute |
| `admin(action: "notifications", params: {provider: "slack", webhook_url: "..."})` | Rotate the webhook / switch channel |

`webhook_url` must start with `https://hooks.slack.com/` — anything else is rejected.

### Message format

Alerts are rendered once as a small HTML subset and converted to Slack mrkdwn on the way out (`<b>` → `*bold*`, `<i>` → `_italic_`, `<code>` → `` `code` ``), so Slack and Telegram stay in sync from a single formatter.

```
🚨 *Watch alert*
*Metric:* error_rate
*Value:* 12.4 (threshold 5)
*Urgency:* high
*Service:* api
*Environment:* production
Error rate exceeded threshold over the last 5 minutes
2026-07-27T14:03:11Z
```

---

## Telegram

### 1. Create a bot

1. Message [@BotFather](https://t.me/BotFather) on Telegram → `/newbot` → follow the prompts.
2. Copy the bot token (`123456789:AA...`).
3. Add the bot to the target chat or group, send it a message, then find the chat ID via
   `https://api.telegram.org/bot<TOKEN>/getUpdates` (look for `"chat":{"id":...}`). Group IDs are negative.

### 2. Configure OpenTrace

```
admin(action: "notifications", params: {provider: "telegram", bot_token: "123456789:AA...", chat_id: "-1001234567890"})
admin(action: "notifications", params: {provider: "telegram", test: true})
```

Same `enabled: true|false` toggle as Slack. Messages are sent with `parse_mode: HTML`.

---

## Generic webhook

Set `OPENTRACE_ALERT_WEBHOOK_URL` in the environment to POST raw JSON for both watch and health-check alerts. This one is environment-only and requires a restart.

```
X-OpenTrace-Event: watch.alert
Content-Type: application/json

{"alert_id":"...","watch_id":"...","metric":"error_rate","urgency":"high",
 "summary":"...","trigger_value":12.4,"threshold_value":5,
 "service":"api","environment":"production","timestamp":"2026-07-27T14:03:11Z"}
```

Health-check alerts use `X-OpenTrace-Event: healthcheck.status_change` with the health-check payload. 5xx responses are retried up to 3 times with backoff; 4xx are treated as permanent failures.

---

## Configuring without an agent

There is no CLI subcommand or HTTP endpoint for notification settings — the MCP `admin` tool is the supported path. If you need to script it, the config is a single JSON row in `app_config`:

```bash
sqlite3 ~/.opentrace/opentrace.db \
  "INSERT INTO app_config (key,value) VALUES ('slack_config','{\"webhook_url\":\"https://hooks.slack.com/services/...\",\"enabled\":true}') \
   ON CONFLICT(key) DO UPDATE SET value=excluded.value"
```

The Telegram equivalent is the `telegram_config` key with `{"bot_token":"...","chat_id":"...","enabled":true}`.

---

## Troubleshooting

| Symptom | Cause |
|---|---|
| Test succeeds, real alerts never arrive | No watch rules or health checks exist yet, or none have crossed their threshold. Check `admin(action: "notifications")` shows `enabled: true`. |
| `slack API error 403: invalid_token` | The webhook was revoked or the app was removed from the workspace. Create a new one and re-run the configure command. |
| `slack API error 404: no_service` | The URL is wrong or truncated — copy the full path including all three segments. |
| Nothing in logs | Delivery failures are logged at `warn` with the message `watch notification error` / `health check notification error`. Credentials are redacted. |
| Alerts stop after moving the channel | Slack webhooks are bound to the channel chosen at creation. Create a new webhook for the new channel. |
