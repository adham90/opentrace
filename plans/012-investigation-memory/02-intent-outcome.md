# Intent Classification & Outcome Inference

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
