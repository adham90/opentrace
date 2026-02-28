# Subsystem Integrations

## Integration 1: Watcher Alerts (Watch System)

**Trigger:** Watcher alert fires → investigation starts with full history.
**Link out:** Watcher created during investigation → linked to session.

### Auto-Link on Watcher Creation

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

### Auto-Link on Alert Investigation

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

### Recurrence Chain

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

## Integration 2: Error Groups (Sentry-lite)

**Trigger:** Error group reopens → treated as recurrence, same as watcher alert.
**Link out:** `resolve_error` / `ignore_error` called → linked to session.

### Auto-Link on Error Investigation

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

### Auto-Link on Error Resolution

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

### Error Group Recurrence

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

### What Claude Code Sees

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

## Integration 3: Agent Notes (Persistent Memory)

**Enrichment:** Existing notes for investigated service/endpoint served as context.
**Link out:** Auto-create notes from resolved investigations.

### Surface Existing Notes During Investigation

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

### Auto-Create Notes From Resolved Sessions

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

### What Claude Code Sees

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

## Integration 4: Runbooks

**Link in:** Runbook execution recorded as part of session.
**Link out:** Runbook effectiveness tracked across sessions.

### Track Runbook Execution

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

### Track Runbook Effectiveness

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

### Auto-Suggest Runbooks

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

### What Claude Code Sees

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

## Integration 5: Health Checks (Uptime Monitoring)

**Trigger:** Health check goes `down` or `degraded` → investigation trigger (same as watcher).
**Link out:** `create_healthcheck` during investigation → linked to session.

### Health Check as Investigation Trigger

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

### Link Health Check Creation

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

### What Claude Code Sees

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

## Integration 6: Trends & Deploy Markers

**Enrichment:** Trend data shows metric changes leading up to investigation.
**Enrichment:** Deploy markers correlate investigations with code changes.

### Capture Pre-Investigation Metrics

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

### Correlate With Deploys

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

### Post-Fix Validation

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

### What Claude Code Sees

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

## Integration 7: Request Performance

**Enrichment:** Performance baselines for investigated endpoints.
**Link out:** N+1 query patterns linked to investigations.

### Track Performance Context

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

### What Claude Code Sees

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

## Integration 8: Database Diagnostics

**Link out:** Query fingerprints from `explain_query` stored in query memory.
**Link out:** `kill_query` PIDs recorded as resolution actions.

### Build Query Memory

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

### Track kill_query

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

### What Claude Code Sees

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

## Integration 9: Distributed Traces

**Link out:** Trace IDs followed during investigation are recorded.
**Enrichment:** Past investigations linked to the same trace patterns.

### Track Traces

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

### What Claude Code Sees

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

## Integration 10: Web Analytics & Traffic Heatmap

**Enrichment:** Traffic patterns provide investigation context.

### Correlate Traffic Anomalies

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

### What Claude Code Sees

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

## Integration 11: Audit Log

**Link out:** Investigation sessions appear as audit entries.
**Enrichment:** Recent admin actions correlated with investigations.

### Record Investigation in Audit Log

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

### Surface Recent Admin Actions

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

### What Claude Code Sees

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

## Integration 12: Compare Periods (Anomaly Detection)

**Link out:** Auto-compare before/after investigation on session close.
**Enrichment:** Serve comparison data to future sessions.

### Auto-Compare on Session Close

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

### What Claude Code Sees (On Future Recurrence)

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
