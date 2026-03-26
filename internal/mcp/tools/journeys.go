package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/pkg/store"
)

// JourneysDeps holds the stores needed by the journeys tool.
type JourneysDeps struct {
	JourneyStore store.JourneyStore
}

// JourneysTool returns the consolidated tool definition for user journey tools.
func JourneysTool() mcp.Tool {
	return mcp.NewTool("journeys",
		mcp.WithDescription(`User journey analysis: sessions, navigation paths, funnels, and request timelines.

Actions:
- sessions: List/get user sessions (by user_id, session_id, or recent)
- paths: Common navigation paths with error rates
- funnels: Funnel analysis (sub-actions: list, create, analyze, delete)
- timeline: Request timeline waterfall for a single request
- waterfall: Session waterfall showing all requests in a session`),
		mcp.WithString("action", mcp.Required(), mcp.Description("Action: sessions, paths, funnels, timeline, waterfall")),
		// sessions params
		mcp.WithString("user_id", mcp.Description("User ID to filter sessions")),
		mcp.WithString("session_id", mcp.Description("Specific session ID")),
		mcp.WithString("since", mcp.Description("Time window (default: 24h). Examples: 1h, 7d")),
		mcp.WithNumber("limit", mcp.Description("Max results (default: 10)")),
		// paths params
		mcp.WithString("service", mcp.Description("Service name filter")),
		mcp.WithNumber("min_occurrences", mcp.Description("Minimum path occurrences (default: 5)")),
		mcp.WithNumber("path_length", mcp.Description("Max steps in path (default: 5)")),
		mcp.WithBoolean("error_paths_only", mcp.Description("Only show paths with errors")),
		mcp.WithString("starting_from", mcp.Description("Filter paths starting from this endpoint")),
		// funnels params
		mcp.WithString("funnel_action", mcp.Description("Funnel sub-action: list, create, analyze, delete")),
		mcp.WithString("name", mcp.Description("Funnel name (for create)")),
		mcp.WithObject("steps", mcp.Description("Funnel steps array (for create)")),
		mcp.WithNumber("funnel_id", mcp.Description("Funnel ID (for analyze/delete)")),
		// timeline params
		mcp.WithNumber("log_id", mcp.Description("Log ID for request timeline")),
		mcp.WithNumber("min_duration_ms", mcp.Description("Minimum event duration to include in timeline")),
		// waterfall params
		mcp.WithBoolean("summary_only", mcp.Description("Only show request summaries without events")),
	)
}

// JourneysHandler returns a handler for the consolidated journeys tool.
func JourneysHandler(d JourneysDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		action, _ := args["action"].(string)

		switch action {
		case "sessions":
			return handleSessions(ctx, d, args)
		case "paths":
			return handlePaths(ctx, d, args)
		case "funnels":
			return handleFunnels(ctx, d, args)
		case "timeline":
			return handleRequestTimeline(ctx, d, args)
		case "waterfall":
			return handleSessionWaterfall(ctx, d, args)
		default:
			return mcp.NewToolResultError(fmt.Sprintf("unknown action: %s (use sessions, paths, funnels, timeline, waterfall)", action)), nil
		}
	}
}

func handleSessions(ctx context.Context, d JourneysDeps, args map[string]any) (*mcp.CallToolResult, error) {
	userID, _ := args["user_id"].(string)
	sessionID, _ := args["session_id"].(string)

	sinceStr := "24h"
	if v, ok := args["since"].(string); ok && v != "" {
		sinceStr = v
	}
	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	duration, err := ParseTimeRange(sinceStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
	}
	since := time.Now().UTC().Add(-duration)

	resp := map[string]any{}

	if sessionID != "" {
		session, err := d.JourneyStore.GetSession(ctx, sessionID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("session not found: %v", err)), nil
		}

		steps, err := d.JourneyStore.GetSessionRequests(ctx, sessionID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get session requests: %v", err)), nil
		}

		resp["session"] = map[string]any{
			"session_id":    session.SessionID,
			"user_id":       session.UserID,
			"started_at":    session.StartedAt.Format(time.RFC3339),
			"ended_at":      session.EndedAt.Format(time.RFC3339),
			"request_count": session.RequestCount,
			"error_count":   session.ErrorCount,
			"has_error":     session.HasError,
			"entry_path":    session.EntryPath,
			"exit_path":     session.ExitPath,
		}
		resp["steps"] = formatSteps(steps)
	} else if userID != "" {
		sessions, err := d.JourneyStore.ListSessions(ctx, store.SessionListParams{
			UserID: userID,
			Since:  since,
			Limit:  limit,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list sessions: %v", err)), nil
		}

		steps, err := d.JourneyStore.GetUserJourney(ctx, userID, since, limit*10)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get user journey: %v", err)), nil
		}

		resp["user_id"] = userID
		resp["session_count"] = len(sessions)
		resp["sessions"] = formatSessions(sessions)
		resp["recent_steps"] = formatSteps(steps)
	} else {
		sessions, err := d.JourneyStore.ListSessions(ctx, store.SessionListParams{
			Since: since,
			Limit: limit,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list sessions: %v", err)), nil
		}
		resp["sessions"] = formatSessions(sessions)
		resp["total"] = len(sessions)
	}

	resp = WithSuggestions(resp,
		Suggest("journeys", "Drill into a specific request's waterfall", map[string]any{"action": "timeline"}),
		Suggest("journeys", "See common navigation patterns", map[string]any{"action": "paths", "since": sinceStr}),
	)

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func handlePaths(ctx context.Context, d JourneysDeps, args map[string]any) (*mcp.CallToolResult, error) {
	service, _ := args["service"].(string)
	sinceStr := "7d"
	if v, ok := args["since"].(string); ok && v != "" {
		sinceStr = v
	}
	minOccurrences := 5
	if v, ok := args["min_occurrences"].(float64); ok && v > 0 {
		minOccurrences = int(v)
	}
	pathLength := 5
	if v, ok := args["path_length"].(float64); ok && v > 0 {
		pathLength = int(v)
	}
	errorPathsOnly := false
	if v, ok := args["error_paths_only"].(bool); ok {
		errorPathsOnly = v
	}
	startingFrom, _ := args["starting_from"].(string)

	duration, err := ParseTimeRange(sinceStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
	}
	since := time.Now().UTC().Add(-duration)

	paths, err := d.JourneyStore.CommonPaths(ctx, store.PathAnalysisParams{
		Service:        service,
		Since:          since,
		MinOccurrences: minOccurrences,
		PathLength:     pathLength,
		ErrorPathsOnly: errorPathsOnly,
		StartingFrom:   startingFrom,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to analyze paths: %v", err)), nil
	}

	var pathList []map[string]any
	for _, p := range paths {
		pathList = append(pathList, map[string]any{
			"steps":              p.Steps,
			"count":              p.Count,
			"avg_total_duration": round2(p.AvgTotalDuration),
			"error_rate":         round2(p.ErrorRate),
		})
	}
	if pathList == nil {
		pathList = make([]map[string]any, 0)
	}

	resp := map[string]any{
		"paths":       pathList,
		"total_paths": len(pathList),
	}
	resp = WithSuggestions(resp,
		Suggest("journeys", "Drill into a specific user's session", map[string]any{"action": "sessions"}),
		Suggest("journeys", "Define and analyze conversion funnels", map[string]any{"action": "funnels", "funnel_action": "list"}),
	)

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func handleFunnels(ctx context.Context, d JourneysDeps, args map[string]any) (*mcp.CallToolResult, error) {
	funnelAction, _ := args["funnel_action"].(string)
	if funnelAction == "" {
		funnelAction = "list"
	}

	switch funnelAction {
	case "create":
		name, _ := args["name"].(string)
		if name == "" {
			return mcp.NewToolResultError("name is required for create"), nil
		}
		service, _ := args["service"].(string)

		stepsRaw, ok := args["steps"]
		if !ok {
			return mcp.NewToolResultError("steps is required for create"), nil
		}

		var steps []store.FunnelStep
		stepsJSON, err := json.Marshal(stepsRaw)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid steps: %v", err)), nil
		}
		if err := json.Unmarshal(stepsJSON, &steps); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid steps format: %v", err)), nil
		}

		funnel, err := d.JourneyStore.CreateFunnel(ctx, store.Funnel{
			Name:    name,
			Service: service,
			Steps:   steps,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create funnel: %v", err)), nil
		}

		resp := map[string]any{
			"created":   true,
			"funnel_id": funnel.ID,
			"name":      funnel.Name,
			"steps":     funnel.Steps,
		}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil

	case "analyze":
		funnelID, ok := args["funnel_id"].(float64)
		if !ok || funnelID <= 0 {
			return mcp.NewToolResultError("funnel_id is required for analyze"), nil
		}

		sinceStr := "7d"
		if v, ok := args["since"].(string); ok && v != "" {
			sinceStr = v
		}
		duration, err := ParseTimeRange(sinceStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
		}
		since := time.Now().UTC().Add(-duration)

		result, err := d.JourneyStore.AnalyzeFunnel(ctx, int64(funnelID), since)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to analyze funnel: %v", err)), nil
		}

		resp := map[string]any{
			"funnel_name":        result.FunnelName,
			"total_entered":      result.TotalEntered,
			"steps":              result.Steps,
			"overall_conversion": result.OverallConversion,
		}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil

	case "list":
		funnels, err := d.JourneyStore.ListFunnels(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list funnels: %v", err)), nil
		}

		var list []map[string]any
		for _, f := range funnels {
			list = append(list, map[string]any{
				"id":         f.ID,
				"name":       f.Name,
				"service":    f.Service,
				"steps":      len(f.Steps),
				"created_at": f.CreatedAt.Format(time.RFC3339),
			})
		}
		if list == nil {
			list = make([]map[string]any, 0)
		}

		resp := map[string]any{"funnels": list}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil

	case "delete":
		funnelID, ok := args["funnel_id"].(float64)
		if !ok || funnelID <= 0 {
			return mcp.NewToolResultError("funnel_id is required for delete"), nil
		}
		if err := d.JourneyStore.DeleteFunnel(ctx, int64(funnelID)); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to delete funnel: %v", err)), nil
		}
		data, _ := json.Marshal(map[string]any{"deleted": true})
		return mcp.NewToolResultText(string(data)), nil

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown funnel_action: %s (use create, analyze, list, delete)", funnelAction)), nil
	}
}

func handleRequestTimeline(ctx context.Context, d JourneysDeps, args map[string]any) (*mcp.CallToolResult, error) {
	logID, ok := args["log_id"].(float64)
	if !ok || logID <= 0 {
		return mcp.NewToolResultError("log_id is required"), nil
	}

	minDuration := float64(0)
	if v, ok := args["min_duration_ms"].(float64); ok {
		minDuration = v
	}

	rt, err := d.JourneyStore.GetRequestTimeline(ctx, int64(logID))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get timeline: %v", err)), nil
	}

	var events []map[string]any
	var sqlTime, viewTime, cacheTime, httpTime float64
	for _, ev := range rt.Events {
		if ev.DurationMs < minDuration {
			continue
		}
		entry := map[string]any{
			"type":        ev.Type,
			"name":        ev.Name,
			"start_ms":    ev.StartMs,
			"duration_ms": ev.DurationMs,
		}
		if ev.Details != nil {
			entry["details"] = ev.Details
		}
		events = append(events, entry)

		switch ev.Type {
		case "sql":
			sqlTime += ev.DurationMs
		case "view":
			viewTime += ev.DurationMs
		case "cache":
			cacheTime += ev.DurationMs
		case "http":
			httpTime += ev.DurationMs
		}
	}
	if events == nil {
		events = make([]map[string]any, 0)
	}

	otherTime := rt.DurationMs - sqlTime - viewTime - cacheTime - httpTime
	if otherTime < 0 {
		otherTime = 0
	}

	resp := map[string]any{
		"request": map[string]any{
			"log_id":            rt.LogID,
			"controller":        rt.Controller,
			"action":            rt.Action,
			"path":              rt.Path,
			"status":            rt.Status,
			"total_duration_ms": rt.DurationMs,
			"started_at":        rt.StartedAt.Format(time.RFC3339),
		},
		"events":      events,
		"event_count": len(events),
		"summary": map[string]any{
			"sql_time_ms":   round2(sqlTime),
			"view_time_ms":  round2(viewTime),
			"cache_time_ms": round2(cacheTime),
			"http_time_ms":  round2(httpTime),
			"other_time_ms": round2(otherTime),
		},
	}

	if rt.Bottleneck != nil {
		pctOfTotal := float64(0)
		if rt.DurationMs > 0 {
			pctOfTotal = rt.Bottleneck.DurationMs / rt.DurationMs * 100
		}
		resp["bottleneck"] = map[string]any{
			"type":         rt.Bottleneck.Type,
			"name":         rt.Bottleneck.Name,
			"duration_ms":  rt.Bottleneck.DurationMs,
			"pct_of_total": round2(pctOfTotal),
		}
	}

	resp = WithSuggestions(resp,
		Suggest("journeys", "See the full session this request belongs to", map[string]any{"action": "sessions"}),
		Suggest("log_context", "View surrounding log entries", map[string]any{"log_id": int64(logID)}),
	)

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func handleSessionWaterfall(ctx context.Context, d JourneysDeps, args map[string]any) (*mcp.CallToolResult, error) {
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return mcp.NewToolResultError("session_id is required"), nil
	}

	summaryOnly := false
	if v, ok := args["summary_only"].(bool); ok {
		summaryOnly = v
	}

	timelines, err := d.JourneyStore.GetSessionTimeline(ctx, sessionID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get session timeline: %v", err)), nil
	}

	var totalDuration float64
	var requests []map[string]any
	for _, rt := range timelines {
		totalDuration += rt.DurationMs
		entry := map[string]any{
			"log_id":      rt.LogID,
			"controller":  rt.Controller,
			"action":      rt.Action,
			"path":        rt.Path,
			"status":      rt.Status,
			"duration_ms": rt.DurationMs,
			"event_count": len(rt.Events),
		}
		if rt.Bottleneck != nil {
			entry["bottleneck"] = fmt.Sprintf("%s: %s (%.0fms)", rt.Bottleneck.Type, rt.Bottleneck.Name, rt.Bottleneck.DurationMs)
		}
		if !summaryOnly && len(rt.Events) > 0 {
			var events []map[string]any
			for _, ev := range rt.Events {
				events = append(events, map[string]any{
					"type":        ev.Type,
					"name":        ev.Name,
					"start_ms":    ev.StartMs,
					"duration_ms": ev.DurationMs,
				})
			}
			entry["events"] = events
		}
		requests = append(requests, entry)
	}
	if requests == nil {
		requests = make([]map[string]any, 0)
	}

	resp := map[string]any{
		"session_id":        sessionID,
		"total_requests":    len(timelines),
		"total_duration_ms": round2(totalDuration),
		"requests":          requests,
	}

	resp = WithSuggestions(resp,
		Suggest("journeys", "See session details and user info", map[string]any{"action": "sessions", "session_id": sessionID}),
		Suggest("journeys", "Drill into a specific request", map[string]any{"action": "timeline"}),
	)

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func formatSteps(steps []store.RequestStep) []map[string]any {
	result := make([]map[string]any, 0, len(steps))
	for _, s := range steps {
		entry := map[string]any{
			"timestamp":   s.Timestamp.Format(time.RFC3339),
			"controller":  s.Controller,
			"action":      s.Action,
			"method":      s.Method,
			"status":      s.Status,
			"duration_ms": s.DurationMs,
			"log_id":      s.LogID,
		}
		if s.Path != "" {
			entry["path"] = s.Path
		}
		if s.HasError {
			entry["has_error"] = true
		}
		if s.ErrorClass != "" {
			entry["error_class"] = s.ErrorClass
		}
		if s.SQLCount > 0 {
			entry["sql_count"] = s.SQLCount
		}
		result = append(result, entry)
	}
	return result
}

func formatSessions(sessions []store.UserSession) []map[string]any {
	result := make([]map[string]any, 0, len(sessions))
	for _, s := range sessions {
		entry := map[string]any{
			"session_id":    s.SessionID,
			"started_at":    s.StartedAt.Format(time.RFC3339),
			"ended_at":      s.EndedAt.Format(time.RFC3339),
			"request_count": s.RequestCount,
			"has_error":     s.HasError,
		}
		if s.UserID != "" {
			entry["user_id"] = s.UserID
		}
		if s.EntryPath != "" {
			entry["entry_path"] = s.EntryPath
		}
		if s.ExitPath != "" {
			entry["exit_path"] = s.ExitPath
		}
		result = append(result, entry)
	}
	return result
}
