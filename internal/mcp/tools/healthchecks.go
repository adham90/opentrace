package tools

import (
	"context"
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
		action := ArgString(args, "action")

		switch action {
		case "list":
			return HandleHealthcheckList(ctx, d, args)
		case "uptime":
			return HandleHealthcheckUptime(ctx, d, args)
		case "create":
			return HandleHealthcheckCreate(ctx, d, args)
		case "delete":
			return HandleHealthcheckDelete(ctx, d, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use list, uptime, create, delete)", action)), nil
		}
	}
}

func HandleHealthcheckList(ctx context.Context, d HealthchecksDeps, args map[string]any) (*CallToolResult, error) {
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}
	checks, err := d.HealthCheckStore.List(ctx, store.ListHealthCheckParams{Environment: env})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list health checks: %v", err)), nil
	}

	if len(checks) == 0 {
		return EmptyResult("No health checks configured. Use healthchecks with action=create to add one.")
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

	return JSONResult(map[string]any{
		"count":        len(summaries),
		"healthchecks": summaries,
	})
}

func HandleHealthcheckUptime(ctx context.Context, d HealthchecksDeps, args map[string]any) (*CallToolResult, error) {
	hours := ArgFloat(args, "hours", 24.0)
	if hours > 720 {
		hours = 720
	}

	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)

	summaries, err := d.HealthCheckStore.UptimeSummaries(ctx, since)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get uptime summaries: %v", err)), nil
	}

	if len(summaries) == 0 {
		return EmptyResult("No health checks configured. Use healthchecks with action=create to add one.")
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

	return JSONResult(resp, suggestions...)
}

func HandleHealthcheckCreate(ctx context.Context, d HealthchecksDeps, args map[string]any) (*CallToolResult, error) {
	name := ArgString(args, "name")
	if name == "" {
		return NewToolResultError("name is required"), nil
	}
	url := ArgString(args, "url")
	if url == "" {
		return NewToolResultError("url is required"), nil
	}

	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	params := store.CreateHealthCheckParams{
		Name:        name,
		URL:         url,
		Environment: env,
	}
	if v := ArgString(args, "method"); v != "" {
		params.Method = v
	}
	if v := ArgInt(args, "interval_secs", 0, 86400); v > 0 {
		params.IntervalSecs = v
	}
	if v := ArgInt(args, "timeout_secs", 0, 300); v > 0 {
		params.TimeoutSecs = v
	}
	if v := ArgInt(args, "expected_status", 0, 599); v > 0 {
		params.ExpectedStatus = v
	}

	hc, err := d.HealthCheckStore.Create(ctx, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to create health check: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"id":              hc.ID,
		"name":            hc.Name,
		"url":             hc.URL,
		"method":          hc.Method,
		"interval_secs":   hc.IntervalSecs,
		"timeout_secs":    hc.TimeoutSecs,
		"expected_status": hc.ExpectedStatus,
		"enabled":         hc.Enabled,
		"message":         fmt.Sprintf("Health check '%s' created. It will be probed every %ds.", hc.Name, hc.IntervalSecs),
	})
}

func HandleHealthcheckDelete(ctx context.Context, d HealthchecksDeps, args map[string]any) (*CallToolResult, error) {
	id := ArgString(args, "id")
	if id == "" {
		return NewToolResultError("id is required"), nil
	}

	if err := d.HealthCheckStore.Delete(ctx, id); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to delete health check: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"status":  "deleted",
		"id":      id,
		"message": "Health check and all its results have been deleted.",
	})
}
