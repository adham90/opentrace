package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"


	"github.com/adham90/opentrace/pkg/store"
)

// HealthchecksDeps holds the stores needed by the healthchecks tool.
type HealthchecksDeps struct {
	HealthCheckStore store.HealthCheckStore
}

// HealthchecksHandler returns a handler for the consolidated healthchecks tool.
func HealthchecksHandler(d HealthchecksDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		if d.HealthCheckStore == nil {
			return NewToolResultError("HealthCheckStore not configured"), nil
		}

		args := GetArguments(request)
		action, _ := args["action"].(string)

		switch action {
		case "list":
			return handleHealthcheckList(ctx, d)
		case "uptime":
			return handleHealthcheckUptime(ctx, d, args)
		case "create":
			return handleHealthcheckCreate(ctx, d, args)
		case "delete":
			return handleHealthcheckDelete(ctx, d, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use list, uptime, create, delete)", action)), nil
		}
	}
}

func handleHealthcheckList(ctx context.Context, d HealthchecksDeps) (*CallToolResult, error) {
	checks, err := d.HealthCheckStore.List(ctx, store.ListHealthCheckParams{})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list health checks: %v", err)), nil
	}

	if len(checks) == 0 {
		return NewToolResultText("No health checks configured. Use healthchecks with action=create to add one."), nil
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

		results, _ := d.HealthCheckStore.LatestResults(ctx, c.ID, 1)
		if len(results) > 0 {
			s.CurrentStatus = string(results[0].Status)
			s.LastResponseMs = results[0].ResponseMs
			s.LastCheckedAt = results[0].CheckedAt.Format(time.RFC3339)
		}

		summaries[i] = s
	}

	resp := map[string]any{
		"count":        len(summaries),
		"healthchecks": summaries,
	}

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

func handleHealthcheckUptime(ctx context.Context, d HealthchecksDeps, args map[string]any) (*CallToolResult, error) {
	hours := 24.0
	if v, ok := args["hours"].(float64); ok && v > 0 {
		hours = v
	}
	if hours > 720 {
		hours = 720
	}

	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)

	summaries, err := d.HealthCheckStore.UptimeSummaries(ctx, since)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get uptime summaries: %v", err)), nil
	}

	if len(summaries) == 0 {
		return NewToolResultText("No health checks configured. Use healthchecks with action=create to add one."), nil
	}

	type extSummary struct {
		store.UptimeSummary
		RecentErrors []string `json:"recent_errors,omitempty"`
	}

	extended := make([]extSummary, len(summaries))
	for i, s := range summaries {
		extended[i] = extSummary{UptimeSummary: s}
		if s.CurrentStatus == "down" || s.CurrentStatus == "degraded" {
			results, _ := d.HealthCheckStore.LatestResults(ctx, s.HealthCheckID, 3)
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

	var suggestions []ToolSuggestion
	for _, ep := range extended {
		if ep.CurrentStatus == "down" || ep.CurrentStatus == "degraded" {
			suggestions = append(suggestions, Suggest("overview", fmt.Sprintf("Investigate '%s' — currently %s", ep.Name, ep.CurrentStatus), map[string]any{"action": "diagnose"}))
			break
		}
	}
	WithSuggestions(resp, suggestions...)

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

func handleHealthcheckCreate(ctx context.Context, d HealthchecksDeps, args map[string]any) (*CallToolResult, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return NewToolResultError("name is required"), nil
	}
	url, _ := args["url"].(string)
	if url == "" {
		return NewToolResultError("url is required"), nil
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

	hc, err := d.HealthCheckStore.Create(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to create health check: %v", err)), nil
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
	return NewToolResultText(string(data)), nil
}

func handleHealthcheckDelete(ctx context.Context, d HealthchecksDeps, args map[string]any) (*CallToolResult, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return NewToolResultError("id is required"), nil
	}

	if err := d.HealthCheckStore.Delete(ctx, id); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to delete health check: %v", err)), nil
	}

	resp := map[string]any{
		"status":  "deleted",
		"id":      id,
		"message": "Health check and all its results have been deleted.",
	}

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}
