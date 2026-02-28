# Plan 012: Investigation Memory

## Overview

OpenTrace learns from past MCP investigation sessions and guides future investigations through better tool suggestions, historical context, and watcher-linked recurrence tracking. Everything is automatic and invisible to the end user.

When a developer investigates an issue through Claude Code (or any MCP client), OpenTrace silently tracks the session, records what worked, links outcomes to watchers, and serves that knowledge to future investigations. The result: investigations that took 8 steps the first time take 3 the next time.

Investigation Memory connects to **every major subsystem** in OpenTrace — error groups, agent notes, runbooks, health checks, trends, request performance, database diagnostics, distributed traces, web analytics, audit logs, and period comparisons. The investigation session becomes the central hub that ties all of OpenTrace's data together into actionable institutional knowledge.

**Effort**: Large | **Impact**: Very High

**Vision**: OpenTrace evolves from a monitoring tool into a **production intelligence layer for AI coding agents** — bridging the gap between development and production so that every AI agent writes safer, better-informed code.

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

## Identity Chain

Every tool call is tagged with the full identity chain, derived from the authenticated MCP token and connection lifecycle:

```
Auth Token → user_id (WHO is investigating)
  → connection_id (WHICH MCP connection — one per Claude Code conversation)
  → session_id (WHICH investigation — usually 1:1 with connection)
  → step_index (WHICH step in the investigation)
```

### Why Auth Token Matters

The MCP token already resolves to a `User` record with `id`, `email`, `role`, `display_name`. This gives us:

- **Per-user investigation memory** — Developer A's patterns improve Developer A's suggestions
- **Team knowledge sharing** — Developer B benefits from Developer A's past resolutions
- **Access scoping** — only serve investigation context from data sources the user can access
- **Session continuity** — same user reconnecting can resume a session
- **Attribution** — "A teammate resolved this same issue 3 days ago"

### How It Maps to Existing Code

Currently in `wrapWithActivityLog()` (server.go:239-241):
```go
// Current: hardcoded, no real identity
sessionID := "mcp"
userID := ""
```

After this plan:
```go
// New: real identity from auth context
sessionID := sessionTracker.CurrentSession(ctx).ID
userID := authUserFromContext(ctx).ID
```

---

## Session Lifecycle

### Primary: MCP Connection = Session

One MCP connection = one investigation session. No timers. No heuristics.

**Stdio mode** (each Claude Code window spawns its own MCP process):
```
Process starts → Initialize() called → session created
  All tool calls → same session, step_index increments
Process exits → session closed, summary requested
```

**SSE/HTTP mode** (multiple clients connect to one server):
```
SSE connection opens → session created with connection_id
  All tool calls on this connection → same session
SSE connection closes → session closed, summary requested
```

```go
// On MCP initialize
func (s *MCPServer) onInitialize(ctx context.Context, req InitializeRequest) {
    user := authUserFromContext(ctx)  // from token validation

    s.session = s.sessionStore.Create(ctx, CreateInvestigationSessionParams{
        UserID:      user.ID,
        UserEmail:   user.Email,
        UserRole:    string(user.Role),
        ClientName:  req.ClientInfo.Name,     // "claude-code", "cursor", etc.
        ClientVersion: req.ClientInfo.Version,// "1.2.3"
        Workspace:   extractWorkspace(req),   // project path if available
        Transport:   s.transportType,         // "stdio" or "sse"
        Status:      "open",
    })
}

// On connection close / process exit
func (s *MCPServer) onShutdown(ctx context.Context) {
    s.requestSessionSummary(ctx)  // MCP Sampling (see Phase 4)
    s.finalizeSession(ctx)        // auto-notes, metric snapshots, linking
    s.sessionStore.Close(ctx, s.session.ID)
}
```

### Fallback: Ask Claude Code (for ambiguous cases)

If a user reconnects (same `user_id + workspace` within a short window), the server doesn't know if this is a continuation or a new investigation. Instead of guessing, ask:

```json
{
  "results": { "...normal tool results..." },
  "session_context": {
    "recent_session": {
      "session_id": "sess_abc",
      "summary": "Investigating payment timeout errors",
      "last_active": "5 minutes ago",
      "steps_completed": 4
    },
    "request": "A recent investigation session was found. If this is a continuation, include \"resume_session\": \"sess_abc\" in your next tool call. Otherwise, this will be treated as a new investigation."
  }
}
```

Claude Code reads this and either includes `resume_session` in the next call or doesn't. No user interaction needed — Claude Code decides based on what the user is asking.

```go
func (s *MCPServer) checkForResumableSession(ctx context.Context, user *User) *InvestigationSession {
    recent, err := s.sessionStore.FindRecent(ctx, FindRecentSessionParams{
        UserID:    user.ID,
        Workspace: s.workspace,
        MaxAge:    30 * time.Minute,
        Status:    "open",
    })
    if err != nil || recent == nil {
        return nil
    }
    return recent
}
```

### Multiple Concurrent Sessions

Each MCP connection gets its own `connection_id`. Multiple Claude Code windows from the same user create separate sessions that don't interfere:

```
Developer has 3 Claude Code windows open:

Window 1 (connection_id: conn_aaa):
  → Session sess_001: investigating payment errors

Window 2 (connection_id: conn_bbb):
  → Session sess_002: weekly user activity report

Window 3 (connection_id: conn_ccc):
  → Session sess_003: setting up monitoring for checkout

All three tracked independently via connection_id.
No mixing. No confusion.
```

**Cross-pollination** between concurrent sessions investigating the same topic:

```go
func (s *MCPServer) findParallelInvestigations(ctx context.Context, currentSession *InvestigationSession) []ParallelSessionInfo {
    // Find OTHER active sessions from ANY user that are investigating
    // similar services/error patterns — but only if the requesting user
    // has access to the same data sources
    return s.sessionStore.FindParallel(ctx, FindParallelParams{
        ExcludeSessionID: currentSession.ID,
        Service:          currentSession.PrimaryService,
        Intent:           currentSession.Intent,
        MaxAge:           1 * time.Hour,
        RequireAccess:    currentSession.UserID,  // access-scoped
    })
}
```

---

## Data Model

### Migration: `0000xx_investigation_memory.up.sql`

```sql
-- ============================================================
-- Investigation Sessions
-- ============================================================
CREATE TABLE IF NOT EXISTS investigation_sessions (
    id TEXT PRIMARY KEY,                              -- UUID

    -- Identity (from auth token)
    user_id TEXT NOT NULL DEFAULT '',                  -- links to users.id
    user_email TEXT NOT NULL DEFAULT '',               -- denormalized for quick display
    user_role TEXT NOT NULL DEFAULT '',                -- "admin" or "member"

    -- Client Info (from MCP initialize)
    client_name TEXT NOT NULL DEFAULT '',              -- "claude-code", "cursor", "continue"
    client_version TEXT NOT NULL DEFAULT '',           -- "1.2.3"
    workspace TEXT NOT NULL DEFAULT '',                -- project path / repo name
    transport TEXT NOT NULL DEFAULT '',                -- "stdio" or "sse"
    connection_id TEXT NOT NULL DEFAULT '',            -- unique per MCP connection

    -- Session Classification
    intent TEXT NOT NULL DEFAULT '',                   -- "investigation", "query", "configuration", "exploration"
    intent_detail TEXT NOT NULL DEFAULT '',            -- "payment timeout errors", "weekly report", etc.
    primary_service TEXT NOT NULL DEFAULT '',          -- dominant service being investigated
    primary_datasource_id INTEGER DEFAULT NULL,       -- dominant data source used

    -- Outcome
    status TEXT NOT NULL DEFAULT 'open'
        CHECK(status IN ('open', 'resolved', 'unresolved', 'abandoned')),
    summary TEXT NOT NULL DEFAULT '',                  -- Claude Code-generated summary
    root_cause TEXT NOT NULL DEFAULT '',               -- Claude Code-generated root cause
    fix_description TEXT NOT NULL DEFAULT '',          -- what was done to fix it

    -- Watcher Links
    created_watcher_ids TEXT NOT NULL DEFAULT '[]',    -- JSON array of watcher IDs created during session
    triggered_by_alert_id TEXT DEFAULT NULL,           -- alert that triggered this investigation
    triggered_by_watcher_id TEXT DEFAULT NULL,         -- watcher that triggered the alert

    -- Error Group Links
    resolved_error_group_ids TEXT NOT NULL DEFAULT '[]',  -- JSON array of error group fingerprints resolved
    investigated_error_fingerprints TEXT NOT NULL DEFAULT '[]', -- error fingerprints viewed via error_detail

    -- Health Check Links
    created_healthcheck_ids TEXT NOT NULL DEFAULT '[]',  -- JSON array of health check IDs created
    triggered_by_healthcheck_id TEXT DEFAULT NULL,       -- health check that triggered investigation

    -- Agent Note Links
    created_note_ids TEXT NOT NULL DEFAULT '[]',         -- JSON array of note IDs created during session
    auto_note_ids TEXT NOT NULL DEFAULT '[]',            -- JSON array of auto-generated note IDs on session close

    -- Runbook Links
    runbooks_executed TEXT NOT NULL DEFAULT '[]',        -- JSON array: [{"name": "slow_database", "step": 2}]

    -- Database Diagnostic Links
    explained_queries TEXT NOT NULL DEFAULT '[]',        -- JSON array of query fingerprints from explain_query
    killed_queries TEXT NOT NULL DEFAULT '[]',           -- JSON array of PIDs from kill_query

    -- Trace Links
    trace_ids TEXT NOT NULL DEFAULT '[]',                -- JSON array of distributed trace IDs followed

    -- Deploy Correlation
    correlated_deploy TEXT NOT NULL DEFAULT '',          -- deploy marker hash if deploy happened near investigation start

    -- Metrics Snapshots
    pre_investigation_snapshot TEXT NOT NULL DEFAULT '{}',   -- JSON: metric values at session start
    post_investigation_snapshot TEXT NOT NULL DEFAULT '{}',  -- JSON: metric values at session end

    -- Metrics
    total_steps INTEGER NOT NULL DEFAULT 0,
    total_errors INTEGER NOT NULL DEFAULT 0,
    tool_sequence TEXT NOT NULL DEFAULT '[]',          -- JSON array: ["list_logs", "query_datasource", ...]
    tool_fingerprint TEXT NOT NULL DEFAULT '',         -- pipe-separated: "list_logs|query_datasource|..."
    arg_signature TEXT NOT NULL DEFAULT '',            -- normalized key patterns for similarity matching

    -- Timing
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_activity_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at TEXT DEFAULT NULL,
    duration_seconds INTEGER NOT NULL DEFAULT 0,

    -- Recurrence Tracking
    recurrence_group TEXT DEFAULT NULL,                -- groups sessions investigating the same recurring issue
    recurrence_count INTEGER NOT NULL DEFAULT 0,      -- which occurrence this is (1st, 2nd, 3rd...)
    previous_session_id TEXT DEFAULT NULL,             -- links to the previous investigation of same issue
    fix_durability_seconds INTEGER DEFAULT NULL        -- how long the previous fix lasted before recurrence
);

CREATE INDEX idx_inv_sessions_user ON investigation_sessions(user_id, started_at DESC);
CREATE INDEX idx_inv_sessions_status ON investigation_sessions(status, started_at DESC);
CREATE INDEX idx_inv_sessions_intent ON investigation_sessions(intent, intent_detail);
CREATE INDEX idx_inv_sessions_service ON investigation_sessions(primary_service);
CREATE INDEX idx_inv_sessions_connection ON investigation_sessions(connection_id);
CREATE INDEX idx_inv_sessions_watcher ON investigation_sessions(triggered_by_watcher_id);
CREATE INDEX idx_inv_sessions_healthcheck ON investigation_sessions(triggered_by_healthcheck_id);
CREATE INDEX idx_inv_sessions_recurrence ON investigation_sessions(recurrence_group);
CREATE INDEX idx_inv_sessions_fingerprint ON investigation_sessions(tool_fingerprint);
CREATE INDEX idx_inv_sessions_deploy ON investigation_sessions(correlated_deploy);

-- ============================================================
-- Enhanced MCP Activity (extends existing mcp_activity table)
-- ============================================================
ALTER TABLE mcp_activity ADD COLUMN investigation_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_activity ADD COLUMN step_index INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mcp_activity ADD COLUMN context TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_activity ADD COLUMN was_suggested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mcp_activity ADD COLUMN suggestion_rank INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mcp_activity ADD COLUMN followed_by TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_mcp_activity_inv_session
    ON mcp_activity(investigation_session_id, step_index);

-- ============================================================
-- Tool Transition Stats (pre-computed for fast ranking)
-- ============================================================
CREATE TABLE IF NOT EXISTS tool_transitions (
    from_tool TEXT NOT NULL,
    to_tool TEXT NOT NULL,
    intent TEXT NOT NULL DEFAULT '',
    total_count INTEGER NOT NULL DEFAULT 0,
    resolved_count INTEGER NOT NULL DEFAULT 0,          -- transitions in resolved sessions
    abandoned_count INTEGER NOT NULL DEFAULT 0,         -- transitions in abandoned sessions
    avg_duration_ms INTEGER NOT NULL DEFAULT 0,
    last_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (from_tool, to_tool, intent)
);

-- ============================================================
-- Workflow Templates (golden paths for cold start)
-- ============================================================
CREATE TABLE IF NOT EXISTS workflow_templates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    intent TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',                      -- "Connection Pool Investigation"
    step_order INTEGER NOT NULL,
    tool_name TEXT NOT NULL,
    args_hint TEXT NOT NULL DEFAULT '{}',               -- JSON: suggested args
    source TEXT NOT NULL DEFAULT 'curated'
        CHECK(source IN ('curated', 'learned')),
    resolved_session_count INTEGER NOT NULL DEFAULT 0,  -- how many resolved sessions match this path
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_workflow_templates_intent ON workflow_templates(intent, step_order);

-- ============================================================
-- Query Memory (fingerprints from explain_query investigations)
-- ============================================================
CREATE TABLE IF NOT EXISTS query_memory (
    fingerprint TEXT PRIMARY KEY,                       -- normalized query fingerprint
    last_investigation_session_id TEXT NOT NULL DEFAULT '',
    investigation_count INTEGER NOT NULL DEFAULT 0,
    last_root_cause TEXT NOT NULL DEFAULT '',           -- "missing index on users.email"
    last_fix TEXT NOT NULL DEFAULT '',                  -- "CREATE INDEX idx_users_email ON users(email)"
    avg_duration_before_ms INTEGER DEFAULT NULL,
    avg_duration_after_ms INTEGER DEFAULT NULL,         -- if post-fix snapshot shows improvement
    first_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- ============================================================
-- Runbook Effectiveness
-- ============================================================
CREATE TABLE IF NOT EXISTS runbook_effectiveness (
    runbook_name TEXT PRIMARY KEY,
    total_executions INTEGER NOT NULL DEFAULT 0,
    resolved_sessions INTEGER NOT NULL DEFAULT 0,       -- sessions that resolved after running this runbook
    abandoned_sessions INTEGER NOT NULL DEFAULT 0,
    avg_steps_after INTEGER NOT NULL DEFAULT 0,         -- average additional steps after runbook
    avg_session_duration_seconds INTEGER NOT NULL DEFAULT 0,
    last_executed_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

### Go Models

```go
// InvestigationSession represents a complete MCP investigation session.
type InvestigationSession struct {
    ID                   string    `json:"id"`

    // Identity
    UserID               string    `json:"user_id"`
    UserEmail            string    `json:"user_email"`
    UserRole             string    `json:"user_role"`

    // Client
    ClientName           string    `json:"client_name"`
    ClientVersion        string    `json:"client_version"`
    Workspace            string    `json:"workspace"`
    Transport            string    `json:"transport"`
    ConnectionID         string    `json:"connection_id"`

    // Classification
    Intent               string    `json:"intent"`
    IntentDetail         string    `json:"intent_detail"`
    PrimaryService       string    `json:"primary_service"`
    PrimaryDatasourceID  *int      `json:"primary_datasource_id,omitempty"`

    // Outcome
    Status               string    `json:"status"`
    Summary              string    `json:"summary"`
    RootCause            string    `json:"root_cause"`
    FixDescription       string    `json:"fix_description"`

    // Watcher Links
    CreatedWatcherIDs    []string  `json:"created_watcher_ids"`
    TriggeredByAlertID   *string   `json:"triggered_by_alert_id,omitempty"`
    TriggeredByWatcherID *string   `json:"triggered_by_watcher_id,omitempty"`

    // Error Group Links
    ResolvedErrorGroupIDs         []string `json:"resolved_error_group_ids"`
    InvestigatedErrorFingerprints []string `json:"investigated_error_fingerprints"`

    // Health Check Links
    CreatedHealthcheckIDs    []string `json:"created_healthcheck_ids"`
    TriggeredByHealthcheckID *string  `json:"triggered_by_healthcheck_id,omitempty"`

    // Agent Note Links
    CreatedNoteIDs []string `json:"created_note_ids"`
    AutoNoteIDs    []string `json:"auto_note_ids"`

    // Runbook Links
    RunbooksExecuted []RunbookExecution `json:"runbooks_executed"`

    // Database Diagnostic Links
    ExplainedQueries []string `json:"explained_queries"`
    KilledQueries    []string `json:"killed_queries"`

    // Trace Links
    TraceIDs []string `json:"trace_ids"`

    // Deploy Correlation
    CorrelatedDeploy string `json:"correlated_deploy,omitempty"`

    // Metrics Snapshots
    PreInvestigationSnapshot  map[string]float64 `json:"pre_investigation_snapshot,omitempty"`
    PostInvestigationSnapshot map[string]float64 `json:"post_investigation_snapshot,omitempty"`

    // Metrics
    TotalSteps           int       `json:"total_steps"`
    TotalErrors          int       `json:"total_errors"`
    ToolSequence         []string  `json:"tool_sequence"`
    ToolFingerprint      string    `json:"tool_fingerprint"`
    ArgSignature         string    `json:"arg_signature"`

    // Timing
    StartedAt            time.Time `json:"started_at"`
    LastActivityAt       time.Time `json:"last_activity_at"`
    EndedAt              *time.Time `json:"ended_at,omitempty"`
    DurationSeconds      int       `json:"duration_seconds"`

    // Recurrence
    RecurrenceGroup      *string   `json:"recurrence_group,omitempty"`
    RecurrenceCount      int       `json:"recurrence_count"`
    PreviousSessionID    *string   `json:"previous_session_id,omitempty"`
    FixDurabilitySeconds *int      `json:"fix_durability_seconds,omitempty"`
}

type RunbookExecution struct {
    Name     string `json:"name"`
    StepIndex int   `json:"step_index"`
}

type QueryMemory struct {
    Fingerprint              string  `json:"fingerprint"`
    LastInvestigationSession string  `json:"last_investigation_session_id"`
    InvestigationCount       int     `json:"investigation_count"`
    LastRootCause            string  `json:"last_root_cause"`
    LastFix                  string  `json:"last_fix"`
    AvgDurationBeforeMs      *int64  `json:"avg_duration_before_ms,omitempty"`
    AvgDurationAfterMs       *int64  `json:"avg_duration_after_ms,omitempty"`
}

type RunbookEffectiveness struct {
    RunbookName         string  `json:"runbook_name"`
    TotalExecutions     int     `json:"total_executions"`
    ResolvedSessions    int     `json:"resolved_sessions"`
    AbandonedSessions   int     `json:"abandoned_sessions"`
    ResolutionRate      float64 `json:"resolution_rate"`
    AvgStepsAfter       int     `json:"avg_steps_after"`
    AvgDurationSeconds  int     `json:"avg_duration_seconds"`
}
```

---

## Subsystem Integrations

### Integration 1: Watcher Alerts (Watch System)

**Trigger:** Watcher alert fires → investigation starts with full history.
**Link out:** Watcher created during investigation → linked to session.

#### Auto-Link on Watcher Creation

```go
func createWatchHandler(ws store.WatchStore, st *SessionTracker) server.ToolHandlerFunc {
    return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        watch, err := ws.Create(ctx, params)

        if session := st.CurrentSession(ctx); session != nil {
            st.LinkWatcher(ctx, session.ID, watch.ID)
        }

        return result, nil
    }
}
```

#### Auto-Link on Alert Investigation

```go
func (st *SessionTracker) checkAlertTrigger(ctx context.Context, session *InvestigationSession, toolName string, args map[string]any) {
    if alertID, ok := args["alert_id"].(string); ok {
        alert, _ := st.watchStore.GetAlert(ctx, alertID)
        if alert != nil {
            session.TriggeredByAlertID = &alertID
            session.TriggeredByWatcherID = &alert.WatchID

            prevSession := st.sessionStore.FindByCreatedWatcher(ctx, alert.WatchID)
            if prevSession != nil {
                session.PreviousSessionID = &prevSession.ID
                session.RecurrenceGroup = prevSession.RecurrenceGroup
                if session.RecurrenceGroup == nil {
                    group := alert.WatchID
                    session.RecurrenceGroup = &group
                    prevSession.RecurrenceGroup = &group
                }
                session.RecurrenceCount = prevSession.RecurrenceCount + 1

                if prevSession.EndedAt != nil {
                    durability := int(time.Since(*prevSession.EndedAt).Seconds())
                    session.FixDurabilitySeconds = &durability
                }
            }
        }
    }
}
```

#### Recurrence Chain

```
Session 1 (Feb 24):
  → Investigates payment errors
  → Creates watcher "payment-pool-saturation" (watch_789)
  → recurrence_group: NULL (first time)

        ↓ watcher fires after 3 days

Session 2 (Feb 27):
  → triggered_by_watcher_id: "watch_789"
  → previous_session_id: "sess_001"
  → recurrence_group: "watch_789"
  → recurrence_count: 2
  → fix_durability_seconds: 259200 (3 days)

        ↓ watcher fires after 1 day

Session 3 (Feb 28):
  → recurrence_count: 3
  → fix_durability_seconds: 86400 (1 day)
  → Context: "3rd occurrence. Fixes lasting shorter each time."
```

---

### Integration 2: Error Groups (Sentry-lite)

**Trigger:** Error group reopens → treated as recurrence, same as watcher alert.
**Link out:** `resolve_error` / `ignore_error` called → linked to session.

#### Auto-Link on Error Investigation

When `error_detail` or `investigate_error` is called, record which error fingerprints are being investigated:

```go
func (st *SessionTracker) trackErrorInvestigation(ctx context.Context, session *InvestigationSession, toolName string, args map[string]any) {
    if toolName == "error_detail" || toolName == "investigate_error" {
        if fp, ok := args["fingerprint"].(string); ok {
            session.InvestigatedErrorFingerprints = appendUnique(session.InvestigatedErrorFingerprints, fp)
        }
    }
}
```

#### Auto-Link on Error Resolution

When `resolve_error` is called during a session:

```go
func resolveErrorHandler(egs store.ErrorGroupStore, st *SessionTracker) server.ToolHandlerFunc {
    return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        args := request.GetArguments()
        fingerprint := args["fingerprint"].(string)

        err := egs.UpdateStatus(ctx, fingerprint, store.ErrorGroupResolved)

        if session := st.CurrentSession(ctx); session != nil {
            session.ResolvedErrorGroupIDs = appendUnique(session.ResolvedErrorGroupIDs, fingerprint)
            // This is a strong resolution signal
        }

        return result, nil
    }
}
```

#### Error Group Recurrence

When an error group that was previously `resolved` gets new events and reopens to `unresolved`:

```go
func (st *SessionTracker) checkErrorRecurrence(ctx context.Context, session *InvestigationSession, fingerprint string) {
    // Find the session that resolved this error group
    prevSession, _ := st.sessionStore.FindByResolvedError(ctx, fingerprint)
    if prevSession == nil {
        return
    }

    // This is a recurrence — same error came back
    session.PreviousSessionID = &prevSession.ID
    recGroup := "error:" + fingerprint
    session.RecurrenceGroup = &recGroup
    session.RecurrenceCount = prevSession.RecurrenceCount + 1

    if prevSession.EndedAt != nil {
        durability := int(time.Since(*prevSession.EndedAt).Seconds())
        session.FixDurabilitySeconds = &durability
    }
}
```

#### What Claude Code Sees

```json
{
  "investigation_context": {
    "error_history": {
      "fingerprint": "abc123",
      "previously_resolved": true,
      "resolved_by": "teammate",
      "resolved_date": "2026-02-25",
      "resolution_summary": "Fixed N+1 query in OrdersController#index by adding includes(:line_items)",
      "times_reopened": 2,
      "note": "This error was resolved twice before but keeps coming back. Previous fixes were query-level. Consider a structural fix."
    }
  }
}
```

---

### Integration 3: Agent Notes (Persistent Memory)

**Enrichment:** Existing notes for investigated service/endpoint served as context.
**Link out:** Auto-create notes from resolved investigations.

#### Surface Existing Notes During Investigation

When the session's `primary_service` is identified, fetch existing agent notes:

```go
func (ci *ContextInjector) getRelevantNotes(ctx context.Context, session *InvestigationSession) []AgentNote {
    notes := []AgentNote{}

    if session.PrimaryService != "" {
        serviceNotes, _ := ci.noteStore.GetByEntity(ctx, "service", session.PrimaryService)
        notes = append(notes, serviceNotes...)
    }

    // Also get notes for any error fingerprints being investigated
    for _, fp := range session.InvestigatedErrorFingerprints {
        errorNotes, _ := ci.noteStore.GetByEntity(ctx, "error", fp)
        notes = append(notes, errorNotes...)
    }

    // Get notes for any queries being explained
    for _, qfp := range session.ExplainedQueries {
        queryNotes, _ := ci.noteStore.GetByEntity(ctx, "query", qfp)
        notes = append(notes, queryNotes...)
    }

    return notes
}
```

#### Auto-Create Notes From Resolved Sessions

When a session resolves with a summary, automatically create an agent note:

```go
func (st *SessionTracker) createAutoNotes(ctx context.Context, session *InvestigationSession) {
    if session.Status != "resolved" || session.Summary == "" {
        return
    }

    // Create note on the primary service
    if session.PrimaryService != "" {
        note, _ := st.noteStore.Create(ctx, store.CreateNoteParams{
            EntityType: "service",
            EntityID:   session.PrimaryService,
            Content:    fmt.Sprintf("[Investigation %s] %s", session.StartedAt.Format("2006-01-02"), session.Summary),
            Source:     "investigation_memory",
            SessionID:  session.ID,
        })
        if note != nil {
            session.AutoNoteIDs = append(session.AutoNoteIDs, note.ID)
        }
    }

    // Create note on resolved error groups
    for _, fp := range session.ResolvedErrorGroupIDs {
        note, _ := st.noteStore.Create(ctx, store.CreateNoteParams{
            EntityType: "error",
            EntityID:   fp,
            Content:    fmt.Sprintf("Resolved: %s. Root cause: %s", session.Summary, session.RootCause),
            Source:     "investigation_memory",
            SessionID:  session.ID,
        })
        if note != nil {
            session.AutoNoteIDs = append(session.AutoNoteIDs, note.ID)
        }
    }
}
```

#### What Claude Code Sees

```json
{
  "investigation_context": {
    "relevant_notes": [
      {
        "entity": "service:payments",
        "content": "[Investigation 2026-02-25] Connection pool exhaustion from batch import job",
        "created_by": "investigation_memory",
        "session_id": "sess_001"
      },
      {
        "entity": "query:SELECT * FROM orders WHERE...",
        "content": "N+1 query — add .includes(:line_items) to fix",
        "created_by": "user_42"
      }
    ]
  }
}
```

---

### Integration 4: Runbooks

**Link in:** Runbook execution recorded as part of session.
**Link out:** Runbook effectiveness tracked across sessions.

#### Track Runbook Execution

When `runbook` tool is called, record it:

```go
func runbookHandler(deps Deps, st *SessionTracker) server.ToolHandlerFunc {
    return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        args := request.GetArguments()
        name := args["playbook"].(string) // "slow_database", "error_spike", etc.

        if session := st.CurrentSession(ctx); session != nil {
            session.RunbooksExecuted = append(session.RunbooksExecuted, RunbookExecution{
                Name:      name,
                StepIndex: session.TotalSteps,
            })
        }

        // ... execute runbook normally ...
        return result, nil
    }
}
```

#### Track Runbook Effectiveness

On session close, update runbook stats:

```go
func (st *SessionTracker) updateRunbookEffectiveness(ctx context.Context, session *InvestigationSession) {
    for _, rb := range session.RunbooksExecuted {
        stepsAfterRunbook := session.TotalSteps - rb.StepIndex

        st.runbookStore.UpdateEffectiveness(ctx, UpdateRunbookParams{
            Name:            rb.Name,
            SessionResolved: session.Status == "resolved",
            SessionAbandoned: session.Status == "abandoned",
            StepsAfter:      stepsAfterRunbook,
            SessionDuration: session.DurationSeconds,
        })
    }
}
```

#### Auto-Suggest Runbooks

When an investigation starts, suggest runbooks with proven effectiveness:

```go
func (ci *ContextInjector) suggestRunbook(ctx context.Context, session *InvestigationSession) *RunbookSuggestion {
    if session.TotalSteps > 0 {
        return nil // only suggest at the start
    }

    // Find runbooks with high resolution rate for this intent
    effective, _ := ci.runbookStore.GetMostEffective(ctx, session.Intent)
    if effective == nil || effective.ResolutionRate() < 0.5 {
        return nil
    }

    return &RunbookSuggestion{
        Name:           effective.RunbookName,
        ResolutionRate: effective.ResolutionRate(),
        AvgSteps:       effective.AvgStepsAfter,
        Reason:         fmt.Sprintf("This runbook resolved %d%% of similar investigations", int(effective.ResolutionRate()*100)),
    }
}
```

#### What Claude Code Sees

```json
{
  "investigation_context": {
    "runbook_suggestion": {
      "name": "connection_exhaustion",
      "resolution_rate": 0.78,
      "avg_steps_after": 2,
      "reason": "This runbook resolved 78% of similar investigations in an average of 2 additional steps"
    }
  }
}
```

---

### Integration 5: Health Checks (Uptime Monitoring)

**Trigger:** Health check goes `down` or `degraded` → investigation trigger (same as watcher).
**Link out:** `create_healthcheck` during investigation → linked to session.

#### Health Check as Investigation Trigger

Same pattern as watcher alerts:

```go
func (st *SessionTracker) checkHealthCheckTrigger(ctx context.Context, session *InvestigationSession, toolName string, args map[string]any) {
    if toolName == "uptime_status" || toolName == "list_healthchecks" {
        // Check if any health check is currently down
        checks, _ := st.healthCheckStore.List(ctx, store.ListHealthCheckParams{})
        for _, hc := range checks {
            if hc.Status == "down" || hc.Status == "degraded" {
                // This investigation might be triggered by a health check failure
                session.TriggeredByHealthcheckID = &hc.ID

                // Find previous sessions triggered by this health check
                prevSession := st.sessionStore.FindByHealthCheck(ctx, hc.ID)
                if prevSession != nil {
                    session.PreviousSessionID = &prevSession.ID
                    recGroup := "healthcheck:" + hc.ID
                    session.RecurrenceGroup = &recGroup
                    session.RecurrenceCount = prevSession.RecurrenceCount + 1
                }
                break
            }
        }
    }
}
```

#### Link Health Check Creation

```go
func createHealthCheckHandler(hcs store.HealthCheckStore, st *SessionTracker) server.ToolHandlerFunc {
    return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        hc, err := hcs.Create(ctx, params)

        if session := st.CurrentSession(ctx); session != nil {
            session.CreatedHealthcheckIDs = appendUnique(session.CreatedHealthcheckIDs, hc.ID)
            // Health check creation = resolution signal (same as watcher)
        }

        return result, nil
    }
}
```

#### What Claude Code Sees

```json
{
  "investigation_context": {
    "health_check_history": {
      "endpoint": "https://api.example.com/payments/health",
      "current_status": "down",
      "downtime_started": "15 minutes ago",
      "previous_outages": [
        {
          "date": "2026-02-20",
          "duration": "45 minutes",
          "investigation_summary": "Database connection pool exhaustion",
          "fix": "Restarted worker processes"
        }
      ]
    }
  }
}
```

---

### Integration 6: Trends & Deploy Markers

**Enrichment:** Trend data shows metric changes leading up to investigation.
**Enrichment:** Deploy markers correlate investigations with code changes.

#### Capture Pre-Investigation Metrics

On session start, snapshot current metrics for the primary service:

```go
func (st *SessionTracker) capturePreSnapshot(ctx context.Context, session *InvestigationSession) {
    if session.PrimaryService == "" {
        return
    }

    snapshot := map[string]float64{}

    // Get current metrics from trends
    errorRate, _ := st.trendStore.GetLatestBucket(ctx, session.PrimaryService, "error_rate")
    if errorRate != nil {
        snapshot["error_rate"] = errorRate.Value
    }

    avgResponse, _ := st.trendStore.GetLatestBucket(ctx, session.PrimaryService, "avg_response_ms")
    if avgResponse != nil {
        snapshot["avg_response_ms"] = avgResponse.Value
    }

    session.PreInvestigationSnapshot = snapshot
}
```

#### Correlate With Deploys

Check if a deploy happened shortly before the investigation started:

```go
func (st *SessionTracker) correlateDeploy(ctx context.Context, session *InvestigationSession) {
    // Look for deploy markers within 30 minutes before the session started
    deploys, _ := st.trendStore.GetDeployMarkers(ctx, DeployMarkerParams{
        After:  session.StartedAt.Add(-30 * time.Minute),
        Before: session.StartedAt,
    })

    if len(deploys) > 0 {
        // Most recent deploy is the likely culprit
        session.CorrelatedDeploy = deploys[len(deploys)-1].CommitHash
    }
}
```

#### Post-Fix Validation

On session close, capture another snapshot and compare:

```go
func (st *SessionTracker) capturePostSnapshot(ctx context.Context, session *InvestigationSession) {
    if session.PrimaryService == "" || session.Status != "resolved" {
        return
    }

    snapshot := map[string]float64{}
    errorRate, _ := st.trendStore.GetLatestBucket(ctx, session.PrimaryService, "error_rate")
    if errorRate != nil {
        snapshot["error_rate"] = errorRate.Value
    }

    session.PostInvestigationSnapshot = snapshot
}
```

#### What Claude Code Sees

```json
{
  "investigation_context": {
    "trend_context": {
      "error_rate_trend": "Stable at 0.5% for 2 weeks, spiked to 8% at 10:00 AM today",
      "response_time_trend": "P95 increased from 120ms to 3,200ms over the last hour"
    },
    "deploy_correlation": {
      "deploy_hash": "abc123",
      "deploy_time": "9:45 AM (15 minutes before error spike)",
      "note": "A deploy occurred shortly before this issue started. Consider checking recent code changes."
    },
    "metric_improvement_from_last_fix": {
      "error_rate": { "before": 8.2, "after": 0.5, "change": "-94%" },
      "note": "Previous fix reduced error rate by 94%, but it reverted after 3 days"
    }
  }
}
```

---

### Integration 7: Request Performance

**Enrichment:** Performance baselines for investigated endpoints.
**Link out:** N+1 query patterns linked to investigations.

#### Track Performance Context

When `request_performance` is called, capture the current state as a reference:

```go
func (st *SessionTracker) trackPerformanceBaseline(ctx context.Context, session *InvestigationSession, args map[string]any) {
    if endpoint, ok := args["path"].(string); ok {
        // Store in the session's pre-investigation snapshot
        results, _ := st.logStore.SearchRequestSummaries(ctx, store.RequestSummarySearchParams{
            Path:  endpoint,
            Limit: 1,
            SortBy: "duration_ms",
        })
        if len(results) > 0 {
            session.PreInvestigationSnapshot["endpoint_avg_ms"] = results[0].DurationMs
            session.PreInvestigationSnapshot["endpoint_sql_count"] = float64(results[0].SQLCount)
        }
    }
}
```

#### What Claude Code Sees

```json
{
  "investigation_context": {
    "performance_context": {
      "endpoint": "/api/payments/checkout",
      "current": { "avg_ms": 3200, "sql_count": 47, "has_n_plus_one": true },
      "baseline": { "avg_ms": 120, "sql_count": 5 },
      "previous_investigation": {
        "date": "2026-02-20",
        "summary": "N+1 query in OrdersController#index — added includes(:line_items)",
        "fix_held": true
      }
    }
  }
}
```

---

### Integration 8: Database Diagnostics

**Link out:** Query fingerprints from `explain_query` stored in query memory.
**Link out:** `kill_query` PIDs recorded as resolution actions.

#### Build Query Memory

When `explain_query` is called during an investigation:

```go
func explainQueryHandler(deps Deps, st *SessionTracker) server.ToolHandlerFunc {
    return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        args := request.GetArguments()
        query := args["query"].(string)
        fingerprint := normalizeQueryFingerprint(query)

        result, err := handler(ctx, request) // execute normally

        if session := st.CurrentSession(ctx); session != nil {
            session.ExplainedQueries = appendUnique(session.ExplainedQueries, fingerprint)

            // Check if this query has been investigated before
            memory, _ := st.queryMemoryStore.Get(ctx, fingerprint)
            if memory != nil {
                // Inject past investigation context into the response
                addQueryMemoryToResult(result, memory)
            }
        }

        return result, nil
    }
}

// On session close, update query memory with findings
func (st *SessionTracker) updateQueryMemory(ctx context.Context, session *InvestigationSession) {
    if session.Status != "resolved" {
        return
    }

    for _, fp := range session.ExplainedQueries {
        st.queryMemoryStore.Upsert(ctx, UpsertQueryMemoryParams{
            Fingerprint:  fp,
            SessionID:    session.ID,
            RootCause:    session.RootCause,
            Fix:          session.FixDescription,
        })
    }
}
```

#### Track kill_query

```go
func killQueryHandler(deps Deps, st *SessionTracker) server.ToolHandlerFunc {
    return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        args := request.GetArguments()
        pid := fmt.Sprintf("%v", args["pid"])

        result, err := handler(ctx, request)

        if session := st.CurrentSession(ctx); session != nil {
            session.KilledQueries = appendUnique(session.KilledQueries, pid)
            // kill_query is a strong resolution action signal
        }

        return result, nil
    }
}
```

#### What Claude Code Sees

```json
{
  "investigation_context": {
    "query_memory": {
      "fingerprint": "SELECT * FROM orders WHERE user_id = ? ORDER BY created_at DESC",
      "previously_investigated": true,
      "investigation_count": 3,
      "last_root_cause": "Missing index on orders.user_id — sequential scan on 2M rows",
      "last_fix": "CREATE INDEX idx_orders_user_id ON orders(user_id)",
      "performance_after_fix": { "avg_ms_before": 450, "avg_ms_after": 12 }
    }
  }
}
```

---

### Integration 9: Distributed Traces

**Link out:** Trace IDs followed during investigation are recorded.
**Enrichment:** Past investigations linked to the same trace patterns.

#### Track Traces

When `trace_lookup` or `incident_timeline` is called:

```go
func (st *SessionTracker) trackTrace(ctx context.Context, session *InvestigationSession, toolName string, args map[string]any) {
    if toolName == "trace_lookup" || toolName == "incident_timeline" {
        if traceID, ok := args["trace_id"].(string); ok {
            session.TraceIDs = appendUnique(session.TraceIDs, traceID)
        }
    }
}
```

#### What Claude Code Sees

```json
{
  "investigation_context": {
    "trace_context": {
      "related_traces": [
        {
          "trace_id": "abc-123-def",
          "previously_investigated": true,
          "session_summary": "Timeout in payment-gateway service caused by downstream auth-service latency",
          "services_involved": ["payments", "auth", "gateway"]
        }
      ]
    }
  }
}
```

---

### Integration 10: Web Analytics & Traffic Heatmap

**Enrichment:** Traffic patterns provide investigation context.

#### Correlate Traffic Anomalies

```go
func (ci *ContextInjector) getTrafficContext(ctx context.Context, session *InvestigationSession) *TrafficContext {
    if session.PrimaryService == "" {
        return nil
    }

    // Get current traffic for the service's endpoints
    endpoints, _ := ci.analyticsStore.TopEndpoints(ctx, store.TopEndpointParams{
        Service: session.PrimaryService,
        Window:  "1h",
        Limit:   5,
    })

    // Check for unusual traffic patterns
    heatmap, _ := ci.analyticsStore.TrafficHeatmap(ctx, session.PrimaryService)

    return &TrafficContext{
        TopEndpoints: endpoints,
        Anomaly:      detectTrafficAnomaly(heatmap, time.Now()),
    }
}
```

#### What Claude Code Sees

```json
{
  "investigation_context": {
    "traffic_context": {
      "current_traffic": "2.3x normal for this hour on Fridays",
      "top_endpoints": [
        { "path": "/api/payments/checkout", "rpm": 450, "error_rate": 8.2 },
        { "path": "/api/payments/status", "rpm": 1200, "error_rate": 0.1 }
      ],
      "anomaly": "Traffic spike started at 9:45 AM, coinciding with the error spike. Possibly a traffic-triggered issue."
    }
  }
}
```

---

### Integration 11: Audit Log

**Link out:** Investigation sessions appear as audit entries.
**Enrichment:** Recent admin actions correlated with investigations.

#### Record Investigation in Audit Log

On session close:

```go
func (st *SessionTracker) recordAuditEntry(ctx context.Context, session *InvestigationSession) {
    if session.Intent != "investigation" || session.TotalSteps < 3 {
        return // don't audit trivial sessions
    }

    st.auditStore.Log(ctx, store.AuditEntry{
        Action:    "investigation_completed",
        UserID:    session.UserID,
        Details:   fmt.Sprintf("Session %s: %s (%s, %d steps, %s)", session.ID, session.Summary, session.Status, session.TotalSteps, humanDuration(session.DurationSeconds)),
        CreatedAt: time.Now(),
    })
}
```

#### Surface Recent Admin Actions

If admin actions happened near the investigation start, they might be relevant:

```go
func (ci *ContextInjector) getRecentAdminActions(ctx context.Context, session *InvestigationSession) []AuditEntry {
    // Admin actions within 1 hour before investigation started
    entries, _ := ci.auditStore.ListRecent(ctx, store.AuditListParams{
        After:  session.StartedAt.Add(-1 * time.Hour),
        Before: session.StartedAt,
        Limit:  5,
    })
    return entries
}
```

#### What Claude Code Sees

```json
{
  "investigation_context": {
    "recent_admin_actions": [
      {
        "action": "connector_updated",
        "user": "admin@example.com",
        "details": "Updated payments DB connector: changed max_connections from 20 to 10",
        "time": "30 minutes ago"
      }
    ],
    "note": "A connector configuration change happened 30 minutes before this issue started."
  }
}
```

---

### Integration 12: Compare Periods (Anomaly Detection)

**Link out:** Auto-compare before/after investigation on session close.
**Enrichment:** Serve comparison data to future sessions.

#### Auto-Compare on Session Close

```go
func (st *SessionTracker) autoCompare(ctx context.Context, session *InvestigationSession) {
    if session.Status != "resolved" || session.PrimaryService == "" {
        return
    }

    // Compare the hour before investigation to the hour after
    beforeStart := session.StartedAt.Add(-1 * time.Hour)
    afterEnd := time.Now()

    comparison, _ := st.logStore.ComparePeriods(ctx, store.ComparePeriodParams{
        Service:     session.PrimaryService,
        PeriodA:     beforeStart,
        PeriodAEnd:  session.StartedAt,
        PeriodB:     *session.EndedAt,
        PeriodBEnd:  afterEnd,
    })

    if comparison != nil {
        session.PostInvestigationSnapshot["error_rate_change"] = comparison.ErrorRateDelta
        session.PostInvestigationSnapshot["response_time_change"] = comparison.ResponseTimeDelta
    }
}
```

#### What Claude Code Sees (On Future Recurrence)

```json
{
  "investigation_context": {
    "previous_fix_impact": {
      "error_rate": { "before_fix": 8.2, "after_fix": 0.5, "improvement": "-94%" },
      "response_time_ms": { "before_fix": 3200, "after_fix": 120, "improvement": "-96%" },
      "measured_at": "2026-02-25",
      "note": "Previous fix was highly effective but temporary (reverted after 3 days)"
    }
  }
}
```

---

## Intent Classification

Intent is determined automatically without user involvement. Three layers, in priority order:

### Layer 1: Alert/Trigger-Based (highest confidence)

If the investigation starts because a watcher alert, health check failure, or error reopening triggered it:

```go
func (s *SessionTracker) classifyFromTrigger(session *InvestigationSession) (string, string) {
    if session.TriggeredByAlertID != nil {
        return "investigation", fmt.Sprintf("Alert: watcher %s", *session.TriggeredByWatcherID)
    }
    if session.TriggeredByHealthcheckID != nil {
        return "investigation", fmt.Sprintf("Health check %s is down", *session.TriggeredByHealthcheckID)
    }
    return "", "" // no trigger, try next layer
}
```

### Layer 2: Context Parameter (high confidence)

Every MCP tool gets an optional `context` parameter. Claude Code fills it in naturally because it's a helpful LLM that sees the parameter description:

```go
mcp.WithString("context", mcp.Description(
    "Brief description of what you are investigating or trying to accomplish. "+
    "Examples: 'investigating payment timeout errors', "+
    "'weekly activity report for stakeholders', "+
    "'setting up monitoring for checkout service'",
)),
```

Server-side classification from context:

```go
func classifyIntent(context string, toolName string) (intent string, detail string) {
    if context == "" {
        return classifyFromTool(toolName) // Layer 3 fallback
    }

    lower := strings.ToLower(context)
    detail = context

    investigationKeywords := []string{
        "bug", "error", "broken", "investigating", "failing", "incident",
        "outage", "slow", "timeout", "crash", "spike", "alert", "issue",
        "debug", "wrong", "problem", "regression",
    }
    for _, kw := range investigationKeywords {
        if strings.Contains(lower, kw) {
            return "investigation", detail
        }
    }

    queryKeywords := []string{
        "report", "summary", "show me", "list", "how many", "count",
        "activity", "usage", "stats", "overview",
    }
    for _, kw := range queryKeywords {
        if strings.Contains(lower, kw) {
            return "query", detail
        }
    }

    configKeywords := []string{
        "set up", "configure", "create", "monitor", "watch", "alert",
    }
    for _, kw := range configKeywords {
        if strings.Contains(lower, kw) {
            return "configuration", detail
        }
    }

    return "exploration", detail
}
```

### Layer 3: Tool Pattern Inference (fallback)

When no context is provided, infer from tool name and arguments:

```go
func classifyFromTool(toolName string, args map[string]any) (string, string) {
    switch {
    case toolName == "diagnose" || toolName == "triage_alerts" || toolName == "investigate_error":
        return "investigation", ""
    case toolName == "error_groups" || toolName == "error_detail" || toolName == "error_impact":
        return "investigation", ""
    case toolName == "runbook":
        return "investigation", ""
    case toolName == "watch" || toolName == "create_healthcheck":
        return "configuration", ""
    case toolName == "log_stats" || toolName == "system_overview" || toolName == "web_analytics":
        return "query", ""
    default:
        if level, ok := args["level"].(string); ok && level == "error" {
            return "investigation", ""
        }
        return "exploration", ""
    }
}
```

### How Intents Affect Behavior

| Intent | Session Tracking | Suggestion Ranking | Context Injection | Subsystem Linking |
|---|---|---|---|---|
| `investigation` | Full tracking, outcomes matter | Ranked from resolved investigations | Full context served | All subsystems linked |
| `query` | Tracked but lightweight | Basic tool suggestions | No investigation context | Minimal linking |
| `configuration` | Tracked, linked to investigations | Setup-oriented suggestions | Link to related investigation | Watcher/healthcheck linking only |
| `exploration` | Tracked minimally | Default static suggestions | Minimal context | No linking |

---

## Outcome Inference

Session outcomes are determined automatically. No user action needed.

### Automatic Outcome Signals

```go
type OutcomeSignal struct {
    Signal     string  // what happened
    Outcome    string  // inferred outcome
    Confidence float64 // 0.0–1.0
}

func (st *SessionTracker) inferOutcome(session *InvestigationSession) string {
    signals := []OutcomeSignal{}

    // Strong positive signals
    if len(session.CreatedWatcherIDs) > 0 {
        signals = append(signals, OutcomeSignal{"watcher_created", "resolved", 0.8})
    }
    if len(session.CreatedHealthcheckIDs) > 0 {
        signals = append(signals, OutcomeSignal{"healthcheck_created", "resolved", 0.8})
    }
    if len(session.ResolvedErrorGroupIDs) > 0 {
        signals = append(signals, OutcomeSignal{"error_resolved", "resolved", 0.9})
    }
    if len(session.KilledQueries) > 0 {
        signals = append(signals, OutcomeSignal{"query_killed", "resolved", 0.7})
    }
    if session.Summary != "" {
        signals = append(signals, OutcomeSignal{"summary_provided", "resolved", 0.9})
    }

    // Check last few tool calls
    successfulEnd := !session.lastToolWasError()
    if successfulEnd && session.TotalSteps >= 3 {
        signals = append(signals, OutcomeSignal{"successful_final_steps", "resolved", 0.6})
    }

    // Negative signals
    errorRate := float64(session.TotalErrors) / float64(max(session.TotalSteps, 1))
    if errorRate > 0.5 && session.TotalSteps >= 4 {
        signals = append(signals, OutcomeSignal{"high_error_rate", "unresolved", 0.7})
    }
    if session.lastNToolsAllErrors(3) {
        signals = append(signals, OutcomeSignal{"consecutive_failures", "unresolved", 0.8})
    }

    // Short sessions with no clear outcome
    if session.TotalSteps <= 2 && session.Intent == "query" {
        signals = append(signals, OutcomeSignal{"quick_query", "resolved", 0.9})
    }

    return weightedOutcome(signals)
}
```

### MCP Sampling: Ask Claude Code for Summary (Primary Source)

When the session ends (connection closes), use MCP Sampling to ask Claude Code's own LLM to summarize:

```go
func (s *MCPServer) requestSessionSummary(ctx context.Context) {
    if s.session == nil || s.session.TotalSteps == 0 {
        return
    }

    // Only request summaries for investigation sessions with enough steps
    if s.session.Intent != "investigation" || s.session.TotalSteps < 3 {
        return
    }

    toolList := strings.Join(s.session.ToolSequence, " → ")
    prompt := fmt.Sprintf(
        "You just completed an investigation using OpenTrace. "+
        "Tools used: %s\n\n"+
        "Please provide a brief JSON response with these fields:\n"+
        "- summary: one sentence describing what was investigated and found\n"+
        "- root_cause: the root cause if identified (empty string if not)\n"+
        "- fix_applied: what fix was applied if any (empty string if not)\n"+
        "- outcome: \"resolved\", \"unresolved\", or \"partial\"\n\n"+
        "Respond ONLY with valid JSON, no markdown.",
        toolList,
    )

    result, err := s.mcpServer.CreateSampling(ctx, mcp.CreateMessageRequest{
        Messages: []mcp.SamplingMessage{
            {Role: "user", Content: mcp.TextContent{Text: prompt}},
        },
        MaxTokens: 200,
    })

    if err != nil {
        s.session.Status = s.sessionTracker.inferOutcome(s.session)
        return
    }

    var summary SessionSummaryResponse
    if err := json.Unmarshal([]byte(result.Content.Text), &summary); err == nil {
        s.session.Summary = summary.Summary
        s.session.RootCause = summary.RootCause
        s.session.FixDescription = summary.FixApplied
        if summary.Outcome != "" {
            s.session.Status = summary.Outcome
        }
    }
}
```

**Fallback chain:**
1. MCP Sampling (best — Claude Code has full context) → use response
2. MCP Sampling not supported → automatic inference from signals
3. Very short session (1-2 steps, intent=query) → auto-mark as resolved, no summary needed

---

## Suggestion Ranking Engine

### How It Works

Every tool response currently includes static `suggested_tools`. This plan replaces static suggestions with evidence-based ranking from historical sessions.

```go
// Current: static suggestions hardcoded per tool
suggestions = append(suggestions,
    suggest("error_detail", "Investigate the most frequent error",
        map[string]any{"fingerprint": summaries[0].Fingerprint}))

// New: ranked suggestions from investigation memory
suggestions = s.rankingSvc.RankSuggestions(ctx, RankingRequest{
    CurrentTool:  toolName,
    Intent:       session.Intent,
    StepIndex:    session.TotalSteps,
    Service:      session.PrimaryService,
    SessionTools: session.ToolSequence,
    FallbackSuggestions: staticSuggestions,
})
```

### Ranking Algorithm

```go
func (r *RankingService) RankSuggestions(ctx context.Context, req RankingRequest) []ToolSuggestion {
    transitions, err := r.store.GetTransitions(ctx, GetTransitionsParams{
        FromTool:   req.CurrentTool,
        Intent:     req.Intent,
        MinSupport: 3,
        MaxAge:     90,
    })

    if err != nil || len(transitions) == 0 {
        // Check for effective runbooks at session start
        if req.StepIndex == 0 {
            runbook := r.suggestRunbook(ctx, req.Intent)
            if runbook != nil {
                return prependRunbookSuggestion(runbook, req.FallbackSuggestions)
            }
        }

        // Check curated templates
        templates := r.store.GetTemplateNextStep(ctx, req.Intent, req.StepIndex)
        if len(templates) > 0 {
            return templatesToSuggestions(templates)
        }

        return req.FallbackSuggestions
    }

    scored := make([]ScoredSuggestion, 0, len(transitions))
    for _, t := range transitions {
        score := r.computeScore(t)
        scored = append(scored, ScoredSuggestion{
            Tool:     t.ToTool,
            Score:    score,
            Source:   "learned",
            Evidence: fmt.Sprintf("Used in %d resolved sessions", t.ResolvedCount),
        })
    }

    sort.Slice(scored, func(i, j int) bool {
        return scored[i].Score > scored[j].Score
    })

    return scoredToSuggestions(scored, req)
}

func (r *RankingService) computeScore(t ToolTransition) float64 {
    if t.TotalCount == 0 {
        return 0
    }

    resolvedRate := float64(t.ResolvedCount) / float64(t.TotalCount)
    abandonPenalty := float64(t.AbandonedCount) / float64(t.TotalCount)
    score := resolvedRate - (abandonPenalty * 0.5)

    daysSinceLastSeen := time.Since(t.LastSeenAt).Hours() / 24
    timeDecay := math.Pow(0.5, daysSinceLastSeen/30.0)
    score *= timeDecay

    return score
}
```

### Negative Signal Tracking

```go
func (st *SessionTracker) recordTransition(ctx context.Context, session *InvestigationSession, newTool string) {
    if session.TotalSteps > 0 {
        prevTool := session.ToolSequence[len(session.ToolSequence)-1]
        st.activityStore.UpdateFollowedBy(ctx, session.ID, session.TotalSteps-1, newTool)
        st.transitionStore.IncrementTransition(ctx, prevTool, newTool, session.Intent)
    }
}

func (st *SessionTracker) finalizeTransitions(ctx context.Context, session *InvestigationSession) {
    if session.Status == "abandoned" && session.TotalSteps > 0 {
        lastTool := session.ToolSequence[len(session.ToolSequence)-1]
        st.transitionStore.IncrementAbandoned(ctx, lastTool, "", session.Intent)
    }
}
```

### Suggestion Acceptance Tracking

```go
func (st *SessionTracker) trackSuggestionAcceptance(ctx context.Context, session *InvestigationSession, toolName string) {
    if session.TotalSteps == 0 {
        return
    }

    prevSuggestions := session.LastSuggestions
    wasSuggested := false
    rank := 0
    for i, s := range prevSuggestions {
        if s.Tool == toolName {
            wasSuggested = true
            rank = i + 1
            break
        }
    }

    st.activityStore.SetSuggestionTracking(ctx, session.ID, session.TotalSteps, wasSuggested, rank)
}
```

---

## Context Injection (What Claude Code Receives)

The key output of this system. Every tool response is enriched with investigation context from past sessions AND related subsystem data.

### Full Enriched Response Example

```json
{
  "results": {
    "total": 47,
    "logs": [{ "id": 1234, "level": "error", "message": "Connection pool exhausted", "service": "payments" }]
  },
  "investigation_context": {
    "similar_past_sessions": [
      {
        "date": "2026-02-25",
        "investigated_by": "teammate",
        "summary": "Connection pool exhaustion caused by uncommitted transactions from batch import job",
        "root_cause": "Batch import job lacks transaction timeout",
        "fix_applied": "Killed stuck queries, increased pool size to 20",
        "outcome": "resolved",
        "steps": 5,
        "path": ["list_logs", "query_datasource", "get_log_entry", "query_datasource", "watch"]
      }
    ],
    "recurrence_info": {
      "is_recurring": true,
      "occurrence_number": 3,
      "previous_fixes": [
        { "fix": "Increased pool size to 20", "held_for": "3 days" },
        { "fix": "Increased pool size to 50", "held_for": "1 day" }
      ],
      "escalation_note": "Fixes are holding for less time each occurrence. Previous fixes were symptomatic (pool size increases). Root cause (transaction timeouts in batch import) has not been addressed."
    },
    "relevant_notes": [
      { "entity": "service:payments", "content": "Connection pool exhaustion from batch import", "source": "investigation_memory" }
    ],
    "error_history": {
      "fingerprint": "abc123",
      "previously_resolved": true,
      "times_reopened": 2,
      "resolution_summary": "Pool restart"
    },
    "deploy_correlation": {
      "deploy_hash": "abc123",
      "deploy_time": "15 minutes before error spike"
    },
    "traffic_context": {
      "current_traffic": "2.3x normal for this hour"
    },
    "recent_admin_actions": [
      { "action": "connector_updated", "details": "Changed max_connections from 20 to 10", "time": "30 minutes ago" }
    ],
    "runbook_suggestion": {
      "name": "connection_exhaustion",
      "resolution_rate": 0.78,
      "reason": "This runbook resolved 78% of similar investigations"
    },
    "dead_ends": [
      { "tool": "db_locks", "reason": "Tried in 2 past sessions, did not contribute to resolution" }
    ],
    "parallel_investigations": [
      { "active": true, "by": "teammate", "started": "10 minutes ago", "findings_so_far": "Found 47 idle-in-transaction connections" }
    ]
  },
  "suggested_tools": [
    {
      "tool": "query_datasource",
      "why": "This query identified the issue in 2 past sessions. Since previous pool size fixes were temporary, look for transaction leak patterns.",
      "args": { "id": 3, "query": "SELECT * FROM pg_stat_activity WHERE state = 'idle in transaction'" },
      "confidence": 0.85,
      "ranking_source": "learned",
      "resolved_sessions": 2
    }
  ]
}
```

### How Context Is Built

```go
func (ci *ContextInjector) BuildContext(ctx context.Context, session *InvestigationSession, toolName string, results map[string]any) map[string]any {
    if session.Intent != "investigation" {
        return results
    }

    ic := InvestigationContext{}

    // 1. Similar past sessions
    similar, _ := ci.sessionStore.FindSimilar(ctx, FindSimilarParams{
        Service:          session.PrimaryService,
        Intent:           session.Intent,
        ToolFingerprint:  session.ToolFingerprint,
        ExcludeSessionID: session.ID,
        UserAccessScope:  session.UserID,
        MaxResults:       3,
        MinSteps:         3,
        OnlyResolved:     true,
    })
    if len(similar) > 0 {
        ic.SimilarPastSessions = toSessionSummaries(similar, session.UserID)
    }

    // 2. Recurrence info (watcher-linked, error-linked, healthcheck-linked)
    if session.TriggeredByWatcherID != nil || session.TriggeredByHealthcheckID != nil {
        ic.RecurrenceInfo = ci.buildRecurrenceInfo(ctx, session)
    }
    // Also check error group recurrence
    for _, fp := range session.InvestigatedErrorFingerprints {
        if errorRecurrence := ci.checkErrorRecurrence(ctx, session, fp); errorRecurrence != nil {
            ic.ErrorHistory = errorRecurrence
        }
    }

    // 3. Agent notes for this service/errors
    ic.RelevantNotes = ci.getRelevantNotes(ctx, session)

    // 4. Deploy correlation
    if session.CorrelatedDeploy != "" {
        ic.DeployCorrelation = ci.buildDeployContext(ctx, session)
    }

    // 5. Traffic context
    ic.TrafficContext = ci.getTrafficContext(ctx, session)

    // 6. Recent admin actions
    ic.RecentAdminActions = ci.getRecentAdminActions(ctx, session)

    // 7. Runbook suggestion (first step only)
    if session.TotalSteps <= 1 {
        ic.RunbookSuggestion = ci.suggestRunbook(ctx, session)
    }

    // 8. Query memory for any queries being investigated
    ic.QueryMemory = ci.getQueryMemory(ctx, session)

    // 9. Performance context
    ic.PerformanceContext = ci.getPerformanceContext(ctx, session)

    // 10. Dead-end tools to avoid
    deadEnds, _ := ci.transitionStore.GetDeadEnds(ctx, session.Intent, session.PrimaryService)
    if len(deadEnds) > 0 {
        ic.DeadEnds = deadEnds
    }

    // 11. Parallel investigations
    parallel := ci.findParallelInvestigations(ctx, session)
    if len(parallel) > 0 {
        ic.ParallelInvestigations = parallel
    }

    // 12. Previous fix impact (for recurrences)
    if session.PreviousSessionID != nil {
        ic.PreviousFixImpact = ci.getFixImpact(ctx, *session.PreviousSessionID)
    }

    if !ic.IsEmpty() {
        results["investigation_context"] = ic
    }
    return results
}
```

### Escalation Note Generation (Deterministic)

```go
func (ci *ContextInjector) buildEscalationNote(recurrence *RecurrenceInfo) string {
    if recurrence.OccurrenceNumber <= 1 {
        return ""
    }

    fixes := recurrence.PreviousFixes
    if len(fixes) == 0 {
        return fmt.Sprintf("This issue has recurred %d times with no recorded fix.", recurrence.OccurrenceNumber)
    }

    isEscalating := len(fixes) >= 2
    for i := 1; i < len(fixes); i++ {
        if fixes[i].HeldForSeconds >= fixes[i-1].HeldForSeconds {
            isEscalating = false
            break
        }
    }

    if isEscalating {
        return fmt.Sprintf(
            "Fixes are holding for less time each occurrence. "+
            "Previous fixes were: %s. "+
            "Consider investigating the root cause rather than applying another symptomatic fix.",
            summarizeFixes(fixes),
        )
    }

    return fmt.Sprintf(
        "This issue has recurred %d times. Last fix: %s (held for %s).",
        recurrence.OccurrenceNumber,
        fixes[len(fixes)-1].Description,
        humanDuration(fixes[len(fixes)-1].HeldForSeconds),
    )
}
```

---

## Example Workflows

### Workflow 1: First-Time Investigation (No History)

```
Developer: "Payments are broken"
Claude Code connects to OpenTrace MCP

── Initialize ──────────────────────────────────────────────
  → Token validated → user_id: "user_42", role: "admin"
  → Session created: sess_001
  → Client: claude-code v1.5.0, workspace: /app/payments
  → Intent: unknown (no tool calls yet)

── Step 1: list_logs ───────────────────────────────────────
  Claude Code calls: list_logs(level: "error", service: "payments",
                               context: "investigating payment failures")

  OpenTrace:
    • Records step 1, intent classified: "investigation"
    • intent_detail: "investigating payment failures"
    • primary_service: "payments"
    • Checks all subsystems:
      - Agent notes for "payments" service → none yet
      - Recent deploys → deploy abc123 was 15 min ago
      - Traffic heatmap → 2.3x normal traffic
      - Admin actions → connector max_connections changed 30 min ago
    • No similar past sessions found
    • Returns: normal results + static suggestions + deploy/traffic context

  Response:
  {
    "results": { "logs": [...47 error logs...] },
    "investigation_context": {
      "deploy_correlation": {
        "deploy_hash": "abc123",
        "deploy_time": "15 minutes ago"
      },
      "traffic_context": { "current_traffic": "2.3x normal" },
      "recent_admin_actions": [
        { "action": "connector_updated", "details": "max_connections 20→10" }
      ],
      "runbook_suggestion": {
        "name": "error_spike",
        "resolution_rate": 0.65,
        "reason": "This runbook resolved 65% of similar investigations"
      }
    },
    "suggested_tools": [
      { "tool": "error_groups", "why": "See error patterns", "ranking_source": "static" }
    ]
  }

── Step 2: error_groups ────────────────────────────────────
  → Links error fingerprint "fp_abc" to session
  → error_history: first time seeing this fingerprint

── Step 3: query_datasource ────────────────────────────────
  → query: "SELECT * FROM pg_stat_activity WHERE state = 'idle in transaction'"
  → Finds 47 idle connections

── Step 4: explain_query ───────────────────────────────────
  → query fingerprint stored in query_memory
  → No prior memory for this query

── Step 5: resolve_error ───────────────────────────────────
  → Error group "fp_abc" marked resolved
  → session.resolved_error_group_ids: ["fp_abc"]
  → Strong resolution signal

── Step 6: watch ───────────────────────────────────────────
  → Watcher watch_789 created
  → session.created_watcher_ids: ["watch_789"]

── Connection closes ───────────────────────────────────────
  OpenTrace:
    1. MCP Sampling → Claude Code provides summary
    2. Auto-creates agent note on service "payments":
       "[2026-02-25] Connection pool exhaustion from batch import job"
    3. Auto-creates agent note on error "fp_abc":
       "Resolved: idle transactions from batch import"
    4. Updates query_memory for the explained query
    5. Captures post-investigation metrics snapshot
    6. Auto-compares before/after: error rate 8.2% → 0.5%
    7. Updates runbook effectiveness (error_spike runbook was suggested, session resolved)
    8. Records transitions for ranking engine
    9. Logs to audit trail
    10. Session finalized: resolved, 6 steps, 8 minutes
```

### Workflow 2: Recurring Issue (Watcher Fires, Full Context)

```
── 3 days later, watcher watch_789 fires ───────────────────
  Alert alert_555: "error_rate > 5 for payments"

── Developer B opens Claude Code ───────────────────────────
  "The payment alert fired"

── Step 1: triage_alerts ───────────────────────────────────
  Claude Code calls: triage_alerts(context: "payment alert fired")

  OpenTrace detects:
    • Alert alert_555 → watcher watch_789
    • watch_789 was created by sess_001
    • This is recurrence #2
    • Previous fix lasted 3 days

  OpenTrace enriches from ALL subsystems:
    • Agent notes: "Connection pool exhaustion from batch import job"
    • Error group: fp_abc was resolved, may have reopened
    • Query memory: "SELECT * FROM pg_stat_activity..." → previously found 47 idle connections
    • Deploy: no new deploy since last fix
    • Traffic: normal
    • Admin actions: none recent
    • Previous fix impact: error rate went from 8.2% → 0.5% but reverted

  Response:
  {
    "results": {
      "alerts": [{ "id": "alert_555", "summary": "error_rate > 5 for payments" }]
    },
    "investigation_context": {
      "recurrence_info": {
        "is_recurring": true,
        "occurrence_number": 2,
        "previous_fixes": [
          { "fix": "Killed stuck queries, created watcher", "held_for": "3 days" }
        ],
        "root_cause_from_last_time": "Batch import job lacks transaction timeout",
        "escalation_note": "Previous fix was symptomatic. Root cause identified but not fixed."
      },
      "relevant_notes": [
        { "entity": "service:payments", "content": "Connection pool exhaustion from batch import job" }
      ],
      "error_history": {
        "fingerprint": "fp_abc",
        "previously_resolved": true,
        "resolution_summary": "Idle transactions from batch import"
      },
      "query_memory": {
        "fingerprint": "SELECT * FROM pg_stat_activity...",
        "last_root_cause": "47 idle-in-transaction connections from batch import"
      },
      "previous_fix_impact": {
        "error_rate": { "before": 8.2, "after": 0.5, "improvement": "-94%" },
        "note": "Fix was effective but temporary (3 days)"
      }
    },
    "suggested_tools": [
      {
        "tool": "query_datasource",
        "why": "Check for idle-in-transaction connections. Previous investigation found 47 stuck connections from batch import. Root cause: missing transaction timeout.",
        "args": { "id": 3, "query": "SELECT * FROM pg_stat_activity WHERE state = 'idle in transaction'" },
        "confidence": 0.85,
        "ranking_source": "learned"
      }
    ]
  }

  Developer B's Claude Code:
    • Has NEVER seen this issue before
    • But knows: exact root cause, what was tried, what worked temporarily,
      why it came back, what query to run, and what to fix permanently
    • Skips ALL exploratory steps
    • Goes directly to fixing the batch import job's transaction timeout

── Result: 2-3 steps instead of 6 ─────────────────────────
```

### Workflow 3: Database Query Investigation With Memory

```
Developer: "This query is slow"
Claude Code calls: explain_query(query: "SELECT * FROM orders WHERE user_id = 42 ORDER BY created_at")

OpenTrace:
  • Fingerprint: "SELECT * FROM orders WHERE user_id = ? ORDER BY created_at"
  • Checks query_memory → FOUND:
    - Investigated 2 times before
    - Root cause: "Missing index on orders.user_id"
    - Fix: "CREATE INDEX idx_orders_user_id ON orders(user_id)"
    - Performance: 450ms → 12ms after fix last time

Response:
{
  "results": { "...explain plan..." },
  "investigation_context": {
    "query_memory": {
      "fingerprint": "SELECT * FROM orders WHERE user_id = ? ORDER BY created_at",
      "investigation_count": 2,
      "last_root_cause": "Missing index on orders.user_id — sequential scan on 2M rows",
      "last_fix": "CREATE INDEX idx_orders_user_id ON orders(user_id)",
      "performance_after_fix": { "avg_ms_before": 450, "avg_ms_after": 12 },
      "note": "This query has been investigated twice. If the index exists and it's still slow, the issue may be different this time."
    }
  }
}
```

### Workflow 4: Simple Query (Minimal Tracking)

```
Developer: "Show me user activity for last week"

── Step 1: log_stats(window: "7d", context: "weekly user activity summary")

  OpenTrace:
    • Intent: "query" (keyword: "summary")
    • Lightweight tracking — no subsystem linking
    • No investigation context injected
    • Returns: normal stats results

── Connection closes
    • Skip MCP Sampling (intent=query)
    • Auto-mark: resolved
    • No auto-notes, no metric snapshots
    • Session stored but excluded from investigation ranking
```

### Workflow 5: Concurrent Sessions With Cross-Pollination

```
── 10:00 AM ────────────────────────────────────────────────

Developer A: "Payment errors are spiking"
  → Session sess_010
  → list_logs → query_datasource → Finds 47 idle connections

── 10:02 AM ────────────────────────────────────────────────

Developer B: "Payments look broken"
  → Session sess_011
  → list_logs(level: "error", service: "payments")

  OpenTrace detects parallel investigation:

  Response:
  {
    "investigation_context": {
      "parallel_investigations": [{
        "active": true,
        "by": "teammate",
        "started": "2 minutes ago",
        "steps_completed": 2,
        "findings_so_far": "Found 47 idle-in-transaction connections via pg_stat_activity"
      }],
      "relevant_notes": [
        { "entity": "service:payments", "content": "Connection pool exhaustion from batch import job" }
      ]
    }
  }

  Developer B's Claude Code knows a teammate is already on it and what they found.
```

### Workflow 6: Health Check Failure Investigation

```
── Health check "payments-api" goes DOWN ───────────────────

Developer: "Payments API health check is failing"
  → list_healthchecks(context: "payments health check failing")

  OpenTrace:
    • Detects health check "payments-api" is down
    • triggered_by_healthcheck_id set
    • Checks for previous sessions triggered by same health check
    • Previous session 2 weeks ago: "SSL certificate expired on payments load balancer"

  Response:
  {
    "investigation_context": {
      "health_check_history": {
        "endpoint": "https://api.example.com/payments/health",
        "current_status": "down",
        "previous_outages": [{
          "date": "2026-02-14",
          "duration": "2 hours",
          "summary": "SSL certificate expired on payments load balancer",
          "fix": "Renewed certificate, added cert expiry watcher"
        }]
      }
    }
  }
```

### Workflow 7: Runbook-Driven Investigation

```
Developer: "Database is slow"
  → diagnose(context: "slow database queries")

  OpenTrace:
    • Intent: investigation
    • Checks runbook effectiveness:
      - "slow_database" runbook: 82% resolution rate, avg 3 steps after

  Response includes:
  {
    "investigation_context": {
      "runbook_suggestion": {
        "name": "slow_database",
        "resolution_rate": 0.82,
        "avg_steps_after": 3,
        "reason": "This runbook resolved 82% of slow database investigations"
      }
    }
  }

  Claude Code runs: runbook(playbook: "slow_database")
    → OpenTrace records: session.runbooks_executed = [{"name": "slow_database", "step": 1}]
    → Runbook runs db_query_stats, explain_query, db_activity, db_locks
    → Session resolves

  On close:
    → runbook_effectiveness for "slow_database": +1 resolved, avg 2 steps after
```

---

## Cold Start: Workflow Templates

New installations have zero investigation history. Ship curated templates:

```go
var defaultTemplates = []WorkflowTemplate{
    {Intent: "investigation", Name: "Error Spike", StepOrder: 1, ToolName: "diagnose"},
    {Intent: "investigation", Name: "Error Spike", StepOrder: 2, ToolName: "error_groups"},
    {Intent: "investigation", Name: "Error Spike", StepOrder: 3, ToolName: "error_detail"},
    {Intent: "investigation", Name: "Error Spike", StepOrder: 4, ToolName: "log_search"},

    {Intent: "investigation", Name: "Slow Database", StepOrder: 1, ToolName: "db_query_stats"},
    {Intent: "investigation", Name: "Slow Database", StepOrder: 2, ToolName: "explain_query"},
    {Intent: "investigation", Name: "Slow Database", StepOrder: 3, ToolName: "db_activity"},
    {Intent: "investigation", Name: "Slow Database", StepOrder: 4, ToolName: "db_locks"},

    {Intent: "investigation", Name: "General Triage", StepOrder: 1, ToolName: "triage_alerts"},
    {Intent: "investigation", Name: "General Triage", StepOrder: 2, ToolName: "diagnose"},
    {Intent: "investigation", Name: "General Triage", StepOrder: 3, ToolName: "error_groups"},

    {Intent: "investigation", Name: "Connection Exhaustion", StepOrder: 1, ToolName: "db_activity"},
    {Intent: "investigation", Name: "Connection Exhaustion", StepOrder: 2, ToolName: "connection_pool_stats"},
    {Intent: "investigation", Name: "Connection Exhaustion", StepOrder: 3, ToolName: "long_transactions"},
    {Intent: "investigation", Name: "Connection Exhaustion", StepOrder: 4, ToolName: "db_locks"},

    {Intent: "investigation", Name: "Performance Regression", StepOrder: 1, ToolName: "request_performance"},
    {Intent: "investigation", Name: "Performance Regression", StepOrder: 2, ToolName: "compare_periods"},
    {Intent: "investigation", Name: "Performance Regression", StepOrder: 3, ToolName: "db_query_stats"},
    {Intent: "investigation", Name: "Performance Regression", StepOrder: 4, ToolName: "explain_query"},
}
```

Templates are used as fallback when learned data has insufficient support. As real sessions accumulate, learned rankings gradually replace templates.

---

## Store Interfaces

```go
type InvestigationSessionStore interface {
    Create(ctx context.Context, params CreateInvestigationSessionParams) (*InvestigationSession, error)
    GetByID(ctx context.Context, id string) (*InvestigationSession, error)
    Close(ctx context.Context, id string) error
    Update(ctx context.Context, id string, params UpdateSessionParams) error

    // Session lookup
    FindRecent(ctx context.Context, params FindRecentSessionParams) (*InvestigationSession, error)
    FindByCreatedWatcher(ctx context.Context, watcherID string) (*InvestigationSession, error)
    FindByResolvedError(ctx context.Context, fingerprint string) (*InvestigationSession, error)
    FindByHealthCheck(ctx context.Context, healthCheckID string) (*InvestigationSession, error)
    FindSimilar(ctx context.Context, params FindSimilarParams) ([]InvestigationSession, error)
    FindParallel(ctx context.Context, params FindParallelParams) ([]InvestigationSession, error)

    // Watcher/error/healthcheck linking
    LinkWatcher(ctx context.Context, sessionID string, watcherID string) error
    LinkResolvedError(ctx context.Context, sessionID string, fingerprint string) error
    LinkHealthCheck(ctx context.Context, sessionID string, healthCheckID string) error

    // Listing / analytics
    List(ctx context.Context, params ListSessionParams) ([]InvestigationSession, error)
    ListCompletedSince(ctx context.Context, since time.Duration) ([]InvestigationSession, error)
    Stats(ctx context.Context) (*SessionStats, error)

    // Retention
    Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

type ToolTransitionStore interface {
    Upsert(ctx context.Context, params UpsertTransitionParams) error
    GetTransitions(ctx context.Context, params GetTransitionsParams) ([]ToolTransition, error)
    GetDeadEnds(ctx context.Context, intent string, service string) ([]DeadEnd, error)
    IncrementTransition(ctx context.Context, from, to, intent string) error
    IncrementAbandoned(ctx context.Context, from, to, intent string) error
}

type WorkflowTemplateStore interface {
    GetNextStep(ctx context.Context, intent string, stepIndex int) ([]WorkflowTemplate, error)
    Seed(ctx context.Context, templates []WorkflowTemplate) error
}

type QueryMemoryStore interface {
    Get(ctx context.Context, fingerprint string) (*QueryMemory, error)
    Upsert(ctx context.Context, params UpsertQueryMemoryParams) error
    ListBySession(ctx context.Context, sessionID string) ([]QueryMemory, error)
}

type RunbookEffectivenessStore interface {
    GetMostEffective(ctx context.Context, intent string) (*RunbookEffectiveness, error)
    UpdateEffectiveness(ctx context.Context, params UpdateRunbookParams) error
    List(ctx context.Context) ([]RunbookEffectiveness, error)
}
```

---

## New MCP Tool: `set_session_summary`

One tool for Claude Code to provide a summary mid-session (fallback when MCP Sampling is unavailable):

```go
mcp.NewTool("set_session_summary",
    mcp.WithDescription(
        "Provide a summary of the current investigation session. "+
        "Call this when you've completed an investigation or identified a root cause. "+
        "This helps future investigations of similar issues.",
    ),
    mcp.WithString("summary", mcp.Required(),
        mcp.Description("One sentence describing what was investigated and found")),
    mcp.WithString("root_cause",
        mcp.Description("The root cause if identified")),
    mcp.WithString("fix_applied",
        mcp.Description("What fix was applied, if any")),
    mcp.WithString("outcome",
        mcp.Description("Session outcome: resolved, unresolved, or partial")),
)
```

---

## Web API Endpoints

```
GET  /api/investigations/sessions              — list sessions with filters
GET  /api/investigations/sessions/{id}         — session detail with full tool sequence + all linked subsystems
GET  /api/investigations/effectiveness         — tool effectiveness by intent
GET  /api/investigations/workflow-paths         — most common successful paths
GET  /api/investigations/recurrence-groups      — recurring issue groups (watcher + error + healthcheck triggered)
GET  /api/investigations/stats                  — aggregate stats
GET  /api/investigations/query-memory           — investigated queries and their root causes
GET  /api/investigations/runbook-effectiveness  — runbook resolution rates
```

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
    DetailedRetention   = 30 * 24 * time.Hour   // 30 days: full activity records
    SessionRetention    = 180 * 24 * time.Hour   // 180 days: session summaries
    TransitionRetention = 365 * 24 * time.Hour   // 1 year: aggregated transition stats
    QueryMemoryRetention = 365 * 24 * time.Hour  // 1 year: query investigation memory
    NoteRetention        = 0                      // never: auto-generated notes persist
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

---

## Part 2: AI Coding Agent Assistant

Investigation Memory (Part 1) makes OpenTrace smart about debugging. This section extends OpenTrace into a **production intelligence layer for AI coding agents** — bridging the gap between development and production.

### The Problem

AI coding agents and production monitoring live in separate worlds:

```
WHAT THE AI AGENT KNOWS           WHAT OPENTRACE KNOWS
─────────────────────────         ────────────────────────
The code in the repo              Logs and errors
The current file being edited     Database performance
Git history                       Health checks
What the user asked               Traffic patterns
Test results                      Watchers and alerts
                                  Investigation history

              ← NO CONNECTION →
```

When Claude Code edits `orders_controller.rb`, it has no idea this file caused 3 production incidents last month. When OpenTrace shows a stack trace pointing to `payment_service.rb:47`, it has no idea what that code does or what recently changed.

**Bridging this gap turns OpenTrace from a monitoring tool into an AI coding agent's production memory.**

---

### New Capability 1: Code Entity Registry

Connect source code files, functions, classes, and endpoints to their production history. Populated automatically by parsing stack traces from error groups, endpoint paths from request summaries, and SQL from query stats.

#### Data Model

```sql
CREATE TABLE IF NOT EXISTS code_entities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL
        CHECK(entity_type IN ('file', 'function', 'class', 'endpoint', 'query')),
    entity_path TEXT NOT NULL,             -- "app/controllers/orders_controller.rb"
    entity_name TEXT NOT NULL DEFAULT '',   -- "OrdersController#index"
    service TEXT NOT NULL DEFAULT '',

    -- Production stats (rolled up from investigations, errors, request summaries)
    error_count_30d INTEGER NOT NULL DEFAULT 0,
    investigation_count INTEGER NOT NULL DEFAULT 0,
    last_incident_at TEXT DEFAULT NULL,
    last_incident_summary TEXT NOT NULL DEFAULT '',
    last_investigation_session_id TEXT DEFAULT NULL,
    avg_response_ms REAL DEFAULT NULL,
    p95_response_ms REAL DEFAULT NULL,
    has_n_plus_one INTEGER NOT NULL DEFAULT 0,

    -- Risk score
    risk_score REAL NOT NULL DEFAULT 0.0,      -- 0.0–1.0
    risk_factors TEXT NOT NULL DEFAULT '[]',    -- JSON: ["3 incidents in 30 days", "N+1 query"]

    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX idx_code_entities_lookup ON code_entities(entity_type, entity_path, entity_name);
CREATE INDEX idx_code_entities_service ON code_entities(service);
CREATE INDEX idx_code_entities_risk ON code_entities(risk_score DESC);
```

#### How It Gets Populated

No manual input needed. OpenTrace builds the registry automatically from data it already has:

```go
// 1. Parse stack traces from error groups → extract file paths, function names
func (r *CodeEntityPopulator) FromErrorGroup(eg *ErrorGroup) {
    for _, frame := range parseStackTrace(eg.Backtrace) {
        r.UpsertEntity(ctx, CodeEntity{
            EntityType: "file",
            EntityPath: frame.FilePath,     // "app/controllers/orders_controller.rb"
            EntityName: frame.FunctionName, // "OrdersController#index"
            Service:    eg.Service,
        })
        r.IncrementErrorCount(ctx, frame.FilePath, frame.FunctionName)
    }
}

// 2. Parse endpoint paths from request summaries → map to controllers
func (r *CodeEntityPopulator) FromRequestSummary(rs *RequestSummary) {
    r.UpsertEntity(ctx, CodeEntity{
        EntityType:    "endpoint",
        EntityPath:    rs.Path,           // "/api/orders"
        EntityName:    rs.Controller,     // "OrdersController#index"
        Service:       rs.Service,
        AvgResponseMs: &rs.DurationMs,
        HasNPlusOne:   rs.DuplicateQueries > 0,
    })
}

// 3. Link investigation sessions to code entities via errors investigated
func (r *CodeEntityPopulator) FromSession(session *InvestigationSession) {
    for _, fp := range session.InvestigatedErrorFingerprints {
        eg, _ := r.errorGroupStore.GetByFingerprint(ctx, fp)
        if eg != nil {
            for _, frame := range parseStackTrace(eg.Backtrace) {
                r.LinkInvestigation(ctx, frame.FilePath, session.ID)
            }
        }
    }
}
```

#### Risk Score Computation

```go
func computeRiskScore(entity *CodeEntity) float64 {
    score := 0.0
    factors := []string{}

    // Error frequency (0–0.4)
    if entity.ErrorCount30d > 10 {
        score += 0.4
        factors = append(factors, fmt.Sprintf("%d errors in 30 days", entity.ErrorCount30d))
    } else if entity.ErrorCount30d > 3 {
        score += 0.2
        factors = append(factors, fmt.Sprintf("%d errors in 30 days", entity.ErrorCount30d))
    }

    // Investigation frequency (0–0.3)
    if entity.InvestigationCount >= 3 {
        score += 0.3
        factors = append(factors, fmt.Sprintf("Investigated %d times", entity.InvestigationCount))
    } else if entity.InvestigationCount >= 1 {
        score += 0.15
    }

    // Performance issues (0–0.2)
    if entity.HasNPlusOne {
        score += 0.1
        factors = append(factors, "Has N+1 query pattern")
    }
    if entity.P95ResponseMs != nil && *entity.P95ResponseMs > 1000 {
        score += 0.1
        factors = append(factors, fmt.Sprintf("P95 response: %dms", int(*entity.P95ResponseMs)))
    }

    // Recency (0–0.1)
    if entity.LastIncidentAt != nil && time.Since(*entity.LastIncidentAt) < 7*24*time.Hour {
        score += 0.1
        factors = append(factors, "Incident in the last 7 days")
    }

    entity.RiskScore = min(score, 1.0)
    entity.RiskFactors = factors
    return entity.RiskScore
}
```

#### MCP Tools

```go
// code_context — Get production history for a file or function
mcp.NewTool("code_context",
    mcp.WithDescription(
        "Get production context for a source code file or function. "+
        "Returns error history, performance data, investigation history, "+
        "risk score, and relevant notes. Use this when editing or reviewing code "+
        "to understand its production behavior.",
    ),
    mcp.WithString("file", mcp.Required(),
        mcp.Description("File path (e.g., 'app/controllers/orders_controller.rb')")),
    mcp.WithString("function",
        mcp.Description("Function or method name (e.g., 'OrdersController#index')")),
)

// code_risk — Risk assessment for one or more files
mcp.NewTool("code_risk",
    mcp.WithDescription(
        "Get risk scores for source code files based on production history. "+
        "Use this before making changes to understand which files are fragile.",
    ),
    mcp.WithString("files", mcp.Required(),
        mcp.Description("Comma-separated file paths to assess")),
)

// whats_fragile — Top riskiest code paths in a service
mcp.NewTool("whats_fragile",
    mcp.WithDescription(
        "Find the riskiest code paths in a service based on production data. "+
        "Returns files/functions ranked by error frequency, investigation count, "+
        "and performance issues.",
    ),
    mcp.WithString("service",
        mcp.Description("Service name to analyze")),
    mcp.WithNumber("limit",
        mcp.Description("Max results (default 10)")),
)
```

#### What Claude Code Sees

When editing `orders_controller.rb`:

```json
{
  "code_context": {
    "file": "app/controllers/orders_controller.rb",
    "risk_score": 0.72,
    "risk_factors": [
      "3 production incidents in the last 30 days",
      "N+1 query on OrdersController#index (orders.includes missing)",
      "Investigated twice for timeout errors"
    ],
    "recent_incidents": [
      {
        "date": "2026-02-25",
        "session_id": "sess_001",
        "summary": "Connection pool exhaustion triggered by batch import hitting OrdersController#index",
        "root_cause": "Missing includes(:line_items) causes 47 queries per request under load",
        "fix_applied": "Added includes(:line_items)",
        "fix_held": true
      }
    ],
    "performance": {
      "avg_response_ms": 340,
      "p95_response_ms": 1200,
      "sql_count_avg": 23
    },
    "relevant_notes": [
      { "content": "N+1 query pattern — add .includes(:line_items) when loading orders with items" }
    ],
    "suggested_actions": [
      "Check that includes(:line_items) is present in any new queries loading orders",
      "Consider adding a watcher for this endpoint's SQL count"
    ]
  }
}
```

---

### New Capability 2: Git & Deploy Intelligence

Track deployments with full context — which files changed, who authored them, and what production impact resulted.

#### Data Model

```sql
CREATE TABLE IF NOT EXISTS deploys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    commit_hash TEXT NOT NULL,
    branch TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    files_changed TEXT NOT NULL DEFAULT '[]',     -- JSON array of file paths
    service TEXT NOT NULL DEFAULT '',
    environment TEXT NOT NULL DEFAULT 'production',

    -- Impact tracking (filled in asynchronously after deploy)
    error_rate_before REAL DEFAULT NULL,
    error_rate_after REAL DEFAULT NULL,
    error_rate_delta REAL DEFAULT NULL,
    response_time_before_ms REAL DEFAULT NULL,
    response_time_after_ms REAL DEFAULT NULL,
    response_time_delta_ms REAL DEFAULT NULL,
    caused_incident INTEGER NOT NULL DEFAULT 0,
    incident_session_id TEXT DEFAULT NULL,

    deployed_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_deploys_service ON deploys(service, deployed_at DESC);
CREATE INDEX idx_deploys_commit ON deploys(commit_hash);
CREATE INDEX idx_deploys_impact ON deploys(caused_incident, deployed_at DESC);
```

#### Webhook Endpoint

```go
// POST /api/events/deploy — called by CI/CD pipeline
type DeployEvent struct {
    CommitHash   string   `json:"commit_hash"`
    Branch       string   `json:"branch"`
    Author       string   `json:"author"`
    Message      string   `json:"message"`
    FilesChanged []string `json:"files_changed"`
    Service      string   `json:"service"`
    Environment  string   `json:"environment"`
}
```

#### Impact Tracking (Background Job)

```go
// Run 15 minutes after each deploy to measure impact
func (d *DeployTracker) MeasureImpact(ctx context.Context, deploy *Deploy) {
    // Compare metrics from 15 min before deploy to 15 min after
    before := d.getMetrics(ctx, deploy.Service, deploy.DeployedAt.Add(-15*time.Minute), deploy.DeployedAt)
    after := d.getMetrics(ctx, deploy.Service, deploy.DeployedAt, deploy.DeployedAt.Add(15*time.Minute))

    deploy.ErrorRateBefore = before.ErrorRate
    deploy.ErrorRateAfter = after.ErrorRate
    deploy.ErrorRateDelta = after.ErrorRate - before.ErrorRate
    deploy.ResponseTimeBeforeMs = before.AvgResponseMs
    deploy.ResponseTimeAfterMs = after.AvgResponseMs
    deploy.ResponseTimeDeltaMs = after.AvgResponseMs - before.AvgResponseMs

    // Check if an investigation session started within 30 min of deploy
    session, _ := d.sessionStore.FindByTimeRange(ctx, FindByTimeRangeParams{
        Service: deploy.Service,
        After:   deploy.DeployedAt,
        Before:  deploy.DeployedAt.Add(30 * time.Minute),
        Intent:  "investigation",
    })
    if session != nil {
        deploy.CausedIncident = true
        deploy.IncidentSessionID = &session.ID
    }

    // Update risk scores for all changed files
    for _, file := range deploy.FilesChanged {
        if deploy.CausedIncident {
            d.codeEntityStore.IncrementIncidentCount(ctx, file)
        }
    }
}
```

#### MCP Tools

```go
// deploy_history — Recent deploys with impact
mcp.NewTool("deploy_history",
    mcp.WithDescription(
        "View recent deployments with their production impact scores. "+
        "Shows error rate and response time changes caused by each deploy.",
    ),
    mcp.WithString("service", mcp.Description("Filter by service")),
    mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
)

// deploy_risk — Pre-deployment risk assessment
mcp.NewTool("deploy_risk",
    mcp.WithDescription(
        "Assess the risk of deploying changes to specific files. "+
        "Returns historical impact data for each file and an overall risk score. "+
        "Use this before deploying to understand potential production impact.",
    ),
    mcp.WithString("files", mcp.Required(),
        mcp.Description("Comma-separated file paths that will be deployed")),
    mcp.WithString("service",
        mcp.Description("Target service")),
)

// record_deploy — Record a deployment event
mcp.NewTool("record_deploy",
    mcp.WithDescription("Record a deployment for impact tracking."),
    mcp.WithString("commit", mcp.Required(), mcp.Description("Commit hash")),
    mcp.WithString("files", mcp.Description("Comma-separated changed files")),
    mcp.WithString("service", mcp.Description("Service name")),
    mcp.WithString("message", mcp.Description("Commit message")),
)
```

#### What Claude Code Sees (Pre-Deploy Check)

```json
{
  "deploy_risk_assessment": {
    "overall_risk": "high",
    "reason": "2 of 3 changed files have recent production incidents",
    "files": [
      {
        "file": "app/controllers/orders_controller.rb",
        "risk": "high",
        "history": {
          "deploys_last_30d": 5,
          "incidents_caused": 2,
          "last_incident": "2026-02-25: Connection pool exhaustion"
        }
      },
      {
        "file": "app/services/payment_service.rb",
        "risk": "medium",
        "history": {
          "deploys_last_30d": 3,
          "incidents_caused": 1,
          "last_incident": "2026-02-10: Payment timeout"
        }
      },
      {
        "file": "app/models/user.rb",
        "risk": "low",
        "history": {
          "deploys_last_30d": 8,
          "incidents_caused": 0
        }
      }
    ],
    "recommendations": [
      "Deploy during low-traffic hours (after 10 PM based on your traffic heatmap)",
      "Create a watcher for error_rate on payments service before deploying",
      "Monitor OrdersController#index response time for 15 minutes after deploy"
    ]
  }
}
```

---

### New Capability 3: Development Session Tracking

Extend session tracking beyond investigations. When an AI agent is writing code, refactoring, or reviewing, track that too and link it to production impact.

#### Extended Session Types

```
Session intents (updated):
  investigation  → debugging a production issue (existing)
  development    → writing/modifying code (NEW)
  review         → reviewing a PR or code change (NEW)
  deployment     → deploying and monitoring a release (NEW)
  query          → simple data retrieval (existing)
  configuration  → setting up monitoring (existing)
  exploration    → browsing/exploring (existing)
```

#### Development Session Detection

```go
func classifyDevelopmentIntent(context string, toolName string, args map[string]any) (string, string) {
    lower := strings.ToLower(context)

    // Development keywords
    devKeywords := []string{
        "adding", "implementing", "building", "creating feature",
        "refactoring", "modifying", "updating code", "writing",
        "fixing", "changing", "optimizing",
    }
    for _, kw := range devKeywords {
        if strings.Contains(lower, kw) {
            return "development", context
        }
    }

    // Review keywords
    reviewKeywords := []string{
        "reviewing", "review pr", "checking pr", "code review",
        "looking at changes", "pr #",
    }
    for _, kw := range reviewKeywords {
        if strings.Contains(lower, kw) {
            return "review", context
        }
    }

    // Deploy keywords
    deployKeywords := []string{
        "deploying", "deploy", "releasing", "shipping",
        "going to production", "pushing to prod",
    }
    for _, kw := range deployKeywords {
        if strings.Contains(lower, kw) {
            return "deployment", context
        }
    }

    return "", "" // not a development session
}
```

#### Linking Development to Production

```
Development session (10:00 AM):
  Claude Code modifies orders_controller.rb
  → OpenTrace records: files_modified: ["orders_controller.rb"]

Deploy (11:00 AM):
  CI/CD sends deploy event → files: ["orders_controller.rb"]
  → OpenTrace links: deploy includes changes from dev session sess_100

Investigation (1:00 PM):
  Error rate spikes → new investigation starts
  → OpenTrace connects ALL the dots:
    deploy.files_changed includes "orders_controller.rb"
    dev session sess_100 modified this file 3 hours ago
    → investigation_context includes:
      "orders_controller.rb was modified 3 hours ago (session sess_100)
       and deployed at 11:00 AM. Error spike started at 12:45 PM."
```

#### What Development Sessions Track

```sql
-- Additional columns on investigation_sessions for development sessions
ALTER TABLE investigation_sessions ADD COLUMN files_modified TEXT NOT NULL DEFAULT '[]';
ALTER TABLE investigation_sessions ADD COLUMN files_read TEXT NOT NULL DEFAULT '[]';
ALTER TABLE investigation_sessions ADD COLUMN linked_deploy_id INTEGER DEFAULT NULL;
```

---

### New Capability 4: Proactive Context Delivery

Don't wait for the agent to call a tool. Push relevant production context automatically via MCP notifications and resources.

#### MCP Notifications (Server → Client)

```go
// When something production-relevant happens while an agent is connected
func (s *MCPServer) checkForProactiveAlerts(ctx context.Context) {
    if s.session == nil || s.session.Intent == "" {
        return
    }

    // 1. Did any watcher fire for the service the agent is working on?
    if s.session.PrimaryService != "" {
        alerts, _ := s.watchStore.ListAlerts(ctx, store.ListAlertParams{
            Service:  s.session.PrimaryService,
            Status:   "active",
            After:    s.session.StartedAt,
        })
        for _, alert := range alerts {
            s.sendNotification(ctx, "production_alert", map[string]any{
                "message": fmt.Sprintf("Alert: %s for service %s", alert.Summary, s.session.PrimaryService),
                "alert_id": alert.ID,
                "suggested_tool": "triage_alerts",
            })
        }
    }

    // 2. Did error rate change for files the agent recently modified?
    for _, file := range s.session.FilesModified {
        entity, _ := s.codeEntityStore.GetByPath(ctx, file)
        if entity != nil && entity.ErrorCount30d > entity.PreviousErrorCount30d {
            s.sendNotification(ctx, "code_risk_change", map[string]any{
                "file": file,
                "message": fmt.Sprintf("Error count for %s increased since your last edit", file),
                "new_errors": entity.ErrorCount30d - entity.PreviousErrorCount30d,
            })
        }
    }

    // 3. Did a teammate's investigation find something relevant?
    if s.session.PrimaryService != "" {
        parallel := s.findParallelInvestigations(ctx, s.session)
        for _, p := range parallel {
            if p.HasNewFindings {
                s.sendNotification(ctx, "team_finding", map[string]any{
                    "message": fmt.Sprintf("A teammate found something relevant: %s", p.LatestFinding),
                    "session_id": p.SessionID,
                })
            }
        }
    }
}

func (s *MCPServer) sendNotification(ctx context.Context, notificationType string, data map[string]any) {
    s.mcpServer.SendNotification(ctx, mcp.Notification{
        Method: "notifications/message",
        Params: map[string]any{
            "level":   "info",
            "logger":  "opentrace",
            "type":    notificationType,
            "data":    data,
        },
    })
}
```

#### MCP Resources (Subscribable Production State)

```go
// Register resources that agents can subscribe to for real-time updates
func (s *MCPServer) registerResources() {
    // Per-service production status
    s.mcpServer.AddResource(mcp.Resource{
        URI:         "opentrace://services/{service}/status",
        Name:        "Service Production Status",
        Description: "Real-time production metrics for a service. Updates on significant changes.",
        MimeType:    "application/json",
    })

    // Code risk dashboard
    s.mcpServer.AddResource(mcp.Resource{
        URI:         "opentrace://code/risk-summary",
        Name:        "Code Risk Summary",
        Description: "Top risky code paths across all services. Updates when risk scores change.",
        MimeType:    "application/json",
    })

    // Active investigations feed
    s.mcpServer.AddResource(mcp.Resource{
        URI:         "opentrace://investigations/active",
        Name:        "Active Investigations",
        Description: "Currently active investigation sessions and their findings.",
        MimeType:    "application/json",
    })
}
```

---

### New Capability 5: Test-Production Correlation

Connect test coverage to production error paths. Help AI agents write the most impactful tests.

#### Data Model

```sql
CREATE TABLE IF NOT EXISTS test_production_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    test_file TEXT NOT NULL,                    -- "spec/controllers/orders_controller_spec.rb"
    production_file TEXT NOT NULL,              -- "app/controllers/orders_controller.rb"
    production_endpoint TEXT DEFAULT NULL,      -- "/api/orders"
    production_function TEXT DEFAULT NULL,      -- "OrdersController#index"

    -- Production impact of the code path this test covers
    error_fingerprints TEXT NOT NULL DEFAULT '[]',  -- errors in this code path
    error_count_30d INTEGER NOT NULL DEFAULT 0,
    investigation_count INTEGER NOT NULL DEFAULT 0,

    -- Coverage assessment
    has_error_case_coverage INTEGER NOT NULL DEFAULT 0, -- does the test cover error scenarios?
    has_performance_coverage INTEGER NOT NULL DEFAULT 0, -- does the test assert on performance?

    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_test_prod_links_prod ON test_production_links(production_file);

-- Track production error paths that have NO test coverage
CREATE TABLE IF NOT EXISTS uncovered_error_paths (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_path TEXT NOT NULL,
    function_name TEXT NOT NULL DEFAULT '',
    error_fingerprint TEXT NOT NULL,
    error_count_30d INTEGER NOT NULL DEFAULT 0,
    investigation_count INTEGER NOT NULL DEFAULT 0,
    impact_score REAL NOT NULL DEFAULT 0.0,     -- from ErrorImpact (users affected)
    suggested_test_description TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_uncovered_impact ON uncovered_error_paths(impact_score DESC);
```

#### MCP Tools

```go
// test_gaps — Find production error paths with no test coverage
mcp.NewTool("test_gaps",
    mcp.WithDescription(
        "Find production error paths that have no test coverage. "+
        "Returns code paths ranked by production impact (error frequency, "+
        "user impact, investigation count). Use this to prioritize which tests to write.",
    ),
    mcp.WithString("service", mcp.Description("Filter by service")),
    mcp.WithString("file", mcp.Description("Filter by source file")),
    mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
)

// test_priority — Highest-value tests to write based on production data
mcp.NewTool("test_priority",
    mcp.WithDescription(
        "Get a prioritized list of tests to write based on production impact. "+
        "Combines error frequency, user impact, and investigation history "+
        "to identify the most valuable test coverage gaps.",
    ),
    mcp.WithString("service", mcp.Description("Filter by service")),
    mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
)
```

#### What Claude Code Sees

```json
{
  "test_gaps": {
    "uncovered_error_paths": [
      {
        "file": "app/services/payment_service.rb",
        "function": "PaymentService#charge",
        "error": "Net::ReadTimeout when payment gateway is slow",
        "production_impact": {
          "error_count_30d": 47,
          "users_affected": 2400,
          "investigations": 3
        },
        "suggested_test": "Test PaymentService#charge with a slow/timing out gateway connection. Verify it raises a user-friendly error and doesn't leave the payment in a pending state."
      },
      {
        "file": "app/controllers/orders_controller.rb",
        "function": "OrdersController#create",
        "error": "ActiveRecord::RecordNotUnique on duplicate order submission",
        "production_impact": {
          "error_count_30d": 12,
          "users_affected": 89,
          "investigations": 1
        },
        "suggested_test": "Test OrdersController#create with a duplicate idempotency key. Verify it returns the existing order instead of raising."
      }
    ]
  }
}
```

---

### New Capability 6: Webhook/Event Intake

Accept events from the entire development ecosystem so OpenTrace has the full picture.

#### Endpoints

```go
// All webhook endpoints validate via API key or bearer token

POST /api/events/deploy    // CI/CD pipeline sends deploy info
POST /api/events/pr        // GitHub webhook for PR events
POST /api/events/test      // Test runner sends results
POST /api/events/alert     // External alerting (PagerDuty, OpsGenie)
POST /api/events/commit    // Git hook sends commit info
POST /api/events/custom    // Generic event for custom integrations
```

#### Event Models

```go
type PREvent struct {
    Action       string   `json:"action"`       // "opened", "merged", "closed"
    PRNumber     int      `json:"pr_number"`
    Title        string   `json:"title"`
    Author       string   `json:"author"`
    Branch       string   `json:"branch"`
    BaseBranch   string   `json:"base_branch"`
    FilesChanged []string `json:"files_changed"`
    URL          string   `json:"url"`
}

type TestEvent struct {
    TestFile     string `json:"test_file"`
    TestName     string `json:"test_name"`
    Status       string `json:"status"`       // "passed", "failed", "error"
    DurationMs   int    `json:"duration_ms"`
    ErrorMessage string `json:"error_message,omitempty"`
    CommitHash   string `json:"commit_hash"`
    Branch       string `json:"branch"`
}

type ExternalAlertEvent struct {
    Source       string `json:"source"`        // "pagerduty", "opsgenie", "custom"
    AlertID      string `json:"alert_id"`
    Summary      string `json:"summary"`
    Severity     string `json:"severity"`
    Service      string `json:"service"`
    URL          string `json:"url,omitempty"`
}
```

#### How Events Connect

```
commit → PR opened → CI tests run → PR merged → deploy → metrics change → alert → investigation
   │         │            │             │           │            │           │          │
   └─────────┴────────────┴─────────────┴───────────┴────────────┴───────────┴──────────┘
                              All linked in OpenTrace
```

When an investigation starts, OpenTrace can trace the full chain: "This error started after deploy `abc123`, which was PR #456, authored by developer A, which changed `orders_controller.rb`. CI tests passed but there was no test covering the error case."

---

### New Capability 7: The `context` Meta-Tool

Instead of requiring agents to know which of 80+ tools to call, provide one tool that returns everything relevant to the current task.

```go
mcp.NewTool("context",
    mcp.WithDescription(
        "Get all relevant OpenTrace context for your current work. "+
        "Describe what you're doing and OpenTrace returns production data, "+
        "investigation history, risk scores, and suggestions tailored to your task. "+
        "This is the recommended starting tool for any interaction with OpenTrace.",
    ),
    mcp.WithString("task", mcp.Required(),
        mcp.Description(
            "What you're currently doing. Examples: "+
            "'editing app/controllers/orders_controller.rb to add pagination', "+
            "'debugging payment timeout errors', "+
            "'reviewing PR #123 that changes payment_service.rb', "+
            "'preparing to deploy payments service', "+
            "'writing tests for OrdersController'")),
    mcp.WithString("files",
        mcp.Description("Comma-separated file paths you're working with")),
    mcp.WithString("service",
        mcp.Description("Service name if known")),
)
```

#### Context Bundle by Task Type

```go
func (h *contextHandler) Handle(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    args := req.GetArguments()
    task := args["task"].(string)
    files := parseFilesList(args["files"])
    service := args["service"]

    taskType := classifyTask(task) // "editing", "debugging", "reviewing", "deploying", "testing"

    bundle := ContextBundle{}

    switch taskType {
    case "editing":
        // Code risk, error history, performance, notes, investigation history
        for _, file := range files {
            bundle.CodeContext = append(bundle.CodeContext, h.getCodeContext(ctx, file))
        }
        bundle.RelevantNotes = h.getNotesForFiles(ctx, files)
        bundle.RecentInvestigations = h.getInvestigationsForFiles(ctx, files)

    case "debugging":
        // Full investigation context (same as investigation memory)
        bundle.InvestigationContext = h.getInvestigationContext(ctx, service, task)
        bundle.RelevantNotes = h.getNotesForService(ctx, service)
        bundle.RunbookSuggestion = h.suggestRunbook(ctx, task)

    case "reviewing":
        // Risk assessment for changed files, production history
        bundle.DeployRisk = h.assessDeployRisk(ctx, files)
        for _, file := range files {
            bundle.CodeContext = append(bundle.CodeContext, h.getCodeContext(ctx, file))
        }
        bundle.TestGaps = h.getTestGaps(ctx, files)

    case "deploying":
        // Pre-deploy risk check, recommended watchers, safe deploy windows
        bundle.DeployRisk = h.assessDeployRisk(ctx, files)
        bundle.RecommendedWatchers = h.suggestWatchers(ctx, files, service)
        bundle.SafeDeployWindow = h.getSafeDeployWindow(ctx, service)

    case "testing":
        // Production error paths, test gaps, priority tests to write
        bundle.TestGaps = h.getTestGaps(ctx, files)
        bundle.TestPriority = h.getTestPriority(ctx, service)
    }

    data, _ := json.Marshal(bundle)
    return mcp.NewToolResultText(string(data)), nil
}
```

#### What Claude Code Sees (editing a file)

```json
{
  "task_type": "editing",
  "context": {
    "code_risk": [
      {
        "file": "app/controllers/orders_controller.rb",
        "risk_score": 0.72,
        "risk_factors": ["3 incidents in 30 days", "N+1 query pattern"],
        "last_incident": "Connection pool exhaustion, 3 days ago",
        "watch_out_for": "Ensure .includes(:line_items) is present in any new queries"
      }
    ],
    "relevant_notes": [
      "N+1 query pattern on OrdersController#index — always use includes(:line_items)",
      "Batch import job can exhaust connection pool — avoid long-running transactions"
    ],
    "recent_investigations": [
      {
        "date": "2026-02-25",
        "summary": "Connection pool exhaustion from batch import",
        "related_to_this_file": true
      }
    ],
    "performance_baseline": {
      "avg_response_ms": 340,
      "sql_count": 23,
      "note": "This endpoint is the #3 most trafficked. Changes here affect 15% of total traffic."
    }
  },
  "suggested_tools": [
    { "tool": "code_context", "why": "Get detailed production history for specific functions" },
    { "tool": "deploy_risk", "why": "Assess risk before deploying your changes" }
  ]
}
```

---

### New Capability 8: Cross-Agent Knowledge Sharing

Any MCP client (Claude Code, Cursor, Copilot, Continue) benefits from any other client's findings. OpenTrace is the shared brain.

#### How It Works

Already built into Investigation Memory — the auth token identifies the user, the session tracks what they did, and context injection shares findings across users (access-scoped). This section makes it explicit for **different AI agent types**:

```
Claude Code (Developer A):
  → Investigates payment timeout
  → Finds root cause: batch import job
  → OpenTrace stores investigation + summary + root cause

Cursor (Developer B, same codebase):
  → Opens payments_controller.rb
  → Calls: context(task: "editing payments_controller.rb")
  → OpenTrace serves Developer A's findings:
    "This file was investigated 2 days ago.
     Root cause: batch import job exhausts connection pool.
     Suggestion: ensure transaction timeouts are set."

Developer B's AI agent writes safer code without ever investigating.
```

The only requirement: both agents authenticate with MCP tokens from the same OpenTrace instance and the users have overlapping data source access.

---

### Agent Assistant Example Workflows

#### Workflow 8: Code-Aware Investigation

```
Developer: "Orders page is slow"
Claude Code connects to OpenTrace MCP

── Step 1: context ─────────────────────────────────────────
  Claude Code calls: context(task: "debugging slow orders page",
                             service: "web")

  OpenTrace returns:
  {
    "task_type": "debugging",
    "context": {
      "code_risk": [{
        "file": "app/controllers/orders_controller.rb",
        "risk_score": 0.72,
        "risk_factors": ["N+1 query", "P95 1200ms"]
      }],
      "investigation_history": [{
        "date": "2026-02-20",
        "summary": "N+1 query in OrdersController#index",
        "fix": "Added includes(:line_items)",
        "fix_held": false
      }],
      "query_memory": {
        "fingerprint": "SELECT * FROM orders...",
        "last_root_cause": "Missing includes causes 47 queries per request"
      },
      "runbook_suggestion": {
        "name": "slow_database",
        "resolution_rate": 0.82
      }
    }
  }

  Claude Code immediately knows:
    • This is a known N+1 query issue
    • It was "fixed" before but the fix didn't hold
    • The specific query and root cause are already documented
    • Goes directly to checking if includes(:line_items) is present
    • Skips: diagnose, log_search, db_query_stats, explain_query
    • 1 step instead of 6
```

#### Workflow 9: Pre-Deploy Safety Check

```
Developer: "Ship it"
Claude Code is about to deploy changes to payments service

── Step 1: deploy_risk ─────────────────────────────────────
  Claude Code calls: deploy_risk(files: "app/controllers/orders_controller.rb,app/services/payment_service.rb",
                                  service: "payments")

  OpenTrace returns:
  {
    "deploy_risk_assessment": {
      "overall_risk": "high",
      "files": [
        { "file": "orders_controller.rb", "risk": "high", "incidents_caused": 2 },
        { "file": "payment_service.rb", "risk": "medium", "incidents_caused": 1 }
      ],
      "recommendations": [
        "Deploy during low-traffic hours (after 10 PM)",
        "Create error_rate watcher for payments before deploying"
      ],
      "test_gaps": [
        "PaymentService#charge timeout handling has no test (3 incidents)"
      ]
    }
  }

  Claude Code:
    → Warns the developer about the risk
    → Suggests writing a test for the timeout handling first
    → Creates a watcher before deploying
    → Recommends a deploy window
```

#### Workflow 10: Test Writing Guided by Production

```
Developer: "Write tests for the payments service"
Claude Code calls: test_priority(service: "payments")

OpenTrace returns:
{
  "test_priority": [
    {
      "rank": 1,
      "file": "app/services/payment_service.rb",
      "function": "PaymentService#charge",
      "error": "Net::ReadTimeout",
      "impact": { "errors_30d": 47, "users_affected": 2400, "investigations": 3 },
      "suggested_test": "Test charge with slow gateway. Verify timeout handling and payment state consistency."
    },
    {
      "rank": 2,
      "file": "app/controllers/orders_controller.rb",
      "function": "OrdersController#create",
      "error": "RecordNotUnique on duplicate submission",
      "impact": { "errors_30d": 12, "users_affected": 89, "investigations": 1 },
      "suggested_test": "Test create with duplicate idempotency key. Verify existing order returned."
    }
  ]
}

Claude Code writes the highest-impact tests first,
guided by real production error data.
```

#### Workflow 11: Proactive Alert During Development

```
Developer is editing payment_service.rb
Claude Code is connected to OpenTrace MCP

── Background: watcher fires ──────────────────────────────
  Watcher "payment-error-rate" triggers: error_rate > 5

  OpenTrace detects:
    • Agent is connected and working on payments service
    • Alert is relevant to current work

  OpenTrace sends MCP notification:
  {
    "method": "notifications/message",
    "params": {
      "level": "warning",
      "logger": "opentrace",
      "data": {
        "type": "production_alert",
        "message": "Payment error rate just spiked to 8%. You're currently editing payment_service.rb — this may be related to a recent deploy.",
        "alert_id": "alert_789",
        "suggested_tool": "triage_alerts"
      }
    }
  }

  Claude Code receives notification and alerts the developer:
    "OpenTrace detected a production alert for the payments service
     you're working on. Want me to investigate?"
```

---

### Agent Assistant Store Interfaces

```go
type CodeEntityStore interface {
    Upsert(ctx context.Context, entity CodeEntity) error
    GetByPath(ctx context.Context, filePath string) (*CodeEntity, error)
    GetByFunction(ctx context.Context, filePath string, functionName string) (*CodeEntity, error)
    GetByService(ctx context.Context, service string, limit int) ([]CodeEntity, error)
    GetTopRisk(ctx context.Context, service string, limit int) ([]CodeEntity, error)
    IncrementErrorCount(ctx context.Context, filePath string) error
    IncrementIncidentCount(ctx context.Context, filePath string) error
    LinkInvestigation(ctx context.Context, filePath string, sessionID string) error
    UpdateRiskScores(ctx context.Context) error  // batch recompute
}

type DeployStore interface {
    Create(ctx context.Context, deploy Deploy) (*Deploy, error)
    GetByCommit(ctx context.Context, commitHash string) (*Deploy, error)
    List(ctx context.Context, params ListDeployParams) ([]Deploy, error)
    UpdateImpact(ctx context.Context, deployID int, impact DeployImpact) error
    GetRiskForFiles(ctx context.Context, files []string) ([]FileDeployRisk, error)
}

type TestProductionLinkStore interface {
    Upsert(ctx context.Context, link TestProductionLink) error
    GetGaps(ctx context.Context, params TestGapParams) ([]UncoveredErrorPath, error)
    GetPriority(ctx context.Context, service string, limit int) ([]UncoveredErrorPath, error)
}

type EventStore interface {
    LogPREvent(ctx context.Context, event PREvent) error
    LogTestEvent(ctx context.Context, event TestEvent) error
    LogExternalAlert(ctx context.Context, event ExternalAlertEvent) error
    LogCustomEvent(ctx context.Context, eventType string, data map[string]any) error
}
```

---

### Agent Assistant Web API Endpoints

```
-- Code Intelligence
GET  /api/code/entities                    — list code entities with risk scores
GET  /api/code/entities/{path}            — single entity detail
GET  /api/code/risk                        — risk assessment for files
GET  /api/code/fragile                     — top risky code paths

-- Deploy Intelligence
GET  /api/deploys                          — deploy history with impact
POST /api/deploys                          — record a deploy
GET  /api/deploys/{id}/impact             — deploy impact detail

-- Test Intelligence
GET  /api/tests/gaps                       — uncovered production error paths
GET  /api/tests/priority                   — highest-value tests to write

-- Events
POST /api/events/deploy                    — CI/CD deploy webhook
POST /api/events/pr                        — GitHub PR webhook
POST /api/events/test                      — test result webhook
POST /api/events/alert                     — external alert webhook
POST /api/events/commit                    — git commit webhook
POST /api/events/custom                    — generic event webhook
```

---

## Implementation Order

### Phase 1: Foundation
1. Migration: all new tables and columns
2. `InvestigationSession` model and store
3. `SessionTracker` — creates session on Initialize, tracks steps, closes on Shutdown
4. Wire into `wrapWithActivityLog` — replace hardcoded `sessionID = "mcp"` with real identity
5. Update all mock stores

### Phase 2: Intent + Outcome
6. Add `context` parameter to top 10 most-used tools
7. Intent classification (3 layers)
8. Outcome inference with all subsystem signals
9. MCP Sampling integration
10. `set_session_summary` fallback tool

### Phase 3: Watcher + Error Group + Health Check Linking
11. Auto-link on watcher creation
12. Auto-link on alert investigation + recurrence tracking
13. Auto-link on error investigation (`error_detail`, `investigate_error`)
14. Auto-link on error resolution (`resolve_error`, `ignore_error`)
15. Error group recurrence detection (reopened errors)
16. Health check failure as investigation trigger
17. Health check creation linking
18. Escalation note generation

### Phase 4: Agent Notes + Runbooks
19. Surface existing notes during investigations
20. Auto-create notes from resolved sessions
21. Track runbook execution in sessions
22. Runbook effectiveness tracking
23. Auto-suggest runbooks based on effectiveness

### Phase 5: Trends + Performance + Database
24. Pre/post investigation metric snapshots
25. Deploy marker correlation
26. Request performance baseline tracking
27. Query memory (explain_query fingerprints)
28. kill_query as resolution signal
29. Post-fix metric comparison

### Phase 6: Traces + Analytics + Audit
30. Trace ID tracking in sessions
31. Traffic context from web analytics
32. Traffic anomaly detection
33. Audit log integration (record investigations + surface admin actions)

### Phase 7: Ranking Engine
34. `ToolTransitionStore` — upsert, query, dead-end detection
35. Transition rollup background job
36. `RankingService` — score formula with runbook integration
37. Replace static suggestions with ranked suggestions
38. Suggestion acceptance tracking
39. Seed workflow templates for cold start

### Phase 8: Context Injection
40. `ContextInjector` — assembles investigation_context from all 12 subsystems
41. Similar session matching
42. Parallel investigation detection
43. Dead-end warnings
44. Access-scoped filtering
45. Response size capping

### Phase 9: Web + Analytics (Part 1)
46. Web API endpoints for investigation sessions
47. Admin UI: investigation dashboard
48. Retention/pruning jobs for Part 1 tables

### Phase 10: Code Entity Registry
49. Migration: `code_entities` table
50. `CodeEntityStore` implementation
51. `CodeEntityPopulator` — auto-populate from error groups, request summaries, investigation sessions
52. Risk score computation + batch recompute job
53. `code_context`, `code_risk`, `whats_fragile` MCP tools
54. Update all mock stores

### Phase 11: Deploy Intelligence + Webhook Intake
55. Migration: `deploys` table
56. `DeployStore` implementation
57. `EventStore` implementation (PR, test, alert, commit, custom events)
58. Webhook HTTP endpoints (`POST /api/events/{type}`) with API key validation
59. `DeployTracker` background job — measures impact 15 min after deploy
60. `deploy_history`, `deploy_risk`, `record_deploy` MCP tools
61. Link deploys to code entities — increment incident counts on impacted files

### Phase 12: Development Session Tracking
62. Extended session intents: `development`, `review`, `deployment`
63. `classifyDevelopmentIntent` — keyword-based classification
64. `files_modified`, `files_read`, `linked_deploy_id` columns on `investigation_sessions`
65. Development → deploy → investigation chain linking

### Phase 13: Proactive Context Delivery
66. MCP notifications infrastructure (`sendNotification` on MCPServer)
67. Background alert checker — polls for alerts relevant to connected sessions
68. Code risk change notifications — detects error count increases for modified files
69. Team finding notifications — detects parallel investigation discoveries
70. MCP resources: `opentrace://services/{service}/status`, `opentrace://code/risk-summary`, `opentrace://investigations/active`

### Phase 14: Test-Production Correlation
71. Migration: `test_production_links` and `uncovered_error_paths` tables
72. `TestProductionLinkStore` implementation
73. Auto-populate from test events + error groups
74. `test_gaps`, `test_priority` MCP tools
75. Suggested test description generation

### Phase 15: Context Meta-Tool + Cross-Agent + Web API
76. `context` meta-tool implementation
77. Task type classifier: `editing`, `debugging`, `reviewing`, `deploying`, `testing`
78. Context bundle assembly — per task type, pulls from relevant subsystems
79. Cross-agent knowledge sharing verification (already built into auth + sessions)
80. Web API endpoints for code entities, deploys, tests, events
81. Admin UI: code risk dashboard, deploy impact view, test coverage gaps

---

## Testing Plan

### Unit Tests
- Session lifecycle: create → step tracking → close
- Intent classification: all 3 layers with edge cases
- Outcome inference: all signal types (watcher, error, healthcheck, kill_query, summary)
- Ranking: score formula, fallback chain, time decay, runbook suggestions
- Recurrence: watcher chains, error reopens, health check failures, fix durability
- Context injection: all 12 subsystems, access filtering, response size limits
- Transition rollup: aggregation correctness
- Query memory: fingerprinting, upsert, lookup
- Runbook effectiveness: scoring, resolution rate calculation
- Auto-notes: creation rules, entity linking

### Store Tests (SQLite)
- Migration up/down
- Session CRUD and all query types
- Transition upsert idempotency
- Similarity search performance
- Query memory persistence
- Runbook effectiveness persistence
- Retention pruning across all tables

### Integration Tests
- Full session lifecycle with all subsystem hooks
- Watcher creation → alert fires → recurrence detected
- Error resolution → error reopens → recurrence detected
- Health check down → investigated → health check created
- Runbook execution → effectiveness tracked → suggested next time
- Query investigated → stored in memory → served next time
- Deploy correlation detected and served
- Pre/post metric comparison on resolved session
- Agent notes auto-created on resolution
- Audit trail recorded for investigation
- MCP Sampling summary flow with fallback
- Multiple concurrent sessions (no cross-contamination)
- Reconnection and session resume
- Cross-team knowledge sharing (access-scoped)

### Part 2: Code Entity + Deploy Tests

#### Unit Tests
- Code entity populator: from error groups (stack trace parsing), request summaries, sessions
- Risk score computation: all factor weights, edge cases (0 errors, maxed out, recent incident)
- Deploy impact measurement: error rate delta, response time delta, incident detection
- Task type classification: editing, debugging, reviewing, deploying, testing keywords
- Context bundle assembly: correct subsystems queried for each task type
- Test-production link matching: file path mapping, coverage detection
- Uncovered error path ranking: impact score calculation

#### Store Tests (SQLite)
- `code_entities` CRUD, unique index on (type, path, name), risk score ordering
- `deploys` CRUD, commit lookup, impact update, risk-for-files query
- `test_production_links` upsert, gap query, priority ordering
- `uncovered_error_paths` impact score ordering
- Event store: all event types persisted correctly

#### Integration Tests
- Full code entity lifecycle: error group created → entity populated → risk computed → served via `code_context`
- Deploy lifecycle: webhook received → deploy recorded → impact measured → linked to investigation
- Development session → deploy → investigation chain correctly linked
- `context` meta-tool returns different bundles for editing vs debugging vs deploying
- MCP notifications delivered when alert fires during active session
- MCP resources return correct real-time data
- Test gaps populated from error groups with no matching test files
- Cross-agent: session from Claude Code enriches Cursor's `context` call (access-scoped)
- Webhook authentication: invalid API key rejected, valid key accepted

### Backward Compatibility Tests
- All existing tool calls work unchanged when no history exists
- Suggestion format remains compatible (new fields are additive)
- Missing `context` parameter doesn't break tools
- MCP Sampling failure doesn't affect tool execution
- All subsystem integrations fail gracefully (tool still works if integration errors)
- New webhook endpoints don't affect existing API routes
- `context` meta-tool works with empty code entity registry (returns empty sections gracefully)
- Deploy risk returns "no data" assessment for unknown files (not an error)

---

## Success Metrics

| Metric | Target | How Measured |
|---|---|---|
| Median steps to resolution | 20-30% reduction | Compare session.total_steps over 30-day windows |
| First-suggestion acceptance | >50% | was_suggested=true on step N+1 |
| Dead-end rate | 15% reduction | Abandoned transitions / total transitions |
| Recurrence detection | >90% accuracy | All trigger types correctly grouped |
| Session summary coverage | >80% of investigation sessions | Non-empty summary field |
| Query memory hit rate | Track | Queries with prior investigation history |
| Runbook suggestion acceptance | Track | Runbook used when suggested |
| Auto-note creation | Track | Notes created per resolved session |
| Cross-team knowledge transfer | Track | Sessions enriched by other users' findings |
| Zero latency impact | <5ms overhead | P99 tool response time before/after |
| **Part 2** | | |
| Code entity coverage | >80% of error groups mapped | Entities with linked error fingerprints / total error groups |
| Risk score accuracy | Manual review of top 20 | Risk factors match known fragile code |
| Deploy impact detection | >90% of incident-causing deploys flagged | Deploys with `caused_incident=true` / deploys followed by investigation within 30 min |
| `context` tool adoption | >60% of sessions start with `context` | Sessions where first tool is `context` / total sessions |
| Context bundle relevance | Track | User follows suggestion from context bundle (next tool matches suggested_tools) |
| Test gap identification | Track | Uncovered error paths that get tests written within 7 days |
| Proactive notification hit rate | >70% actionable | Notifications that led to a tool call within 5 min / total notifications |
| Webhook event ingestion | Track | Events per type per day, processing latency |
| Cross-agent knowledge hits | Track | `context` calls enriched by a different agent's findings |

---

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Cold start — no data for weeks | Curated workflow templates + runbook suggestions as fallback |
| MCP Sampling not supported by client | `set_session_summary` manual fallback + automatic inference from 5+ signal types |
| SQLite write pressure from activity logging | Existing async ActivityLogger (bounded channel, 2 workers) handles this |
| Stale historical patterns | Time-decay in ranking, 30-day rolling windows |
| Low-volume instances → noisy rankings | Minimum support threshold (3 occurrences), template fallback |
| Privacy — leaking findings across teams | Access-scoped filtering on all context injection |
| Context injection makes responses too large | Cap `investigation_context` to 2KB, only include for investigation intent, prioritize most relevant sections |
| Session detection wrong on reconnect | Ask Claude Code via tool response, don't guess |
| Too many subsystem queries per tool call | Lazy loading: only query subsystems relevant to current tool + cached per-session |
| Subsystem integration failure | Each integration wrapped in error recovery — tool always works even if enrichment fails |
| **Part 2** | |
| Stack trace parsing fails for unfamiliar frameworks | Pluggable parsers per language (Ruby, Go, Python, JS). Unknown formats stored raw and skipped for entity mapping |
| Code entity registry grows unbounded | Prune entities with zero errors and zero investigations older than 90 days. Cap at 50K entities per instance |
| Deploy impact measurement window misses slow-onset issues | 15-minute window is the default. Add a second check at 1 hour. Incident linking also catches sessions starting up to 30 min after deploy |
| Webhook endpoint abuse / spam | Rate limiting per API key (100 events/min). Payload size limit (64KB). Event deduplication by idempotency key |
| MCP notifications not supported by all clients | Graceful degradation — notifications are fire-and-forget. If client doesn't support them, they're silently dropped. Agent still gets context via tool calls |
| `context` tool returns too much data | Cap response to 4KB. Prioritize by task type relevance. Include `more_available: true` flag when truncated |
| Risk scores are noisy with little data | Require minimum thresholds: 3 errors before risk > 0.2, 1 investigation before risk > 0.3. Show "insufficient data" for new entities |
| Test-production linking accuracy (wrong file mapping) | Use exact file path matching first. Convention-based mapping (`app/foo.rb` → `spec/foo_spec.rb`) as fallback. Manual override via MCP tool |
| Cross-agent data leakage between organizations | Auth token scopes data to the token's user + their accessible data sources. No cross-org queries possible |

---

## Expected Behavior Summary

| Scenario | What Happens |
|---|---|
| First investigation of a new issue | Normal results + deploy/traffic/admin context. Session recorded. |
| Second investigation of same issue (same dev) | Full history: past session, notes, query memory, fix impact. |
| Same issue, different developer | Teammate's findings served (access-scoped). Notes, query memory, error history included. |
| Watcher fires from previous investigation | Full recurrence chain with fix durability and escalation notes. |
| Error group reopens after being resolved | Treated as recurrence. Previous resolution context served. |
| Health check goes down again | Same recurrence pattern. Previous outage history served. |
| Recurring issue with degrading fixes | "Fixes holding for less time. Consider root cause." |
| Same slow query investigated again | Query memory: "Investigated 3 times. Root cause: missing index. Last fix improved 450ms→12ms." |
| Runbook available for this type of issue | "This runbook resolved 82% of similar investigations." |
| Deploy happened before issue started | "Deploy abc123 happened 15 minutes before error spike." |
| Admin changed config before issue | "Connector max_connections changed from 20 to 10, 30 min ago." |
| Traffic spike correlates with errors | "Traffic is 2.3x normal for this hour." |
| Simple data query | Minimal tracking. No context injection. No summary. |
| Multiple developers investigating simultaneously | Independent sessions. Cross-pollination of findings. |
| Claude Code crashes mid-session | On reconnect: "Continue this investigation?" |
| MCP Sampling not available | Automatic inference + set_session_summary tool. |
| Brand new OpenTrace install | Templates + runbook suggestions until real data accumulates. |
| Subsystem integration fails | Tool works normally. Missing context section logged as warning. |
| **Part 2 Scenarios** | |
| Agent edits a high-risk file | `context` returns risk score, recent incidents, notes, performance baseline. Agent warns developer. |
| Agent edits a file with no production data | `context` returns empty code_risk section. No warnings. No errors. |
| CI/CD sends deploy webhook | Deploy recorded. Impact measured after 15 min. If incident follows, linked to deploy + changed files. |
| Agent about to deploy risky files | `deploy_risk` returns file-by-file risk, incident history, recommended deploy window, suggested pre-deploy watchers. |
| Agent writing tests | `test_priority` returns production error paths ranked by impact. Agent writes highest-value tests first. |
| Watcher fires while agent is connected | MCP notification pushed. Agent alerts developer with context about the alert. |
| Error count increases for a file agent modified | MCP notification: "Error count for X increased since your last edit." |
| Teammate finds root cause in parallel session | MCP notification: "A teammate found something relevant." Context shared access-scoped. |
| Agent calls `context` for the first time | Returns tailored bundle for task type. Becomes the recommended starting point for all interactions. |
| Different AI agents (Claude Code + Cursor) on same codebase | Both authenticate via MCP tokens. Both contribute to and benefit from shared investigation memory + code entity registry. |
| Webhook for unknown event type | `POST /api/events/custom` accepts any structured event. Stored for future correlation. |
| Deploy has no measurable impact | Impact fields remain at zero/null. `caused_incident = false`. No risk score inflation. |
| Test file maps to wrong production file | Exact path matching prevents false links. Convention fallback only for standard layouts. Manual override available. |
