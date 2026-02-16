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

// listHealthchecksHandler lists all configured health checks with their current status.
func listHealthchecksHandler(hcs store.HealthCheckStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if hcs == nil {
			return mcp.NewToolResultError("HealthCheckStore not configured"), nil
		}

		checks, err := hcs.List(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list health checks: %v", err)), nil
		}

		if len(checks) == 0 {
			return mcp.NewToolResultText("No health checks configured. Use create_healthcheck to add one."), nil
		}

		type checkSummary struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			URL            string `json:"url"`
			Method         string `json:"method"`
			IntervalSecs   int    `json:"interval_secs"`
			ExpectedStatus int    `json:"expected_status"`
			Enabled        bool   `json:"enabled"`
			CurrentStatus  string `json:"current_status,omitempty"`
			LastResponseMs *int   `json:"last_response_ms,omitempty"`
			LastCheckedAt  string `json:"last_checked_at,omitempty"`
		}

		summaries := make([]checkSummary, len(checks))
		for i, c := range checks {
			s := checkSummary{
				ID:             c.ID,
				Name:           c.Name,
				URL:            c.URL,
				Method:         c.Method,
				IntervalSecs:   c.IntervalSecs,
				ExpectedStatus: c.ExpectedStatus,
				Enabled:        c.Enabled,
			}

			// Get latest result for current status
			results, _ := hcs.LatestResults(ctx, c.ID, 1)
			if len(results) > 0 {
				s.CurrentStatus = string(results[0].Status)
				s.LastResponseMs = results[0].ResponseMs
				s.LastCheckedAt = results[0].CheckedAt.Format(time.RFC3339)
			}

			summaries[i] = s
		}

		resp := map[string]any{
			"count":         len(summaries),
			"healthchecks": summaries,
		}

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// createHealthcheckHandler creates a new HTTP health check.
func createHealthcheckHandler(hcs store.HealthCheckStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if hcs == nil {
			return mcp.NewToolResultError("HealthCheckStore not configured"), nil
		}

		args := request.GetArguments()

		name, _ := args["name"].(string)
		if name == "" {
			return mcp.NewToolResultError("name is required"), nil
		}
		url, _ := args["url"].(string)
		if url == "" {
			return mcp.NewToolResultError("url is required"), nil
		}

		params := store.CreateHealthCheckParams{
			Name: name,
			URL:  url,
		}
		if v, ok := args["method"].(string); ok && v != "" {
			params.Method = v
		}
		if v, ok := args["interval_secs"].(float64); ok && v > 0 {
			params.IntervalSecs = int(v)
		}
		if v, ok := args["timeout_secs"].(float64); ok && v > 0 {
			params.TimeoutSecs = int(v)
		}
		if v, ok := args["expected_status"].(float64); ok && v > 0 {
			params.ExpectedStatus = int(v)
		}

		hc, err := hcs.Create(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create health check: %v", err)), nil
		}

		resp := map[string]any{
			"id":              hc.ID,
			"name":            hc.Name,
			"url":             hc.URL,
			"method":          hc.Method,
			"interval_secs":   hc.IntervalSecs,
			"timeout_secs":    hc.TimeoutSecs,
			"expected_status": hc.ExpectedStatus,
			"enabled":         hc.Enabled,
			"message":         fmt.Sprintf("Health check '%s' created. It will be probed every %ds.", hc.Name, hc.IntervalSecs),
		}

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// deleteHealthcheckHandler removes a health check and its results.
func deleteHealthcheckHandler(hcs store.HealthCheckStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if hcs == nil {
			return mcp.NewToolResultError("HealthCheckStore not configured"), nil
		}

		args := request.GetArguments()
		id, _ := args["id"].(string)
		if id == "" {
			return mcp.NewToolResultError("id is required"), nil
		}

		if err := hcs.Delete(ctx, id); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to delete health check: %v", err)), nil
		}

		resp := map[string]any{
			"status":  "deleted",
			"id":      id,
			"message": "Health check and all its results have been deleted.",
		}

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// uptimeStatusHandler returns uptime summaries across all health checks.
func uptimeStatusHandler(hcs store.HealthCheckStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if hcs == nil {
			return mcp.NewToolResultError("HealthCheckStore not configured"), nil
		}

		args := request.GetArguments()

		// Default to 24h window
		hours := 24.0
		if v, ok := args["hours"].(float64); ok && v > 0 {
			hours = v
		}
		if hours > 720 { // cap at 30 days
			hours = 720
		}

		since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)

		summaries, err := hcs.UptimeSummaries(ctx, since)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get uptime summaries: %v", err)), nil
		}

		if len(summaries) == 0 {
			return mcp.NewToolResultText("No health checks configured. Use create_healthcheck to add one."), nil
		}

		// Also include recent results for any down checks
		type extSummary struct {
			store.UptimeSummary
			RecentErrors []string `json:"recent_errors,omitempty"`
		}

		extended := make([]extSummary, len(summaries))
		for i, s := range summaries {
			extended[i] = extSummary{UptimeSummary: s}
			if s.CurrentStatus == "down" || s.CurrentStatus == "degraded" {
				results, _ := hcs.LatestResults(ctx, s.HealthCheckID, 3)
				for _, r := range results {
					if r.Error != "" {
						extended[i].RecentErrors = append(extended[i].RecentErrors, r.Error)
					}
				}
			}
		}

		resp := map[string]any{
			"window_hours": hours,
			"since":        since.Format(time.RFC3339),
			"count":        len(extended),
			"endpoints":    extended,
		}

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}
