package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// userJourneyHandler reconstructs user sessions and request steps.
func userJourneyHandler(js store.JourneyStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

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

		duration, err := parseTimeRange(sinceStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
		}
		since := time.Now().UTC().Add(-duration)

		resp := map[string]any{}

		if sessionID != "" {
			// Get specific session with its request steps
			session, err := js.GetSession(ctx, sessionID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("session not found: %v", err)), nil
			}

			steps, err := js.GetSessionRequests(ctx, sessionID)
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
			// Get user's sessions and recent journey
			sessions, err := js.ListSessions(ctx, store.SessionListParams{
				UserID: userID,
				Since:  since,
				Limit:  limit,
			})
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to list sessions: %v", err)), nil
			}

			steps, err := js.GetUserJourney(ctx, userID, since, limit*10)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to get user journey: %v", err)), nil
			}

			resp["user_id"] = userID
			resp["session_count"] = len(sessions)
			resp["sessions"] = formatSessions(sessions)
			resp["recent_steps"] = formatSteps(steps)
		} else {
			// List recent sessions
			sessions, err := js.ListSessions(ctx, store.SessionListParams{
				Since: since,
				Limit: limit,
			})
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to list sessions: %v", err)), nil
			}
			resp["sessions"] = formatSessions(sessions)
			resp["total"] = len(sessions)
		}

		resp = withSuggestions(resp,
			suggest("request_timeline", "Drill into a specific request's waterfall", map[string]any{}),
			suggest("path_analysis", "See common navigation patterns", map[string]any{"since": sinceStr}),
		)

		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}

// pathAnalysisHandler discovers common user navigation paths.
func pathAnalysisHandler(js store.JourneyStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

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

		duration, err := parseTimeRange(sinceStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
		}
		since := time.Now().UTC().Add(-duration)

		paths, err := js.CommonPaths(ctx, store.PathAnalysisParams{
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
		resp = withSuggestions(resp,
			suggest("user_journey", "Drill into a specific user's session", map[string]any{}),
			suggest("funnel_analysis", "Define and analyze conversion funnels", map[string]any{"action": "list"}),
		)

		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}

// funnelAnalysisHandler manages and analyzes conversion funnels.
func funnelAnalysisHandler(js store.JourneyStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
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

			funnel, err := js.CreateFunnel(ctx, store.Funnel{
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
			data, _ := json.MarshalIndent(resp, "", "  ")
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
			duration, err := parseTimeRange(sinceStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid since: %v", err)), nil
			}
			since := time.Now().UTC().Add(-duration)

			result, err := js.AnalyzeFunnel(ctx, int64(funnelID), since)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to analyze funnel: %v", err)), nil
			}

			resp := map[string]any{
				"funnel_name":        result.FunnelName,
				"total_entered":      result.TotalEntered,
				"steps":              result.Steps,
				"overall_conversion": result.OverallConversion,
			}
			data, _ := json.MarshalIndent(resp, "", "  ")
			return mcp.NewToolResultText(string(data)), nil

		case "list":
			funnels, err := js.ListFunnels(ctx)
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
			data, _ := json.MarshalIndent(resp, "", "  ")
			return mcp.NewToolResultText(string(data)), nil

		case "delete":
			funnelID, ok := args["funnel_id"].(float64)
			if !ok || funnelID <= 0 {
				return mcp.NewToolResultError("funnel_id is required for delete"), nil
			}
			if err := js.DeleteFunnel(ctx, int64(funnelID)); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to delete funnel: %v", err)), nil
			}
			data, _ := json.MarshalIndent(map[string]any{"deleted": true}, "", "  ")
			return mcp.NewToolResultText(string(data)), nil

		default:
			return mcp.NewToolResultError(fmt.Sprintf("unknown action: %s (use create, analyze, list, delete)", action)), nil
		}
	}
}

// requestTimelineHandler returns a waterfall analysis of a single request.
func requestTimelineHandler(js store.JourneyStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		logID, ok := args["log_id"].(float64)
		if !ok || logID <= 0 {
			return mcp.NewToolResultError("log_id is required"), nil
		}

		minDuration := float64(0)
		if v, ok := args["min_duration_ms"].(float64); ok {
			minDuration = v
		}

		rt, err := js.GetRequestTimeline(ctx, int64(logID))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get timeline: %v", err)), nil
		}

		// Filter events by min duration
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

		resp = withSuggestions(resp,
			suggest("user_journey", "See the full session this request belongs to", map[string]any{}),
			suggest("log_context", "View surrounding log entries", map[string]any{"log_id": int64(logID)}),
		)

		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}

// sessionWaterfallHandler returns timelines for all requests in a session.
func sessionWaterfallHandler(js store.JourneyStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		sessionID, _ := args["session_id"].(string)
		if sessionID == "" {
			return mcp.NewToolResultError("session_id is required"), nil
		}

		summaryOnly := false
		if v, ok := args["summary_only"].(bool); ok {
			summaryOnly = v
		}

		timelines, err := js.GetSessionTimeline(ctx, sessionID)
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

		resp = withSuggestions(resp,
			suggest("user_journey", "See session details and user info", map[string]any{"session_id": sessionID}),
			suggest("request_timeline", "Drill into a specific request", map[string]any{}),
		)

		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
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
