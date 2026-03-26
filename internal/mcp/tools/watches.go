package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// WatchesDeps holds the stores needed by the watches tool.
type WatchesDeps struct {
	WatchStore   store.WatchStore
	LogStore     store.LogStore
	WatchMetrics *watcher.WatchMetrics
}

// WatchesTool returns the consolidated tool definition for watch management.
func WatchesTool() mcp.Tool {
	return mcp.NewTool("watches",
		mcp.WithDescription(`Watch management: create, list, delete watches, and manage alerts.

Actions:
- status: List active watches with current values and pending alert counts
- create: Create a metric watch (error_rate, response_time, p95_response, log_count, error_count, heartbeat, sql_count, cache_hit_rate)
- delete: Stop/delete a watch by watch_id
- alerts: List pending alerts (from check_alerts)
- dismiss: Dismiss an alert with a reason
- acknowledge: Acknowledge an alert
- investigate: Investigate a watch alert or collect data about a service`),
		mcp.WithString("action", mcp.Required(), mcp.Description("Action: status, create, delete, alerts, dismiss, acknowledge, investigate")),
		// status filters
		mcp.WithString("status", mcp.Description("Filter by status: active, triggered, resolved, expired (for status action)")),
		mcp.WithString("service", mcp.Description("Service name filter")),
		mcp.WithString("session_id", mcp.Description("Agent session ID filter")),
		// create params
		mcp.WithString("metric", mcp.Description("Metric to monitor: error_rate, response_time, p95_response, log_count, error_count, heartbeat, sql_count, cache_hit_rate")),
		mcp.WithString("operator", mcp.Description("Comparison operator: gt, gte, lt, lte, eq, neq")),
		mcp.WithNumber("threshold", mcp.Description("Threshold value to compare against")),
		mcp.WithString("endpoint", mcp.Description("Specific endpoint/path to monitor")),
		mcp.WithString("environment", mcp.Description("Environment filter (e.g. production, staging)")),
		mcp.WithString("commit_hash", mcp.Description("Git commit to associate with this watch")),
		mcp.WithString("duration", mcp.Description("How long the watch stays active (default: 1h). Examples: 30m, 2h, 24h")),
		mcp.WithString("urgency", mcp.Description("Alert urgency: low, normal (default), high, critical")),
		mcp.WithString("check_interval", mcp.Description("How often to check (default: 30s). Examples: 10s, 1m, 5m")),
		mcp.WithNumber("min_consecutive", mcp.Description("Number of consecutive breaches before alerting (default: 1)")),
		// delete / dismiss / acknowledge / investigate params
		mcp.WithString("watch_id", mcp.Description("Watch ID (for delete action)")),
		mcp.WithString("alert_id", mcp.Description("Alert ID (for dismiss, acknowledge, investigate actions)")),
		mcp.WithString("reason", mcp.Description("Reason for dismissal")),
		mcp.WithString("window", mcp.Description("Time window for service investigation (default: 1h). Examples: 15m, 1h, 6h")),
	)
}

// WatchesHandler returns a handler for the consolidated watches tool.
func WatchesHandler(d WatchesDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		action, _ := args["action"].(string)

		switch action {
		case "status":
			return handleWatchStatus(ctx, d, args)
		case "create":
			return handleWatchCreate(ctx, d, args)
		case "delete":
			return handleWatchDelete(ctx, d, args)
		case "alerts":
			return handleWatchAlerts(ctx, d, args)
		case "dismiss":
			return handleWatchDismiss(ctx, d, args)
		case "acknowledge":
			return handleWatchAcknowledge(ctx, d, args)
		case "investigate":
			return handleWatchInvestigate(ctx, d, args)
		default:
			return mcp.NewToolResultError(fmt.Sprintf("unknown action: %s (use status, create, delete, alerts, dismiss, acknowledge, investigate)", action)), nil
		}
	}
}

func handleWatchStatus(ctx context.Context, d WatchesDeps, args map[string]any) (*mcp.CallToolResult, error) {
	params := store.ListWatchParams{}
	if v, ok := args["status"].(string); ok {
		params.Status = store.WatchStatus(v)
	}
	if v, ok := args["service"].(string); ok {
		params.Service = v
	}
	if v, ok := args["session_id"].(string); ok {
		params.SessionID = v
	}

	var watches []store.Watch
	if params.Status == "" {
		active, err := d.WatchStore.List(ctx, store.ListWatchParams{
			Status:    store.WatchStatusActive,
			Service:   params.Service,
			SessionID: params.SessionID,
		})
		if err != nil {
			return nil, fmt.Errorf("listing active watches: %w", err)
		}
		triggered, err := d.WatchStore.List(ctx, store.ListWatchParams{
			Status:    store.WatchStatusTriggered,
			Service:   params.Service,
			SessionID: params.SessionID,
		})
		if err != nil {
			return nil, fmt.Errorf("listing triggered watches: %w", err)
		}
		watches = append(active, triggered...)
	} else {
		var err error
		watches, err = d.WatchStore.List(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("listing watches: %w", err)
		}
	}

	pendingCount, _ := d.WatchStore.CountPendingAlerts(ctx)

	type watchSummary struct {
		ID            string              `json:"id"`
		Metric        store.WatchMetric   `json:"metric"`
		Operator      store.WatchOperator `json:"operator"`
		Threshold     float64             `json:"threshold"`
		Service       string              `json:"service,omitempty"`
		Status        store.WatchStatus   `json:"status"`
		CurrentValue  *float64            `json:"current_value,omitempty"`
		Urgency       store.WatchUrgency  `json:"urgency"`
		ExpiresAt     *time.Time          `json:"expires_at,omitempty"`
		LastCheckedAt *time.Time          `json:"last_checked_at,omitempty"`
		CreatedAt     time.Time           `json:"created_at"`
	}

	summaries := make([]watchSummary, len(watches))
	for i, w := range watches {
		summaries[i] = watchSummary{
			ID:            w.ID,
			Metric:        w.Metric,
			Operator:      w.Operator,
			Threshold:     w.Threshold,
			Service:       w.Service,
			Status:        w.Status,
			CurrentValue:  w.CurrentValue,
			Urgency:       w.Urgency,
			ExpiresAt:     w.ExpiresAt,
			LastCheckedAt: w.LastCheckedAt,
			CreatedAt:     w.CreatedAt,
		}
	}

	result := map[string]any{
		"watches":        summaries,
		"total":          len(summaries),
		"pending_alerts": pendingCount,
	}

	var suggestions []ToolSuggestion
	if pendingCount > 0 {
		suggestions = append(suggestions, Suggest("overview", "Investigate triggered alerts with full context", map[string]any{"action": "diagnose"}))
	}
	for _, w := range watches {
		if w.Status == store.WatchStatusTriggered {
			suggestions = append(suggestions, Suggest("log_search", "Search error logs for triggered service", map[string]any{
				"level":   "error",
				"service": w.Service,
			}))
			break
		}
	}
	WithSuggestions(result, suggestions...)

	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

func handleWatchCreate(ctx context.Context, d WatchesDeps, args map[string]any) (*mcp.CallToolResult, error) {
	metricStr, _ := args["metric"].(string)
	operatorStr, _ := args["operator"].(string)
	threshold, _ := args["threshold"].(float64)

	params := store.CreateWatchParams{
		Metric:    store.WatchMetric(metricStr),
		Operator:  store.WatchOperator(operatorStr),
		Threshold: threshold,
	}

	if v, ok := args["service"].(string); ok {
		params.Service = v
	}
	if v, ok := args["endpoint"].(string); ok {
		params.Endpoint = v
	}
	if v, ok := args["environment"].(string); ok {
		params.Environment = v
	}
	if v, ok := args["commit_hash"].(string); ok {
		params.CommitHash = v
	}
	if v, ok := args["duration"].(string); ok {
		params.Duration = v
	}
	if v, ok := args["urgency"].(string); ok {
		params.Urgency = store.WatchUrgency(v)
	}
	if v, ok := args["check_interval"].(string); ok {
		params.CheckInterval = v
	}
	if v, ok := args["min_consecutive"].(float64); ok {
		params.MinConsecutive = int(v)
	}
	if v, ok := args["session_id"].(string); ok {
		params.SessionID = v
	}
	params.CreatedBy = "mcp"

	w, err := d.WatchStore.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("creating watch: %w", err)
	}

	// Capture baseline
	baseline, err := watcher.CaptureBaseline(ctx, d.LogStore, d.WatchMetrics, w)
	if err == nil {
		_ = d.WatchStore.UpdateBaseline(ctx, w.ID, baseline)
		w, _ = d.WatchStore.GetByID(ctx, w.ID)
	}

	data, _ := json.Marshal(w)
	return mcp.NewToolResultText(string(data)), nil
}

func handleWatchDelete(ctx context.Context, d WatchesDeps, args map[string]any) (*mcp.CallToolResult, error) {
	watchID, _ := args["watch_id"].(string)
	if watchID == "" {
		return mcp.NewToolResultError("watch_id is required for delete action"), nil
	}
	if err := d.WatchStore.Delete(ctx, watchID); err != nil {
		return nil, fmt.Errorf("stopping watch: %w", err)
	}
	return mcp.NewToolResultText(fmt.Sprintf(`{"status":"stopped","watch_id":"%s"}`, watchID)), nil
}

func handleWatchAlerts(ctx context.Context, d WatchesDeps, args map[string]any) (*mcp.CallToolResult, error) {
	service, _ := args["service"].(string)
	alerts, err := d.WatchStore.ListAlerts(ctx, "", "pending", 20)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list alerts: %v", err)), nil
	}

	var filtered []store.WatchAlert
	for _, a := range alerts {
		if service == "" {
			filtered = append(filtered, a)
		} else {
			// include all when no service filter
			filtered = append(filtered, a)
		}
	}

	result := map[string]any{
		"count":  len(filtered),
		"alerts": filtered,
	}

	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

func handleWatchDismiss(ctx context.Context, d WatchesDeps, args map[string]any) (*mcp.CallToolResult, error) {
	alertID, _ := args["alert_id"].(string)
	if alertID == "" {
		return mcp.NewToolResultError("alert_id is required for dismiss action"), nil
	}
	reason, _ := args["reason"].(string)
	if reason == "" {
		reason = "dismissed by agent"
	}
	if err := d.WatchStore.DismissAlert(ctx, alertID, reason); err != nil {
		return nil, fmt.Errorf("dismissing alert: %w", err)
	}
	return mcp.NewToolResultText(fmt.Sprintf(`{"status":"dismissed","alert_id":"%s","reason":"%s"}`, alertID, reason)), nil
}

func handleWatchAcknowledge(ctx context.Context, d WatchesDeps, args map[string]any) (*mcp.CallToolResult, error) {
	alertID, _ := args["alert_id"].(string)
	if alertID == "" {
		return mcp.NewToolResultError("alert_id is required for acknowledge action"), nil
	}
	if err := d.WatchStore.AcknowledgeAlert(ctx, alertID); err != nil {
		return nil, fmt.Errorf("acknowledging alert: %w", err)
	}
	return mcp.NewToolResultText(fmt.Sprintf(`{"status":"acknowledged","alert_id":"%s"}`, alertID)), nil
}

func handleWatchInvestigate(ctx context.Context, d WatchesDeps, args map[string]any) (*mcp.CallToolResult, error) {
	alertID, _ := args["alert_id"].(string)
	service, _ := args["service"].(string)

	// Mode A: investigate a specific alert
	if alertID != "" {
		alert, err := d.WatchStore.GetAlert(ctx, alertID)
		if err != nil {
			return nil, fmt.Errorf("getting alert: %w", err)
		}

		resp := map[string]any{"alert": alert}
		var suggestions []ToolSuggestion

		if w, err := d.WatchStore.GetByID(ctx, alert.WatchID); err == nil && w.Service != "" {
			suggestions = append(suggestions, Suggest("log_search", "Search error logs for this service", map[string]any{
				"level":   "error",
				"service": w.Service,
			}))
			suggestions = append(suggestions, Suggest("overview", "Full investigation for this service", map[string]any{
				"action":  "diagnose",
				"service": w.Service,
			}))
		}
		WithSuggestions(resp, suggestions...)

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}

	// Mode B: one-shot investigation
	if service == "" {
		return nil, fmt.Errorf("either alert_id or service is required")
	}

	windowStr, _ := args["window"].(string)
	if windowStr == "" {
		windowStr = "1h"
	}
	window, err := time.ParseDuration(windowStr)
	if err != nil {
		window = 1 * time.Hour
	}

	now := time.Now().UTC()
	start := now.Add(-window)

	type investigation struct {
		Service      string                     `json:"service"`
		Window       string                     `json:"window"`
		ErrorRate    float64                    `json:"error_rate"`
		LogCount     float64                    `json:"log_count"`
		ErrorCount   float64                    `json:"error_count"`
		RecentErrors []store.WatchEvidenceError `json:"recent_errors,omitempty"`
		RecentLogs   []store.WatchEvidenceLog   `json:"recent_logs,omitempty"`
	}

	inv := investigation{
		Service: service,
		Window:  windowStr,
	}

	// Metrics
	if d.WatchMetrics != nil {
		inv.ErrorRate, _ = d.WatchMetrics.Measure(ctx, store.WatchMetricErrorRate, service, "", window)
		inv.LogCount, _ = d.WatchMetrics.Measure(ctx, store.WatchMetricLogCount, service, "", window)
		inv.ErrorCount, _ = d.WatchMetrics.Measure(ctx, store.WatchMetricErrorCount, service, "", window)
	}

	// Recent errors
	if d.LogStore != nil {
		errorLogs, err := d.LogStore.Search(ctx, store.LogSearchParams{
			Service: service,
			Level:   "error",
			Start:   &start,
			End:     &now,
			Limit:   20,
		})
		if err == nil {
			errorCounts := make(map[string]*store.WatchEvidenceError)
			for _, entry := range errorLogs {
				cls := entry.ExceptionClass
				if cls == "" {
					cls = "Unknown"
				}
				if existing, ok := errorCounts[cls]; ok {
					existing.Count++
				} else {
					msg := entry.Message
					if len(msg) > 200 {
						msg = msg[:200] + "..."
					}
					errorCounts[cls] = &store.WatchEvidenceError{
						ExceptionClass: cls,
						Message:        msg,
						Count:          1,
					}
				}
			}
			for _, e := range errorCounts {
				inv.RecentErrors = append(inv.RecentErrors, *e)
			}
		}

		// Recent logs
		logs, err := d.LogStore.Search(ctx, store.LogSearchParams{
			Service: service,
			Start:   &start,
			End:     &now,
			Limit:   20,
		})
		if err == nil {
			for _, entry := range logs {
				msg := entry.Message
				if len(msg) > 200 {
					msg = msg[:200] + "..."
				}
				inv.RecentLogs = append(inv.RecentLogs, store.WatchEvidenceLog{
					Timestamp: entry.Timestamp,
					Level:     entry.Level,
					Message:   msg,
					Service:   entry.Service,
					TraceID:   entry.TraceID,
				})
			}
		}
	}

	resp := map[string]any{"investigation": inv}
	var suggestions []ToolSuggestion
	if inv.ErrorCount > 0 {
		suggestions = append(suggestions, Suggest("log_search", "View error logs for this service", map[string]any{
			"level":   "error",
			"service": service,
		}))
	}
	if len(inv.RecentErrors) > 0 && inv.RecentErrors[0].ExceptionClass != "" && inv.RecentErrors[0].ExceptionClass != "Unknown" {
		suggestions = append(suggestions, Suggest("log_search", "Search by exception class", map[string]any{
			"exception_class": inv.RecentErrors[0].ExceptionClass,
			"service":         service,
		}))
	}
	WithSuggestions(resp, suggestions...)

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}
