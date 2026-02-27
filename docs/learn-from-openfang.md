# What OpenTrace Can Learn from OpenFang

An analysis of [OpenFang](https://github.com/RightNow-AI/openfang) (Rust-based Agent Operating System) and actionable ideas for OpenTrace.

---

## Executive Summary

OpenFang is a 137K-line Rust project that builds an "Agent Operating System" — autonomous AI agents that run on schedules, communicate across 40+ channels, and execute tools in sandboxed environments. While the domain differs from OpenTrace (application monitoring), several architectural patterns and engineering practices are directly applicable.

**High-impact takeaways for OpenTrace:**

1. **Tamper-evident audit log** (Merkle hash chain)
2. **Richer notification channels** beyond webhooks
3. **Circuit breaker pattern on the agent loop** (loop guards)
4. **Hot-reloadable configuration**
5. **Context overflow recovery** for AI watchers
6. **GCRA rate limiting** (smoother than fixed-window)
7. **Unified notification interface** across all alert sources

---

## 1. Merkle Hash-Chain Audit Trail

### What OpenFang does
Every security-critical action is recorded in an append-only log where each entry contains the SHA-256 hash of its own contents concatenated with the previous entry's hash. A `verify_integrity()` method can detect any tampering by recomputing the entire chain.

### Current state in OpenTrace
`internal/store/audit_store.go` stores audit entries in a simple SQLite table — user actions, target types, details, IP addresses. No chain integrity verification.

### Recommendation
**Add a `prev_hash` column** to the audit log table and compute SHA-256 chains on insert. This is a low-effort, high-value change for compliance-conscious users — especially those monitoring production databases.

**Effort: Small** — a migration adding one column plus ~50 lines in `audit_store.go`.

---

## 2. Richer Notification Channels

### What OpenFang does
40 channel adapters (Slack, Discord, Telegram, Teams, Email, PagerDuty, etc.) behind a unified `ChannelMessage` trait. Each adapter implements a common interface, and they're organized in deployment "waves" by priority.

### Current state in OpenTrace
Notifications are limited to:
- `WatchLogNotifier` — logs to slog
- `WatchWebhookNotifier` — HTTP POST to a URL
- Health check alerts use a separate but similar pair

### Recommendation
**Add 3–4 high-value notification channels** behind the existing `WatchAlertNotifier` interface:

| Priority | Channel | Why |
|----------|---------|-----|
| 1 | **Slack** (incoming webhook) | Most common for DevOps teams |
| 2 | **Email** (SMTP) | Universal fallback |
| 3 | **PagerDuty** (Events API v2) | On-call escalation |
| 4 | **Discord** (webhook) | Developer communities |

The `WatchAlertNotifier` interface is already well-designed for this — each new channel is just a new implementation. Also **unify the health check and watch notification interfaces** into a single dispatcher to reduce duplication.

**Effort: Medium** — each channel adapter is ~100-150 lines. The unification of health check + watch notification is a refactor.

---

## 3. Loop Guard / Circuit Breaker for AI Watchers

### What OpenFang does
The agent loop has a sophisticated `LoopGuard` that:
- Tracks total tool calls with a configurable global circuit breaker (default: 30)
- Detects repeated identical tool calls (hash-based dedup)
- Detects ping-pong patterns (alternating tools)
- Uses graduated responses: Allow → Warn → Block → CircuitBreak
- Has a maximum of 50 iterations and 5 continuations per agent run

### Current state in OpenTrace
AI watchers (`internal/agent/`) run an LLM agent loop, but there's no visible loop guard or tool-call circuit breaker. The connector already has a `CircuitBreaker` (`internal/connector/circuit_breaker.go`), but it protects database connections, not the agent loop itself.

### Recommendation
**Apply the existing `CircuitBreaker` pattern to the AI watcher's agent loop.** Add:
- A maximum iteration count (e.g., 50)
- A global tool-call limit (e.g., 30)
- Detection of repeated identical tool calls to prevent the LLM from getting stuck
- A per-run timeout (e.g., 5 minutes)

This prevents runaway LLM costs and stuck watchers.

**Effort: Small-Medium** — reuse the existing circuit breaker pattern, add counters to the agent loop.

---

## 4. Hot-Reloadable Configuration

### What OpenFang does
A background task polls the config file every 30 seconds. On modification, it applies changes and logs the delta. Config also supports composable includes with deep-merging of TOML files.

### Current state in OpenTrace
Configuration is loaded once at startup from environment variables + `.env` file. Runtime settings exist in the DB (via `SettingsStore`), but the core `Config` struct is immutable after boot. Many values are hardcoded (session duration, rate limit windows, background job intervals).

### Recommendation
**Two improvements:**

1. **File-watching for `.env` changes** — poll the `.env` file periodically (every 60s) and reload relevant settings. This is valuable for changing API keys, CORS origins, or toggling dev mode without restarting.

2. **Move more hardcoded values into `Config` or `SettingsStore`** — specifically: rate limit thresholds (currently hardcoded at 10 login/120 API per minute), background job intervals (15s poll, 15m session cleanup), and health check concurrency (16).

**Effort: Small** — the `.env` file watcher is ~40 lines; extracting hardcoded values is incremental.

---

## 5. Context Overflow Recovery for AI Watchers

### What OpenFang does
A 4-stage progressive recovery pipeline when the agent's context window fills up:
1. Auto-compact: remove older messages, keep recent 10 (at 70% capacity)
2. Aggressive compact: keep only last 4 messages (at 90%)
3. Tool result truncation: truncate all historical results to 2K chars
4. Final error: recommend manual reset

Uses a character-based heuristic (chars ÷ 4) for token estimation.

### Current state in OpenTrace
AI watchers run LLM agent loops, but there's no visible context management. If a watcher accumulates too many tool results, it could hit token limits and fail.

### Recommendation
**Implement a lightweight context budget** in the AI watcher agent loop:
- Estimate token usage with the chars÷4 heuristic
- When approaching the model's context limit, summarize older messages or truncate large tool results
- Set a configurable `max_context_tokens` per watcher

This prevents AI watcher failures during complex investigations.

**Effort: Medium** — requires integration with the LLM provider's token limits and the agent loop.

---

## 6. GCRA Rate Limiting

### What OpenFang does
Uses the GCRA (Generic Cell Rate Algorithm) for API rate limiting. GCRA provides smoother rate limiting than fixed windows — it spreads requests evenly rather than allowing bursts at window boundaries.

### Current state in OpenTrace
Rate limiting in `middleware.go` uses a fixed-window approach (10 req/min login, 120 req/min API). The values are hardcoded.

### Recommendation
**Replace fixed-window rate limiting with a token bucket or GCRA approach.** The Go ecosystem has good libraries for this (e.g., `golang.org/x/time/rate`). Benefits:
- No thundering herd at window boundaries
- Smoother traffic shaping
- Per-IP and per-API-key granularity

Also make the rate limit values configurable (see item 4).

**Effort: Small** — swap the implementation in `middleware.go`, ~50-80 lines.

---

## 7. Taint Tracking for SQL Guardrails

### What OpenFang does
A lattice-based taint propagation system tracks data provenance through labels (`ExternalNetwork`, `UserInput`, `PII`, `Secret`, `UntrustedAgent`). Each value carries taint labels, and "sinks" (like shell execution or network fetch) enforce policies about which labels are blocked.

### Current state in OpenTrace
The `internal/guardrail/` package validates SQL is read-only via AST parsing (`pg_query_go`). This is the primary safety mechanism for user-submitted and AI-generated SQL.

### Recommendation
**Extend the guardrail with taint awareness.** When an AI watcher generates SQL:
- Tag the query as `ai_generated`
- Apply stricter validation (e.g., block `pg_catalog` access, restrict to specific schemas)
- Log differently for audit purposes

This adds defense-in-depth beyond the current read-only check.

**Effort: Small** — add a `source` parameter to the guardrail validation function and adjust rules based on origin.

---

## 8. Session Repair for AI Watchers

### What OpenFang does
A 7-phase session validation and repair pipeline:
1. Collect all tool-use IDs from assistant messages
2. Remove orphaned tool results
3. Reorder misplaced results
4. Insert synthetic errors for missing results
5. Deduplicate results
6. Remove aborted messages
7. Enforce strict role alternation

Also strips prompt injection markers (`<|system|>`, `IGNORE PREVIOUS INSTRUCTIONS`) from tool results.

### Current state in OpenTrace
AI watchers have tool results flowing back into the LLM context, but no visible session repair or sanitization.

### Recommendation
**Add two specific safeguards:**

1. **Tool result sanitization** — strip prompt injection markers before feeding tool output back to the LLM. A simple regex filter catching `<|system|>`, `<<SYS>>`, and `IGNORE PREVIOUS` patterns.

2. **Message history repair** — ensure tool results always follow their corresponding tool-use messages. Orphaned results or missing responses can cause API errors with Claude/GPT.

**Effort: Small** — ~100 lines of sanitization + reordering logic.

---

## 9. Model Routing by Complexity

### What OpenFang does
A heuristic scoring system classifies requests into Simple/Medium/Complex tiers and routes to cheap/mid/premium models accordingly. Factors include: token count, number of available tools, code markers, conversation depth, and system prompt length.

### Current state in OpenTrace
AI watchers use a single configured LLM provider (Anthropic, OpenAI, Ollama, Gemini). No dynamic model selection.

### Recommendation
**For cost optimization,** allow AI watchers to use a cheap model for simple checks and escalate to a more capable model only when the investigation gets complex:
- Simple threshold checks → small/fast model (e.g., Haiku)
- Multi-step investigations with tool use → capable model (e.g., Sonnet)
- Complex root-cause analysis → premium model (e.g., Opus)

The scoring heuristic could be simple: count the number of tool calls in the conversation so far.

**Effort: Medium** — requires LLM driver abstraction changes and configuration.

---

## 10. Provider Health & Fallback Chain

### What OpenFang does
LLM drivers support a fallback chain — if the primary provider fails (rate limit, outage), requests automatically route to the next provider. Error classification distinguishes retryable vs. permanent failures, and exponential backoff with circuit breaking prevents hammering a down provider.

### Current state in OpenTrace
`internal/llm/` has multiple providers but no fallback chain. If the configured provider fails, the watcher run fails.

### Recommendation
**Add a simple fallback chain** to the LLM driver:
```
primary: anthropic/claude-sonnet → fallback: openai/gpt-4o → fallback: ollama/local
```
With exponential backoff on retryable errors (429, 503) and immediate failover on permanent errors.

OpenTrace already has a `retry` package (`internal/retry/retry.go`) with permanent error support — wire it into the LLM providers.

**Effort: Medium** — wrap the existing LLM providers in a chain with retry logic.

---

## 11. Graceful Shutdown Improvements

### What OpenFang does
Multi-signal handling (SIGINT, SIGTERM, API-initiated), PID file management with stale-PID detection, and orderly component teardown (channel bridges → kernel → cleanup).

### Current state in OpenTrace
The `run()` function handles `os.Interrupt` but the shutdown sequence for background goroutines (watch scheduler, health checks, aggregation jobs, session cleanup) relies on context cancellation without ordering guarantees.

### Recommendation
**Add ordered shutdown phases:**
1. Stop accepting new HTTP connections
2. Drain the ingest queue
3. Stop the watch and health check schedulers
4. Wait for in-flight background jobs
5. Close database connections

This prevents data loss during deploys.

**Effort: Small** — restructure the existing context cancellation into ordered phases.

---

## 12. Webhook Security (HMAC Signing)

### What OpenFang does
OFP wire protocol uses HMAC-SHA256 mutual authentication for all inter-node communication.

### Current state in OpenTrace
Webhook notifications are plain HTTP POST with no signature verification mechanism. Receiving services cannot verify that webhooks actually came from OpenTrace.

### Recommendation
**Add HMAC-SHA256 signing to outbound webhooks.** Include a `X-OpenTrace-Signature` header with the HMAC of the request body using a user-configured secret. This is standard practice (GitHub, Stripe, Slack all do this).

**Effort: Small** — ~30 lines in `watch_notify.go` and `healthcheck/alert.go`.

---

## Priority Matrix

| # | Improvement | Impact | Effort | Priority |
|---|-----------|--------|--------|----------|
| 2 | Richer notification channels (Slack, Email) | High | Medium | **P0** |
| 3 | Loop guard for AI watchers | High | Small | **P0** |
| 12 | Webhook HMAC signing | High | Small | **P0** |
| 1 | Merkle hash-chain audit log | Medium | Small | **P1** |
| 6 | GCRA rate limiting | Medium | Small | **P1** |
| 8 | Session repair / prompt injection guard | Medium | Small | **P1** |
| 11 | Graceful shutdown ordering | Medium | Small | **P1** |
| 4 | Hot-reloadable configuration | Medium | Small | **P2** |
| 5 | Context overflow recovery | Medium | Medium | **P2** |
| 7 | Taint tracking for SQL guardrails | Low | Small | **P2** |
| 10 | LLM provider fallback chain | Medium | Medium | **P2** |
| 9 | Model routing by complexity | Low | Medium | **P3** |
