# OpenTrace Plugin Plan

> Turn any AI coding agent into an observability expert with automatic context injection from your OpenTrace instance.

## 1. Vision

The Vercel plugin (`vercel/vercel-plugin`) proves that a **skill + hook** architecture can inject domain expertise into AI agents *at exactly the right moment*. OpenTrace should ship a similar plugin — but instead of teaching agents about a platform, it teaches them about **your production systems** by connecting live observability data (errors, traces, deploys, logs, performance) to the agent's coding workflow.

**Key difference from Vercel's approach**: Vercel's plugin is a standalone repo that ships static knowledge via Node.js hooks. OpenTrace's plugin is **built into the same Go binary** — the hooks are `opentrace hook <event>` subcommands that read stdin, call the OpenTrace server API, and write JSON to stdout. No Node.js, no TypeScript, no npm — just the single `opentrace` binary.

**Two roles, one binary**: The `opentrace` binary serves two roles:
1. **Server** (`opentrace serve`) — runs on your server, ingests telemetry, serves the web UI and MCP SSE endpoint
2. **Client CLI** (`opentrace hook`, `opentrace plugin install`) — runs on the developer's machine, provides hooks and plugin management

The connect script auto-downloads the client binary to `~/.opentrace/bin/opentrace` so developers don't need to install anything manually.

---

## 2. Architecture Overview

```
Developer's machine (client)              Server
────────────────────────────              ──────
~/.opentrace/
├── bin/opentrace                         opentrace serve
│   ├── hook session-start    ←stdin/stdout→  Claude Code hooks
│   ├── hook pretooluse-*     ──HTTP/2s──→    GET /api/plugin/*
│   ├── hook posttooluse-*    ──HTTP/2s──→    GET /api/plugin/*
│   └── plugin install                        GET /api/plugin/bundle
├── plugin/                              # Extracted static assets
│   ├── skills/*/SKILL.md
│   ├── agents/*.md
│   ├── commands/*.md
│   ├── opentrace.md
│   └── generated/skill-manifest.json
└── config.json                          # Server URL + auth token

Project directory
├── .mcp.json                            # MCP SSE connection → server
├── .opentrace/plugin.json               # Per-project config (server URL + token)
└── .claude/settings.local.json          # Hook registrations → ~/.opentrace/bin/opentrace
```

### Source Layout

```
opentrace/                               # Same repo — server + client in one binary
├── plugin/                              # Static plugin assets (embedded via go:embed)
│   ├── .plugin/plugin.json              # Claude Code plugin manifest
│   ├── opentrace.md                     # Observability knowledge graph
│   ├── opentrace-session.md             # Thin session context (injected at start)
│   ├── skills/
│   │   ├── error-investigation/SKILL.md
│   │   ├── trace-analysis/SKILL.md
│   │   ├── deploy-safety/SKILL.md
│   │   ├── log-search/SKILL.md
│   │   ├── database-debugging/SKILL.md
│   │   ├── service-health/SKILL.md
│   │   ├── code-risk/SKILL.md
│   │   ├── incident-response/SKILL.md
│   │   ├── performance-optimization/SKILL.md
│   │   ├── test-generation/SKILL.md
│   │   └── alerting-setup/SKILL.md
│   ├── agents/
│   │   ├── incident-responder.md
│   │   ├── performance-analyst.md
│   │   └── reliability-engineer.md
│   ├── commands/
│   │   ├── status.md
│   │   ├── investigate.md
│   │   ├── deploy-check.md
│   │   └── connect.md
│   └── generated/
│       └── skill-manifest.json          # Pre-compiled skill patterns
├── internal/hook/                       # Go hook handlers (the core engine)
│   ├── hook.go                          # Entry point: parse stdin, dispatch to handler
│   ├── session_start.go                 # SessionStart: profiler + context injection
│   ├── pretooluse.go                    # PreToolUse: skill injection + error context
│   ├── posttooluse.go                   # PostToolUse: validation + deploy check
│   ├── skill_matcher.go                 # Pattern matching engine
│   ├── dedup.go                         # Skill deduplication (atomic file claims)
│   ├── client.go                        # HTTP client to OpenTrace server (2s timeout)
│   ├── profiler.go                      # Project framework detection
│   ├── manifest.go                      # Load/parse skill-manifest.json
│   └── config.go                        # Read .opentrace/plugin.json + ~/.opentrace/config.json
├── internal/api/plugin_endpoint.go      # Server: GET /api/plugin/{context,services,file-errors,deploy-status}
├── internal/api/plugin_bundle.go        # Server: GET /api/plugin/bundle (binary + assets download)
└── cmd/opentrace/
    ├── main.go                          # "hook", "plugin" subcommands
    ├── hook.go                          # CLI: opentrace hook <event-name>
    └── plugin.go                        # CLI: opentrace plugin {install,update,doctor}
```

### Why Go Instead of Node.js

Vercel uses TypeScript hooks because that's their ecosystem. OpenTrace is a Go project. Using Go for hooks means:

1. **Zero extra dependencies** — no Node.js required on the developer's machine
2. **Single binary** — same `opentrace` binary runs the server and the client-side hooks
3. **Fast startup** — Go binary launches in ~5ms vs ~100ms for Node.js
4. **Shared code** — hooks reuse the same HTTP client, config parsing, and types as the server
5. **Cross-platform** — `go build` for linux/darwin/windows, same binary serves all roles
6. **Simple distribution** — server serves its own binary at `/api/plugin/binary` for auto-download

---

## 3. How Installation Works

### Current Flow (MCP only)
```
curl -s http://server:8080/connect | bash
→ Authenticates user
→ Writes .mcp.json with SSE endpoint + token
→ Done (MCP works, but no hooks/skills)
```

### New Flow (MCP + Plugin + Binary)
```
curl -s http://server:8080/connect | bash
→ Step 1: Authenticates user, gets MCP token
→ Step 2: Writes .mcp.json (SSE endpoint + token) — MCP works immediately
→ Step 3: Downloads opentrace binary to ~/.opentrace/bin/opentrace
→ Step 4: Runs `opentrace plugin install --server <url> --token <token>`
    → Extracts embedded plugin assets to ~/.opentrace/plugin/
    → Writes ~/.opentrace/config.json (global: server URL + token)
    → Writes .opentrace/plugin.json (per-project: server URL + token)
    → Writes .claude/settings.local.json (hook registrations)
    → Adds ~/.opentrace/bin to PATH hint
→ Done — next Claude Code session has full MCP + hooks + skills
```

### Binary Distribution

The OpenTrace server serves its own client binary at:
```
GET /api/plugin/binary?os=darwin&arch=arm64
GET /api/plugin/binary?os=linux&arch=amd64
GET /api/plugin/binary?os=darwin&arch=amd64
```

The server cross-compiles all platform binaries at build time (via `go build` with GOOS/GOARCH) and embeds them, OR the server serves a download redirect to GitHub Releases. The connect script auto-detects OS/arch:

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
esac
```

### What Gets Installed Where

```
~/.opentrace/                     # Global (shared across all projects)
├── bin/opentrace                 # The client binary (downloaded from server)
├── config.json                   # Global config: default server URL + token
└── plugin/                       # Extracted static assets
    ├── skills/*/SKILL.md         # Observability skill guides
    ├── agents/*.md               # Specialist agent definitions
    ├── commands/*.md             # Slash command definitions
    ├── opentrace.md              # Knowledge graph
    └── generated/
        └── skill-manifest.json   # Pre-compiled patterns

<project>/.opentrace/             # Per-project (gitignored)
└── plugin.json                   # Project config: server URL + token
                                  # (can override global config.json)

<project>/.claude/
└── settings.local.json           # Hook registrations (gitignored)

<project>/.mcp.json               # MCP SSE connection (gitignored)
```

### Upgrade Path

```bash
# User upgrades their server → binary version may drift
# Option A: Re-run connect (re-downloads matching binary)
curl -s http://server:8080/connect | bash

# Option B: Explicit update
~/.opentrace/bin/opentrace plugin update --server http://server:8080

# Option C: Auto-update check (session-start hook checks version once/day)
# If server version > binary version, inject a one-line notice:
# "OpenTrace plugin update available. Run: opentrace plugin update"
```

### Users Without `curl | bash`

For users who prefer manual setup:
```bash
# 1. Download binary directly
curl -Lo ~/.opentrace/bin/opentrace http://server:8080/api/plugin/binary
chmod +x ~/.opentrace/bin/opentrace

# 2. Install plugin
~/.opentrace/bin/opentrace plugin install --server http://server:8080 --token <token>

# 3. MCP config is still written by connect, or manually:
# Create .mcp.json with SSE endpoint
```

---

## 4. Plugin Manifest

### `.plugin/plugin.json` (Claude Code)
```json
{
  "name": "opentrace-plugin",
  "version": "0.1.0",
  "description": "Connects AI coding agents to your OpenTrace observability data — automatic error context, production insights, and deploy safety checks.",
  "author": {
    "name": "OpenTrace",
    "url": "https://github.com/adham90/opentrace"
  },
  "repository": "https://github.com/adham90/opentrace",
  "license": "MIT",
  "keywords": [
    "observability", "traces", "errors", "logs", "deploys",
    "monitoring", "debugging", "incidents", "performance"
  ]
}
```

---

## 5. Hook System Design

### 5.1 Hook Contract

Claude Code hooks receive JSON on **stdin** and produce JSON on **stdout**:

**Input** (from Claude Code):
```json
{
  "session_id": "abc123",
  "cwd": "/path/to/project",
  "hook_event_name": "PreToolUse",
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "/path/to/project/app/controllers/payments_controller.rb",
    "old_string": "...",
    "new_string": "..."
  }
}
```

**Output** (to Claude Code):
```json
{
  "hookSpecificOutput": {
    "additionalContext": "OpenTrace: This file has 3 production errors:\n1. NoMethodError in process_payment..."
  }
}
```

**Exit codes**:
- `0` — success, parse stdout as JSON
- `2` — block the action (stderr becomes feedback to Claude)
- Other — non-blocking error, ignored

### 5.2 Hook Registry

Hooks are registered in `.claude/settings.local.json` (written by the connect script):

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup|resume|clear|compact",
        "hooks": [
          {
            "type": "command",
            "command": "opentrace hook session-start"
          }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Read|Edit|Write|Bash",
        "hooks": [
          {
            "type": "command",
            "command": "opentrace hook pretooluse-skill-inject",
            "timeout": 5
          }
        ]
      },
      {
        "matcher": "Edit|Write",
        "hooks": [
          {
            "type": "command",
            "command": "opentrace hook pretooluse-error-context",
            "timeout": 5
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Write|Edit",
        "hooks": [
          {
            "type": "command",
            "command": "opentrace hook posttooluse-validate",
            "timeout": 5
          }
        ]
      },
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "opentrace hook posttooluse-deploy-check",
            "timeout": 5
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "opentrace hook prompt-skill-inject",
            "timeout": 5
          }
        ]
      }
    ]
  }
}
```

**Note**: No `hooks.json` file needed — hooks go directly into Claude Code's settings. The plugin directory only holds static assets (skills, knowledge graph, agents, commands).

### 5.3 Hook Implementations

All hooks are subcommands of `opentrace hook <name>`. They share a common pattern:

```go
// cmd/opentrace/hook.go
func runHook() error {
    if len(os.Args) < 3 {
        return fmt.Errorf("usage: opentrace hook <event-name>")
    }

    // Parse stdin
    var input hook.Input
    if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
        return err
    }

    // Dispatch to handler
    var output *hook.Output
    var err error
    switch os.Args[2] {
    case "session-start":
        output, err = hook.HandleSessionStart(input)
    case "pretooluse-skill-inject":
        output, err = hook.HandlePreToolUseSkillInject(input)
    case "pretooluse-error-context":
        output, err = hook.HandlePreToolUseErrorContext(input)
    case "posttooluse-validate":
        output, err = hook.HandlePostToolUseValidate(input)
    case "posttooluse-deploy-check":
        output, err = hook.HandlePostToolUseDeployCheck(input)
    case "prompt-skill-inject":
        output, err = hook.HandlePromptSkillInject(input)
    default:
        return fmt.Errorf("unknown hook: %s", os.Args[2])
    }

    if err != nil {
        return err
    }

    // Write output to stdout
    return json.NewEncoder(os.Stdout).Encode(output)
}
```

### 5.4 Hook Details

#### `session-start` (SessionStart)
**Purpose**: Detect project, inject live system context

```
1. PROFILER — Detect project framework:
   - Scan cwd for marker files: package.json, Gemfile, go.mod, pyproject.toml,
     docker-compose.yml, .env (SERVICE_NAME, OTEL_SERVICE_NAME)
   - Map framework to likely service names
   - Store detected services in OPENTRACE_PLUGIN_SERVICES env var (via Claude env file)

2. CONTEXT INJECTION — Fetch live system state:
   - Read .opentrace/plugin.json for server URL + token
   - Call GET /api/plugin/context?services=<detected>
   - Server returns: active error count, last deploy, health status, ongoing incidents
   - Inject as additionalContext:
     "OpenTrace: web-api has 3 active errors (2 critical).
      Last deploy: 2h ago (commit abc123). No ongoing incidents.
      Use the MCP opentrace tool for details."

3. GRACEFUL DEGRADATION — If server unreachable:
   - Inject static session context from opentrace-session.md
   - Log warning to stderr (visible in Claude Code debug output)
```

#### `pretooluse-skill-inject` (PreToolUse → Read|Edit|Write|Bash)
**Purpose**: Pattern-match tool calls to observability skills, inject relevant guidance

```
1. Parse tool_input:
   - Read/Edit/Write → extract file_path
   - Bash → extract command

2. Load skill manifest (skill-manifest.json):
   - Pre-compiled regex patterns for pathPatterns, bashPatterns, importPatterns
   - Loaded from ~/.opentrace/plugin/generated/ (or embedded fallback)

3. Match skills:
   - Path matching: glob→regex against file_path (full path, basename, suffix)
   - Bash matching: regex against command string
   - Import matching: regex against file content (for Read/Edit/Write)

4. Dedup:
   - Check <tmpdir>/opentrace-<sessionId>-seen-skills.d/ for already-injected skills
   - Atomic claim with O_EXCL flag
   - Apply priority ranking (higher priority = inject first)

5. Inject:
   - Load matched SKILL.md files
   - Enforce 18KB budget, max 3 skills per injection
   - Output as additionalContext
```

#### `pretooluse-error-context` (PreToolUse → Edit|Write)
**Purpose**: Inject production error data for the specific file being edited

```
1. Extract file_path from tool_input
2. Read .opentrace/plugin.json for server URL + token
3. Call GET /api/plugin/file-errors?path=<basename>&service=<detected>
   - Server queries code_entity_store for errors linked to this file
   - Returns: error list with fingerprint, exception class, count, last seen
4. If errors found, inject:
   "⚠ OpenTrace: This file has 3 production errors:
    1. NoMethodError in process_payment (line ~42) — 150 occurrences/24h
    2. TimeoutError in charge_card (line ~87) — 23 occurrences/24h
    Be aware of these when editing.
    Use opentrace errors(action:'detail', fingerprint:'abc') for full context."
5. If no errors or server unreachable → no output (exit 0 with empty JSON)
```

#### `posttooluse-validate` (PostToolUse → Write|Edit)
**Purpose**: Validate written code against known production failure patterns

```
1. Read the written/edited file path from tool_input
2. Match file against skill pathPatterns to find applicable validation rules
3. For each matched skill, run its validate regex rules:
   - pattern: regex to match against file content
   - message: error description
   - severity: error | recommended | warn
   - skipIfFileContains: skip if file already has this pattern
4. Error-severity violations → inject fix instructions as additionalContext
5. Recommended/warn → inject suggestions (non-blocking)
```

#### `posttooluse-deploy-check` (PostToolUse → Bash)
**Purpose**: Detect deploy commands and check for regressions

```
1. Check if Bash command matches deploy patterns:
   - git push (to main/master/production)
   - cap deploy, kamal deploy, kubectl apply
   - vercel --prod, fly deploy, railway up
   - docker push, helm upgrade
2. If not a deploy command → exit 0 (no output)
3. Call GET /api/plugin/deploy-status?since=5m
   - Server returns: error rate delta, new error groups, latency change
4. Inject deployment status:
   "OpenTrace deploy check: Error rate +15% in last 5 minutes.
    2 new error groups detected. Consider rolling back.
    Use opentrace deploys(action:'impact') for details."
```

#### `prompt-skill-inject` (UserPromptSubmit)
**Purpose**: Match user prompts to skills via keyword scoring

```
1. Read user prompt from stdin
2. Score against each skill's promptSignals:
   - phrases: +6 per exact match (case-insensitive)
   - allOf groups: +4 per group where ALL terms match
   - anyOf: +1 per hit, capped at +2
   - noneOf: hard suppress (score → -∞)
3. Filter skills with score >= minScore (default 6)
4. Dedup against already-injected skills
5. Inject top 2 matched skills (8KB budget)
```

---

## 6. Skills Design

### 6.1 Skill Frontmatter Schema

```yaml
---
name: error-investigation
description: "Guide for investigating production errors using OpenTrace"
summary: "Use OpenTrace to investigate errors: list → detail → investigate → code risk"
metadata:
  priority: 7
  pathPatterns:
    - 'app/controllers/**'
    - 'src/**/*.ts'
    - 'internal/**/*.go'
  bashPatterns:
    - '\b(error|exception|crash|bug)\b'
    - '\brake\s+test\b'
    - '\bgo\s+test\b'
  importPatterns:
    - "sentry"
    - "@sentry/*"
    - "bugsnag"
  promptSignals:
    phrases:
      - "production error"
      - "investigate error"
      - "error in production"
      - "debugging production"
    allOf:
      - [error, production]
      - [bug, deploy]
    minScore: 6
  validate:
    - pattern: "rescue\\s+Exception"
      message: "Catching bare Exception hides errors from OpenTrace error tracking"
      severity: "recommended"
    - pattern: "rescue\\s*=>"
      message: "Ruby rescue without exception class — errors won't be properly grouped"
      severity: "warn"
---
```

### 6.2 Skill Content Structure

Each skill follows this pattern:

```markdown
# Error Investigation

## When to use
- User mentions a production error or exception
- Editing a file that has known production errors
- After a deploy shows increased error rates

## Workflow
1. List active errors: `opentrace errors(action: "list", status: "unresolved")`
2. Get error detail: `opentrace errors(action: "detail", fingerprint: "...")`
3. Investigate root cause: `opentrace errors(action: "investigate", fingerprint: "...")`
4. Check code risk: `opentrace code(action: "risk", files: ["path/to/file"])`
5. Check if related to recent deploy: `opentrace deploys(action: "impact")`

## Key patterns
- Always check `suggested_tools` in responses — follow them
- Use `errors(action: "impact")` to assess user/revenue impact
- Cross-reference with `logs(action: "trace", trace_id: "...")` for full request flow
- After fixing, use `code(action: "gen_suggest")` to generate regression tests

## Common pitfalls
- Don't resolve errors without confirming the fix is deployed
- Check error *trends* not just current count — a decreasing error may be self-healing
- Multiple errors with the same root cause share a trace — investigate the earliest one
```

### 6.3 All Skills (11 skills)

| Skill | Priority | Triggers On | Purpose |
|-------|----------|-------------|---------|
| `error-investigation` | 7 | Source file edits, "error" in prompts | Error diagnosis workflow |
| `trace-analysis` | 6 | Performance-related files, "slow"/"latency" in prompts | Trace and span analysis |
| `deploy-safety` | 8 | Deploy commands, CI config edits | Pre/post deploy validation |
| `log-search` | 5 | Log config files, "log"/"search" in prompts | Log search and correlation |
| `database-debugging` | 7 | Migration files, SQL files, "slow query" | Query analysis, lock detection |
| `service-health` | 6 | Docker/K8s configs, health check code | Service monitoring setup |
| `code-risk` | 7 | Any source file with known errors | Risk assessment from prod data |
| `incident-response` | 8 | "incident"/"outage"/"down" in prompts | Incident diagnosis playbook |
| `performance-optimization` | 6 | Perf-sensitive code, "optimize" | Endpoint performance tuning |
| `test-generation` | 5 | Test files, "test" in prompts | Generate tests from prod errors |
| `alerting-setup` | 5 | Monitoring config, "alert"/"watch" | Watch/alert configuration |

---

## 7. Knowledge Graph (`opentrace.md`)

A relational map of observability concepts and how OpenTrace tools connect them:

```markdown
# OpenTrace Knowledge Graph

## Legend
-> depends on | <-> integrates with | > contains | => use tool

## Core Concepts

### Logs > structured events from application code
  -> Services (every log belongs to a service)
  -> Traces (logs carry trace_id for correlation)
  => logs(action: "search") for full-text search
  => logs(action: "trace") for trace correlation
  => logs(action: "context") for surrounding entries

### Errors > grouped exceptions from log data
  -> Logs (errors are extracted from error-level logs)
  -> Code Entities (errors map to source files via stack traces)
  -> Deploys (error spikes correlate with deploy timing)
  => errors(action: "list") for active errors
  => errors(action: "investigate") for root cause analysis

### Traces > distributed request flows
  -> Spans (traces contain ordered spans)
  -> Services (traces cross service boundaries)
  -> Logs (trace_id links logs across services)
  => logs(action: "trace", trace_id: "...") to follow a trace

### Deploys > code releases to production
  -> Errors (deploys can introduce new errors)
  -> Services (deploys target specific services)
  -> Code Entities (deploy diff maps to changed files)
  => deploys(action: "impact") for post-deploy analysis

### Code Entities > source files, functions, classes tracked in production
  -> Errors (files accumulate error counts from stack traces)
  -> Services (entities belong to services)
  => code(action: "risk") for risk assessment
  => code(action: "annotate_file") for production annotations

### Watches > alert rules that monitor conditions
  -> Logs (watches evaluate against log data)
  -> Services (watches scope to services)
  => watches(action: "create") to set up alerts
  => watches(action: "investigate") to examine triggered alerts

### Database > connected external databases
  -> Queries (slow queries, locks, connections)
  -> Services (database serves specific services)
  => database(action: "queries") for slow query analysis
  => database(action: "locks") for lock investigation

## Investigation Workflows

### "Something is broken"
1. overview(action: "triage") → what needs attention
2. errors(action: "list") → active errors sorted by impact
3. errors(action: "investigate", fingerprint: "...") → root cause
4. logs(action: "trace", trace_id: "...") → full request flow
5. code(action: "annotate_file", path: "...") → production context for the fix

### "Is it safe to deploy?"
1. overview(action: "status") → current system health
2. errors(action: "list", status: "unresolved") → unresolved errors
3. code(action: "risk", files: [...changed files...]) → risk of changed code
4. deploys(action: "history") → recent deploy success rate

### "Why is it slow?"
1. analytics(action: "endpoints") → slow endpoints
2. logs(action: "performance", service: "...") → P95/P99 latency
3. database(action: "queries") → slow database queries
4. logs(action: "trace", trace_id: "...") → trace waterfall
```

---

## 8. Server-Side API Endpoints

The hooks call back to the OpenTrace server for live data. These are lightweight endpoints optimized for hook timeout budgets (< 2 seconds).

### 8.1 New Endpoints

| Endpoint | Method | Auth | Purpose | Consumer |
|----------|--------|------|---------|----------|
| `/api/plugin/binary` | GET | None | Download client binary (OS/arch auto-detect) | Connect script |
| `/api/plugin/version` | GET | None | Server version + min client version | Client auto-update check |
| `/api/plugin/context` | GET | Token | Session context: error count, last deploy, health | `session-start` hook |
| `/api/plugin/services` | GET | Token | Match detected service names to known services | `session-start` hook |
| `/api/plugin/file-errors` | GET | Token | Errors linked to a specific source file | `pretooluse-error-context` hook |
| `/api/plugin/deploy-status` | GET | Token | Post-deploy regression indicators | `posttooluse-deploy-check` hook |
| `/api/plugin/bundle` | GET | Token | Download plugin static assets as tar.gz | `opentrace plugin install` |

### 8.2 Authentication

Plugin endpoints use the same MCP token from `.opentrace/plugin.json`:
```
Authorization: Bearer <mcp_token>
```

Reuses existing `MCPTokenAuth` middleware — no new auth system needed.

### 8.3 Response Format

All plugin endpoints return minimal JSON optimized for hook injection:

```json
{
  "context": "OpenTrace: web-api has 3 active errors...",
  "services": ["web-api", "worker"],
  "details": { ... }
}
```

The `context` field is pre-formatted text ready for `additionalContext` injection — hooks don't need to format anything.

### 8.4 Endpoint Implementations

#### `GET /api/plugin/context`
```
Query params: ?services=web-api,worker
Response:
{
  "context": "OpenTrace: web-api has 3 active errors (2 critical). Last deploy: 2h ago.",
  "services": [
    {
      "name": "web-api",
      "active_errors": 3,
      "critical_errors": 2,
      "last_deploy": "2026-04-03T10:00:00Z",
      "health": "degraded"
    }
  ],
  "has_incidents": false
}
```
Implementation: Query ErrorGroupStore.List, DeployStore.GetRecent, HealthCheckStore.UptimeSummaries.

#### `GET /api/plugin/file-errors`
```
Query params: ?path=payments_controller.rb&service=web-api
Response:
{
  "context": "⚠ This file has 2 production errors:\n1. NoMethodError (150/24h)\n2. TimeoutError (23/24h)",
  "errors": [
    {
      "fingerprint": "abc123",
      "exception_class": "NoMethodError",
      "message": "undefined method 'process_payment'",
      "count_24h": 150,
      "last_seen": "2026-04-03T11:30:00Z"
    }
  ]
}
```
Implementation: Query CodeEntityStore by file path, join with ErrorGroupStore for details.

#### `GET /api/plugin/deploy-status`
```
Query params: ?since=5m
Response:
{
  "context": "Deploy check: Error rate +15%. 2 new error groups.",
  "error_rate_delta_pct": 15.2,
  "new_error_groups": 2,
  "latency_p95_delta_ms": 45
}
```
Implementation: Compare ErrorGroupStore counts before/after the since window.

---

## 9. Go Hook Package Design (`internal/hook/`)

### 9.1 Core Types

```go
package hook

// Input is the JSON received on stdin from Claude Code.
type Input struct {
    SessionID     string         `json:"session_id"`
    CWD           string         `json:"cwd"`
    HookEventName string         `json:"hook_event_name"`
    ToolName      string         `json:"tool_name,omitempty"`
    ToolInput     map[string]any `json:"tool_input,omitempty"`
    // UserPromptSubmit fields
    UserPrompt    string         `json:"user_prompt,omitempty"`
}

// Output is the JSON written to stdout for Claude Code.
type Output struct {
    HookSpecificOutput HookSpecificOutput `json:"hookSpecificOutput"`
}

type HookSpecificOutput struct {
    AdditionalContext string `json:"additionalContext,omitempty"`
}

// Config holds the per-project plugin configuration (.opentrace/plugin.json).
type Config struct {
    ServerURL string `json:"server_url"`
    Token     string `json:"token"`
    Version   string `json:"version,omitempty"`
}

// SkillManifest is the pre-compiled skill matching data.
type SkillManifest struct {
    Version int             `json:"version"`
    Skills  []SkillEntry    `json:"skills"`
}

type SkillEntry struct {
    Name             string           `json:"name"`
    Description      string           `json:"description"`
    Summary          string           `json:"summary"`
    Priority         int              `json:"priority"`
    PathPatterns     []string         `json:"pathPatterns"`
    PathRegexes      []string         `json:"pathRegexSources"`
    BashPatterns     []string         `json:"bashPatterns"`
    ImportPatterns   []string         `json:"importPatterns"`
    PromptSignals    *PromptSignals   `json:"promptSignals,omitempty"`
    ValidateRules    []ValidateRule   `json:"validate,omitempty"`
    SkillPath        string           `json:"skillPath"` // relative path to SKILL.md
}

type PromptSignals struct {
    Phrases  []string    `json:"phrases"`
    AllOf    [][]string  `json:"allOf"`
    AnyOf    []string    `json:"anyOf"`
    NoneOf   []string    `json:"noneOf"`
    MinScore int         `json:"minScore"`
}

type ValidateRule struct {
    Pattern            string `json:"pattern"`
    Message            string `json:"message"`
    Severity           string `json:"severity"` // error, recommended, warn
    SkipIfFileContains string `json:"skipIfFileContains,omitempty"`
}
```

### 9.2 Client

```go
// Client calls the OpenTrace server plugin API.
type Client struct {
    baseURL    string
    token      string
    httpClient *http.Client // 2-second timeout
}

func NewClientFromConfig(cwd string) (*Client, error) {
    // Walk up from cwd looking for .opentrace/plugin.json
    // Falls back to ~/.opentrace/plugin.json
}

func (c *Client) GetContext(services []string) (*ContextResponse, error)
func (c *Client) GetFileErrors(path, service string) (*FileErrorsResponse, error)
func (c *Client) GetDeployStatus(since string) (*DeployStatusResponse, error)
func (c *Client) GetServices(names []string) (*ServicesResponse, error)
```

### 9.3 Skill Matcher

```go
// MatchSkills returns skills matching the given tool invocation, ranked by priority.
func MatchSkills(manifest *SkillManifest, toolName string, toolInput map[string]any) []SkillMatch

// MatchPrompt returns skills matching the user prompt text, scored by promptSignals.
func MatchPrompt(manifest *SkillManifest, prompt string) []SkillMatch

type SkillMatch struct {
    Entry    SkillEntry
    Score    float64
    Reason   string // "path:app/controllers/**" or "bash:\\bdeploy\\b" or "prompt:phrases"
}
```

### 9.4 Dedup

```go
// ClaimSkill atomically claims a skill for a session. Returns true if this is the first claim.
func ClaimSkill(sessionID, skillName string) (bool, error)

// SeenSkills returns the set of already-injected skills for a session.
func SeenSkills(sessionID string) (map[string]bool, error)

// ResetHighPriority clears claims for skills with priority >= threshold (used after context compaction).
func ResetHighPriority(sessionID string, manifest *SkillManifest, threshold int) error

// Cleanup removes all temp files for a session.
func Cleanup(sessionID string) error
```

---

## 10. Implementation Plan

### Phase 1: Hook Infrastructure + Static Skills
**Goal**: Working `opentrace hook` subcommand that injects static skill content

1. Create `internal/hook/` package with core types (`hook.go`, `config.go`)
2. Create `internal/hook/manifest.go` — load and parse skill-manifest.json
3. Create `internal/hook/skill_matcher.go` — path, bash, import, prompt matching
4. Create `internal/hook/skill_matcher_test.go`
5. Create `internal/hook/dedup.go` — atomic file-based skill dedup
6. Create `internal/hook/dedup_test.go`
7. Create `internal/hook/profiler.go` — project framework detection
8. Create `internal/hook/profiler_test.go`
9. Create `internal/hook/session_start.go` — static session context injection
10. Create `internal/hook/pretooluse.go` — skill injection + error context
11. Create `internal/hook/posttooluse.go` — validation + deploy detection
12. Create `cmd/opentrace/hook.go` — CLI wiring
13. Update `cmd/opentrace/main.go` — add "hook" subcommand
14. Create `plugin/` directory with manifests, skills, knowledge graph
15. Write all 11 SKILL.md files
16. Write `opentrace.md` knowledge graph
17. Write `opentrace-session.md` thin session context
18. Build skill-manifest.json (Go generate or build step)
19. Embed `plugin/` via `//go:embed`
20. **Test**: Manual hook invocation via stdin pipe

### Phase 2: Live Data Integration
**Goal**: Hooks fetch real-time data from the OpenTrace server

21. Create `internal/hook/client.go` — HTTP client with 2s timeout
22. Create `internal/hook/client_test.go`
23. Create `internal/api/plugin_endpoint.go` — all plugin API endpoints
24. Implement `GET /api/plugin/context`
25. Implement `GET /api/plugin/services`
26. Implement `GET /api/plugin/file-errors`
27. Implement `GET /api/plugin/deploy-status`
28. Mount plugin endpoints in `internal/api/server.go`
29. Update `session-start` hook to call server API
30. Update `pretooluse-error-context` hook to call server API
31. Update `posttooluse-deploy-check` hook to call server API
32. **Test**: Full loop — edit file with prod errors, verify live context injection

### Phase 3: Binary Distribution + Automated Setup
**Goal**: `curl -s http://server/connect | bash` downloads binary, sets up MCP + plugin

33. Add `internal/api/plugin_binary.go` — `GET /api/plugin/binary` (GitHub Releases redirect)
34. Add `internal/api/plugin_version.go` — `GET /api/plugin/version` (server version info)
35. Add `internal/api/plugin_bundle.go` — `GET /api/plugin/bundle` (plugin assets tar.gz)
36. Add `cmd/opentrace/plugin.go` — `opentrace plugin install` (extract assets, write configs, register hooks)
37. Add `opentrace plugin update` subcommand (re-download binary + re-extract assets)
38. Add `opentrace plugin doctor` subcommand (verify binary version, server connectivity, hook registration)
39. Update `internal/routes/auth/connect_script.go` — add binary download + plugin install steps
40. Add GitHub Actions workflow for cross-platform binary builds + releases
41. **Test**: Fresh `curl | bash` end-to-end on macOS + Linux

### Phase 4: Specialist Agents + Commands
**Goal**: Domain-expert agents and quick-access slash commands

42. Write `agents/incident-responder.md`
43. Write `agents/performance-analyst.md`
44. Write `agents/reliability-engineer.md`
45. Write `commands/status.md`
46. Write `commands/investigate.md`
47. Write `commands/deploy-check.md`
48. Write `commands/connect.md`
49. **Test**: Verify agents and commands work in Claude Code

### Phase 5: Polish
**Goal**: Production-ready quality

50. Add `posttooluse-validate` skill validation rules to all skills
51. Add `prompt-skill-inject` prompt signal scoring
52. Add version compatibility check (hook binary vs server version, warn on drift)
53. Add auto-update hint in `session-start` hook (check once/day, inject notice if outdated)
54. Performance profiling: ensure all hooks complete < 2s
55. Add `--debug` flag to `opentrace hook` for troubleshooting
56. Add `OPENTRACE_PLUGIN_LOG_LEVEL=debug` env var for verbose hook output to stderr
57. **Test**: Full integration test suite

---

## 11. Technical Decisions

### 11.1 Plugin Lives Inside the OpenTrace Repo

**Why**: The plugin is tightly coupled to the MCP tool definitions and API endpoints. Keeping it in the same repo means:
- Skills reference exact tool signatures defined in `internal/mcp/tools/`
- Server endpoints and hook consumers are versioned together
- Single `go build` produces a binary with the embedded plugin
- No version drift between plugin and server

**How**: `plugin/` directory at repo root, embedded via `//go:embed plugin/*`

### 11.2 Hooks Are Go Subcommands (Not Node.js)

**Why**: OpenTrace is a Go project. Using Go for hooks eliminates the Node.js dependency:

| | Node.js (Vercel approach) | Go (our approach) |
|---|---|---|
| **Extra dependency** | Node.js 18+ required | None — single binary |
| **Startup time** | ~100ms | ~5ms |
| **Build pipeline** | tsup + TypeScript compilation | Already part of `go build` |
| **Code sharing** | Separate npm package | Same Go packages |
| **Distribution** | Extract .mjs files + package.json | Single binary download |
| **Type safety** | TypeScript (compile-time) | Go (compile-time) |
| **Cross-platform** | Node.js version differences | One binary per OS/arch |

**How**: `opentrace hook <event-name>` reads JSON from stdin, processes it, writes JSON to stdout. All hook logic lives in `internal/hook/`.

### 11.3 Binary Distribution: Server Serves Its Own Client

**Why**: The OpenTrace server and client hooks must be version-compatible. Having the server serve its own client binary guarantees this — when you upgrade the server, the next `opentrace plugin update` (or `curl | bash`) gets the matching binary.

**How it works**:

```
┌──────────────────────────────────────────────────────────┐
│ Build time (CI / go build)                               │
│                                                          │
│  GOOS=darwin GOARCH=arm64 go build → opentrace-darwin-arm64  │
│  GOOS=darwin GOARCH=amd64 go build → opentrace-darwin-amd64  │
│  GOOS=linux  GOARCH=amd64 go build → opentrace-linux-amd64   │
│  GOOS=linux  GOARCH=arm64 go build → opentrace-linux-arm64   │
│                                                          │
│  All binaries embedded into server binary via go:embed   │
│  OR placed in a well-known directory the server reads    │
└──────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────┐
│ Runtime                                                  │
│                                                          │
│  GET /api/plugin/binary?os=darwin&arch=arm64              │
│  → Server returns the matching pre-built binary          │
│  → Content-Disposition: attachment; filename=opentrace   │
│  → 10-20MB depending on platform                        │
└──────────────────────────────────────────────────────────┘
```

**Alternative: GitHub Releases redirect** — instead of embedding binaries in the server, the `/api/plugin/binary` endpoint can redirect to the GitHub Releases URL for the current version. This keeps the server binary small but requires internet access.

**Recommended**: Start with **GitHub Releases redirect** (simpler, smaller server binary). Add embedded binaries later if users need air-gapped installs.

### 11.3 Skills Are Static Markdown, Data Is Live API

**Why**: Skills provide *workflow knowledge* ("how to investigate an error") which is stable. Live *data* ("this file has 3 errors") comes from API calls in hooks. This separation means:
- Skills work offline (no server connection needed for basic guidance)
- Live data is always fresh (hooks call API on each trigger)
- Skills can be customized by users without breaking data flow

### 11.4 Dedup Strategy

Same proven approach as Vercel:
- Claim directory: `<tmpdir>/opentrace-<sessionId>-seen-skills.d/`
- Atomic file creation with `O_EXCL` for mutual exclusion
- Skills with priority >= 7 re-inject after context compaction
- Agent-scoped dedup to prevent cross-contamination

### 11.5 Graceful Degradation

Every hook has a fallback path when the server is unreachable:

| Hook | With Server | Without Server |
|------|-------------|----------------|
| `session-start` | Live error/deploy/health data | Static session context |
| `pretooluse-skill-inject` | Skills + live file error data | Skills only |
| `pretooluse-error-context` | File-specific prod errors | No output |
| `posttooluse-validate` | Skill validation rules | Skill validation rules (static) |
| `posttooluse-deploy-check` | Live regression detection | No output |
| `prompt-skill-inject` | Skills | Skills |

Server-independent hooks (skill-inject, validate, prompt-inject) always work. Server-dependent hooks (error-context, deploy-check) gracefully produce no output.

---

## 12. Connect Script Changes

The updated connect script (served at `GET /connect`) adds these steps after MCP token creation:

```bash
# --- Binary download ---
BIN_DIR="$HOME/.opentrace/bin"
BIN_PATH="$BIN_DIR/opentrace"
mkdir -p "$BIN_DIR"

# Detect platform
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
esac

# Download binary (skip if already installed and same version)
NEED_DOWNLOAD=true
if [ -x "$BIN_PATH" ]; then
  LOCAL_VER=$("$BIN_PATH" version 2>/dev/null | awk '{print $2}' || echo "unknown")
  REMOTE_VER=$(curl -sf "${SERVER}/api/plugin/version" | grep -o '"version":"[^"]*"' | cut -d'"' -f4 || echo "")
  if [ "$LOCAL_VER" = "$REMOTE_VER" ] && [ -n "$REMOTE_VER" ]; then
    NEED_DOWNLOAD=false
    echo "  ✓ opentrace binary up to date (${LOCAL_VER})"
  fi
fi

if [ "$NEED_DOWNLOAD" = true ]; then
  echo -n "  Downloading opentrace binary (${OS}/${ARCH})... "
  HTTP_CODE=$(curl -sf -w '%{http_code}' \
    "${SERVER}/api/plugin/binary?os=${OS}&arch=${ARCH}" \
    -o "$BIN_PATH" 2>/dev/null) || HTTP_CODE="000"

  if [ "$HTTP_CODE" = "200" ] && [ -s "$BIN_PATH" ]; then
    chmod +x "$BIN_PATH"
    echo "ok"
  else
    rm -f "$BIN_PATH"
    echo "skipped (binary not available for ${OS}/${ARCH})"
    echo ""
    echo "  MCP is connected. For plugin features (hooks, skills),"
    echo "  build from source: go install github.com/adham90/opentrace/cmd/opentrace@latest"
    echo ""
  fi
fi

# --- Plugin install ---
if [ -x "$BIN_PATH" ]; then
  echo -n "  Installing plugin... "
  "$BIN_PATH" plugin install \
    --server "${SERVER}" \
    --token "${TOKEN}" \
    --project "$(pwd)" 2>/dev/null && {
    echo "ok"
  } || {
    echo "failed"
    echo "  MCP is connected. Plugin setup failed — run manually:"
    echo "  $BIN_PATH plugin install --server ${SERVER} --token ${TOKEN}"
  }

  # Suggest adding to PATH if not already there
  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *)
      echo ""
      echo "  Add to your shell profile:"
      echo "    export PATH=\"$BIN_DIR:\$PATH\""
      echo ""
      ;;
  esac
fi
```

### What `opentrace plugin install` Does

```
opentrace plugin install --server <url> --token <token> [--project <path>]
```

1. **Extracts embedded plugin assets** to `~/.opentrace/plugin/`
   - Skills, agents, commands, knowledge graph, skill-manifest.json
   - Skips if already extracted and same version
2. **Writes global config** at `~/.opentrace/config.json`
   ```json
   { "server_url": "http://server:8080", "token": "mcp_...", "version": "0.1.0" }
   ```
3. **Writes per-project config** at `<project>/.opentrace/plugin.json`
   ```json
   { "server_url": "http://server:8080", "token": "mcp_..." }
   ```
4. **Writes hook registrations** into `<project>/.claude/settings.local.json`
   - All hooks point to `~/.opentrace/bin/opentrace hook <event>`
5. **Updates `.gitignore`** — adds `.opentrace/` and `.mcp.json`
6. **Prints summary**:
   ```
   ✓ Plugin assets extracted to ~/.opentrace/plugin/
   ✓ Hooks registered in .claude/settings.local.json
   ✓ Config written to .opentrace/plugin.json
   Ready — restart Claude Code to activate.
   ```

---

## 13. File-Level Implementation Map

### New Files to Create

| File | Language | Purpose |
|------|----------|---------|
| **Hook engine** | | |
| `internal/hook/hook.go` | Go | Core types, stdin parsing, handler dispatch |
| `internal/hook/config.go` | Go | Read .opentrace/plugin.json + ~/.opentrace/config.json |
| `internal/hook/session_start.go` | Go | SessionStart: profiler + context injection |
| `internal/hook/pretooluse.go` | Go | PreToolUse: skill-inject + error-context |
| `internal/hook/posttooluse.go` | Go | PostToolUse: validate + deploy-check |
| `internal/hook/prompt.go` | Go | UserPromptSubmit: prompt-skill-inject |
| `internal/hook/skill_matcher.go` | Go | Pattern matching (path, bash, import, prompt signals) |
| `internal/hook/skill_matcher_test.go` | Go | Matcher tests |
| `internal/hook/dedup.go` | Go | Atomic file-based skill deduplication |
| `internal/hook/dedup_test.go` | Go | Dedup tests |
| `internal/hook/client.go` | Go | HTTP client to OpenTrace server (2s timeout) |
| `internal/hook/client_test.go` | Go | Client tests |
| `internal/hook/profiler.go` | Go | Project framework detection (markers + deps) |
| `internal/hook/profiler_test.go` | Go | Profiler tests |
| `internal/hook/manifest.go` | Go | Load/parse skill-manifest.json |
| `internal/hook/manifest_test.go` | Go | Manifest tests |
| **CLI commands** | | |
| `cmd/opentrace/hook.go` | Go | `opentrace hook <event>` — dispatch to internal/hook |
| `cmd/opentrace/plugin.go` | Go | `opentrace plugin {install,update,doctor}` |
| **Server endpoints** | | |
| `internal/api/plugin_endpoint.go` | Go | GET /api/plugin/{context,services,file-errors,deploy-status} |
| `internal/api/plugin_binary.go` | Go | GET /api/plugin/binary — serve/redirect client binary |
| `internal/api/plugin_version.go` | Go | GET /api/plugin/version — server version info |
| `internal/api/plugin_bundle.go` | Go | GET /api/plugin/bundle — plugin assets tar.gz |
| **Plugin static assets** | | |
| `plugin/.plugin/plugin.json` | JSON | Claude Code plugin manifest |
| `plugin/opentrace.md` | Markdown | Observability knowledge graph |
| `plugin/opentrace-session.md` | Markdown | Thin session context template |
| `plugin/skills/*/SKILL.md` (×11) | Markdown | Skill definitions |
| `plugin/agents/*.md` (×3) | Markdown | Specialist agent definitions |
| `plugin/commands/*.md` (×4) | Markdown | Slash command definitions |
| `plugin/generated/skill-manifest.json` | JSON | Pre-compiled skill matching patterns |
| **CI/CD** | | |
| `.github/workflows/release.yml` | YAML | Cross-platform binary builds + GitHub Releases |

### Files to Modify

| File | Change |
|------|--------|
| `cmd/opentrace/main.go` | Add "hook" and "plugin" subcommands |
| `internal/api/server.go` | Mount `/api/plugin/*` endpoints |
| `internal/routes/auth/connect_script.go` | Add binary download + plugin install steps |
| `internal/version/version.go` | Expose version for binary compatibility checks |

---

## 14. Success Metrics

1. **One command setup**: `curl -s http://server/connect | bash` downloads binary, connects MCP, installs plugin — done
2. **Zero extra dependencies**: No Node.js, no npm, no Python — just the auto-downloaded `opentrace` binary
3. **Sub-2s hook latency**: All hooks complete within timeout budgets (Go binary startup ~5ms helps)
4. **Relevant injection rate**: > 80% of injected skills are relevant to what the user is doing
5. **Graceful degradation**: Plugin works with static skills even when server is unreachable
6. **No noise**: Skills inject only when pattern-matched, not on every tool call
7. **Seamless upgrades**: Server upgrade → `opentrace plugin update` → matching client binary + assets

---

## 15. End-to-End User Journey

```
1. User deploys OpenTrace server
   $ docker run -p 8080:8080 opentrace/opentrace

2. User connects from their project
   $ curl -s http://server:8080/connect | bash
     → Prompts for email/password
     → Creates .mcp.json (MCP works immediately)
     → Downloads ~/.opentrace/bin/opentrace (client binary)
     → Runs `opentrace plugin install` (extracts skills, registers hooks)
     → Prints "Done. Restart Claude Code."

3. User opens Claude Code
   → SessionStart hook fires
     → Detects: "This is a Rails app, likely service: web-api"
     → Calls server: "web-api has 3 errors, last deploy 2h ago"
     → Injects context into Claude's session

4. User asks Claude: "Fix the payment processing bug"
   → Claude reads app/controllers/payments_controller.rb
     → PreToolUse hook fires
       → Matches: error-investigation skill (source file edit)
       → Calls server: "This file has 2 prod errors (NoMethodError, TimeoutError)"
       → Injects skill + error data
     → Claude now knows about production errors AND the investigation workflow

5. User says "ship it" → Claude runs git push
   → PostToolUse hook fires
     → Detects: deploy command
     → Calls server: "Error rate +15%, 2 new error groups"
     → Injects warning: "Consider checking opentrace deploys(action:'impact')"
```

---

## 16. Open Questions

1. **Cursor support**: Should we add `.cursor-plugin/` manifest? Cursor has a different plugin API but similar hook model.
2. **Skill customization**: Should users be able to add custom skills (e.g., company-specific runbooks) in `~/.opentrace/plugin/skills/` that merge with built-in skills?
3. **Multi-server**: Can per-project `.opentrace/plugin.json` point to different servers (staging vs production)?
4. **Telemetry**: Should hooks report usage metrics back to the OpenTrace server for plugin analytics?
5. **Skill manifest generation**: Should this be a `go generate` step, a build-time script, or embedded as a Go map?
6. **Binary size**: Client binary is ~20MB. Should we build a stripped-down "client-only" binary (no server, no SQLite) for faster downloads? Or is the full binary fine since it's a one-time download?
7. **Windows support**: Should the connect script support PowerShell? Or is WSL sufficient for Windows users?
8. **Air-gapped installs**: Should the server embed client binaries (adds ~60MB) for environments without internet?
