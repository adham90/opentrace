# Plan 012: Investigation Memory

## Overview

OpenTrace learns from past MCP investigation sessions and guides future investigations through better tool suggestions, historical context, and watcher-linked recurrence tracking. Everything is automatic and invisible to the end user.

When a developer investigates an issue through Claude Code (or any MCP client), OpenTrace silently tracks the session, records what worked, links outcomes to watchers, and serves that knowledge to future investigations. The result: investigations that took 8 steps the first time take 3 the next time.

Investigation Memory connects to **every major subsystem** in OpenTrace — error groups, agent notes, runbooks, health checks, trends, request performance, database diagnostics, distributed traces, web analytics, audit logs, and period comparisons. The investigation session becomes the central hub that ties all of OpenTrace's data together into actionable institutional knowledge.

**Effort**: Large | **Impact**: Very High

**Vision**: OpenTrace evolves from a monitoring tool into a **production intelligence layer for AI coding agents** — bridging the gap between development and production so that every AI agent writes safer, better-informed code.

---

## Table of Contents

| File | Description |
|---|---|
| [01-session-lifecycle.md](01-session-lifecycle.md) | Identity chain, connection tracking, auth, concurrent sessions |
| [02-intent-outcome.md](02-intent-outcome.md) | Intent classification (3 layers), outcome inference, MCP Sampling |
| [03-subsystem-linking.md](03-subsystem-linking.md) | All 12 subsystem integrations (watchers, errors, notes, health checks, etc.) |
| [04-ranking-engine.md](04-ranking-engine.md) | Tool transitions, suggestion scoring, templates, cold start |
| [05-context-injection.md](05-context-injection.md) | Context assembly, similar sessions, dead-end warnings, escalation notes |
| [06-code-entities.md](06-code-entities.md) | Code entity registry, risk scores, MCP tools |
| [07-deploy-intel.md](07-deploy-intel.md) | Deploy tracking, impact measurement, webhooks |
| [08-dev-sessions.md](08-dev-sessions.md) | Development/review/deploy session tracking |
| [09-proactive-context.md](09-proactive-context.md) | MCP notifications, MCP resources, push alerts |
| [10-test-correlation.md](10-test-correlation.md) | Test-production links, test gaps, priority |
| [11-context-metatool.md](11-context-metatool.md) | The `context` meta-tool, cross-agent sharing |
| [12-implementation.md](12-implementation.md) | Phase order, testing plan, metrics, risks, expected behavior |
| [workflows.md](workflows.md) | All 11 example workflows |
| [migrations.md](migrations.md) | All SQL migrations consolidated |

---

## Goals

1. Make `suggested_tools` evidence-based using real investigation outcomes
2. Automatically link investigations to every relevant subsystem (watchers, error groups, health checks, agent notes, traces, trends, etc.)
3. Give MCP clients (Claude Code, Cursor, etc.) context from past investigations without any user action
4. Keep the request path fast — all AI-assisted enrichment happens via Claude Code (MCP Sampling), not server-side LLM

---

## Non-Goals

- No server-side LLM inference (Claude Code does all summarization via MCP Sampling)
- No auto-execution of tools (only better ordering/suggestions/context)
- No user-facing session management (sessions are fully implicit)
- No raw PII storage in session records

---

## How It Works (Simple Version)

```
Developer: "Payment errors are spiking"

Claude Code calls list_logs(level: "error", service: "payments")
    │
    ▼
OpenTrace:
    1. Authenticates via MCP token → knows WHO (user_id, role)
    2. This is an active MCP connection → knows WHICH session
    3. Records tool call with args, result, timing
    4. Checks: "Have we seen this pattern before?"
    5. Checks: related error groups, agent notes, trends, health checks
    6. Returns normal results + context from past investigations + related system data
    │
    ▼
Claude Code reads the context, skips dead ends, follows proven paths

Session ends (connection closes):
    → OpenTrace asks Claude Code (via MCP Sampling) to summarize
    → Summary + outcome stored for future use
    → Auto-creates agent notes for affected services
    → If a watcher was created, it's linked to this session
    → If an error group was resolved, it's linked to this session
    → Post-fix metrics snapshot captured for durability tracking

Next time same issue occurs:
    → Watcher fires / error reopens / health check fails
    → New investigation starts WITH full context
    → OpenTrace serves: "This happened before. Here's what worked.
       Previous fix was temporary. Try going deeper."
```

---

## System Integration Map

The investigation session is the central hub connecting every OpenTrace subsystem:

```
                         ┌──────────────────────┐
                         │     TRIGGERS          │
                         ├──────────────────────┤
                         │ Watch Alerts          │──┐
                         │ Health Check Failures │──┤
                         │ Error Group Reopens   │──┤
                         │ Trend Anomalies       │──┤
                         │ Manual (user starts)  │──┘
                         └──────────┬───────────┘
                                    │ triggers
                                    ▼
┌────────────────┐    ┌───────────────────────────────┐    ┌────────────────┐
│ ENRICHMENT     │    │    INVESTIGATION SESSION       │    │ OUTCOMES       │
│ (context in)   │    │                               │    │ (changes out)  │
├────────────────┤    │ • Identity (auth token)       │    ├────────────────┤
│ Agent Notes    │───▶│ • Tool sequence + timing      │───▶│ Session Summary│
│ Error Groups   │───▶│ • Intent classification       │───▶│ Agent Notes    │
│ Trends/Deploys │───▶│ • Outcome inference           │───▶│ Error Resolved │
│ Request Perf   │───▶│ • Recurrence tracking         │───▶│ Watcher Created│
│ Health Checks  │───▶│ • Subsystem links             │───▶│ Health Check   │
│ DB Diagnostics │───▶│                               │───▶│ Query Memory   │
│ Traces         │───▶│                               │───▶│ Perf Baseline  │
│ Web Analytics  │───▶│                               │───▶│ Audit Entry    │
│ Audit Log      │───▶│                               │───▶│ Metric Deltas  │
│ Runbook Results│───▶│                               │───▶│ Runbook Scores │
└────────────────┘    └───────────────────────────────┘    └────────────────┘
```

---

## Parts

### Part 1: Investigation Memory (files 01–05)
Session lifecycle, intent/outcome inference, subsystem linking, ranking engine, context injection. Makes OpenTrace smart about debugging.

### Part 2: AI Coding Agent Assistant (files 06–11)
Code entity registry, deploy intelligence, development session tracking, proactive context, test-production correlation, context meta-tool. Bridges the gap between development and production.

---

## Privacy and Data Retention

### What Is Stored

| Data | Stored? | Notes |
|---|---|---|
| Tool names | Yes | Always |
| Tool argument keys | Yes | Normalized (e.g., `level`, `service`) |
| Tool argument values | Truncated | Max 500 chars, no raw passwords/tokens |
| Result previews | Truncated | Max 500 chars |
| User ID / email | Yes | From auth token |
| Session summary | Yes | Generated by Claude Code |
| Query fingerprints | Yes | Normalized, no literal values |
| Raw user prompts | No | Never sent to server |
| Full conversation | No | Stays in Claude Code |

### Retention Policy

```go
const (
    DetailedRetention    = 30 * 24 * time.Hour   // 30 days: full activity records
    SessionRetention     = 180 * 24 * time.Hour   // 180 days: session summaries
    TransitionRetention  = 365 * 24 * time.Hour   // 1 year: aggregated transition stats
    QueryMemoryRetention = 365 * 24 * time.Hour   // 1 year: query investigation memory
    NoteRetention        = 0                       // never: auto-generated notes persist
)
```

### Access Scoping

Investigation context from past sessions is only served if the requesting user has access to the same data sources:

```go
func (ci *ContextInjector) filterByAccess(ctx context.Context, sessions []InvestigationSession, userID string) []InvestigationSession {
    accessible := ci.accessStore.GetUserDataSources(ctx, userID)
    accessibleSet := toSet(accessible)

    var filtered []InvestigationSession
    for _, sess := range sessions {
        if sess.PrimaryDatasourceID == nil || accessibleSet[*sess.PrimaryDatasourceID] {
            filtered = append(filtered, sess)
        }
    }
    return filtered
}
```
