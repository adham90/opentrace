# Session Lifecycle

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

## Primary: MCP Connection = Session

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

---

## Fallback: Ask Claude Code (for ambiguous cases)

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

---

## Multiple Concurrent Sessions

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

## Go Models

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
