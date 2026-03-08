# Suggestion Ranking Engine

## How It Works

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

## Ranking Algorithm

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

## Negative Signal Tracking

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

## Suggestion Acceptance Tracking

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

# Context Injection (What Claude Code Receives)

The key output of this system. Every tool response is enriched with investigation context from past sessions AND related subsystem data.

## Full Enriched Response Example

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

## How Context Is Built

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

## Escalation Note Generation (Deterministic)

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

# Cold Start: Workflow Templates

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
