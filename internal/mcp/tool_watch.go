package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// --- watch (write tool) ---

func watchTool() mcp.Tool {
	return mcp.NewTool("watch",
		mcp.WithDescription(`Create a metric watch that monitors a service and alerts when a threshold is breached.

Metrics: error_rate, response_time, p95_response, log_count, error_count, heartbeat, sql_count, cache_hit_rate
Operators: gt, gte, lt, lte, eq, neq

Examples:
- Watch error rate: metric=error_rate, operator=gt, threshold=0.05, service=web
- Watch response time: metric=p95_response, operator=gt, threshold=500, service=api
- Watch for silence: metric=heartbeat, operator=gt, threshold=120, service=worker`),
		mcp.WithString("metric", mcp.Required(), mcp.Description("Metric to monitor: error_rate, response_time, p95_response, log_count, error_count, heartbeat, sql_count, cache_hit_rate")),
		mcp.WithString("operator", mcp.Required(), mcp.Description("Comparison operator: gt, gte, lt, lte, eq, neq")),
		mcp.WithNumber("threshold", mcp.Required(), mcp.Description("Threshold value to compare against")),
		mcp.WithString("service", mcp.Description("Service name to scope the watch to")),
		mcp.WithString("endpoint", mcp.Description("Specific endpoint/path to monitor")),
		mcp.WithString("environment", mcp.Description("Environment filter (e.g. production, staging)")),
		mcp.WithString("commit_hash", mcp.Description("Git commit to associate with this watch")),
		mcp.WithString("duration", mcp.Description("How long the watch stays active (default: 1h). Examples: 30m, 2h, 24h")),
		mcp.WithString("urgency", mcp.Description("Alert urgency: low, normal (default), high, critical")),
		mcp.WithString("check_interval", mcp.Description("How often to check (default: 30s). Examples: 10s, 1m, 5m")),
		mcp.WithNumber("min_consecutive", mcp.Description("Number of consecutive breaches before alerting (default: 1)")),
		mcp.WithString("session_id", mcp.Description("Agent session ID for grouping watches")),
	)
}

func watchHandler(watchStore store.WatchStore, logStore store.LogStore, metrics *watcher.WatchMetrics) func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

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

		w, err := watchStore.Create(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("creating watch: %w", err)
		}

		// Capture baseline
		baseline, err := watcher.CaptureBaseline(ctx, logStore, metrics, w)
		if err == nil {
			_ = watchStore.UpdateBaseline(ctx, w.ID, baseline)
			w, _ = watchStore.GetByID(ctx, w.ID)
		}

		data, _ := json.MarshalIndent(w, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}

// --- watch_status (read tool) ---

func watchStatusTool() mcp.Tool {
	return mcp.NewTool("watch_status",
		mcp.WithDescription("List active watches with their current values and pending alert counts. Use to see what's being monitored and if anything needs attention."),
		mcp.WithString("status", mcp.Description("Filter by status: active, triggered, resolved, expired (default: shows active and triggered)")),
		mcp.WithString("service", mcp.Description("Filter by service name")),
		mcp.WithString("session_id", mcp.Description("Filter by agent session ID")),
	)
}

func watchStatusHandler(watchStore store.WatchStore) func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

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

		// If no status filter, show both active and triggered
		var watches []store.Watch
		if params.Status == "" {
			active, err := watchStore.List(ctx, store.ListWatchParams{
				Status:    store.WatchStatusActive,
				Service:   params.Service,
				SessionID: params.SessionID,
			})
			if err != nil {
				return nil, fmt.Errorf("listing active watches: %w", err)
			}
			triggered, err := watchStore.List(ctx, store.ListWatchParams{
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
			watches, err = watchStore.List(ctx, params)
			if err != nil {
				return nil, fmt.Errorf("listing watches: %w", err)
			}
		}

		pendingCount, _ := watchStore.CountPendingAlerts(ctx)

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

		// Suggest next actions based on alert state.
		var suggestions []ToolSuggestion
		if pendingCount > 0 {
			suggestions = append(suggestions, suggest("diagnose", "Investigate triggered alerts with full context", nil))
		}
		for _, w := range watches {
			if w.Status == store.WatchStatusTriggered {
				suggestions = append(suggestions, suggest("log_search", "Search error logs for triggered service", map[string]any{
					"level":   "error",
					"service": w.Service,
				}))
				break
			}
		}
		withSuggestions(result, suggestions...)

		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}

// --- investigate (read tool) ---

func investigateTool() mcp.Tool {
	return mcp.NewTool("investigate",
		mcp.WithDescription(`Investigate a watch alert or collect data about a service.

Mode A (alert_id): Returns stored evidence for a specific alert — recent errors, affected endpoints, new error classes, relevant logs, source files, and baseline diff.

Mode B (service): One-shot data collection without a watch — gathers recent errors, request summaries, and log patterns for the named service.`),
		mcp.WithString("alert_id", mcp.Description("Alert ID to investigate (Mode A)")),
		mcp.WithString("service", mcp.Description("Service name for one-shot investigation (Mode B)")),
		mcp.WithString("window", mcp.Description("Time window for Mode B investigation (default: 1h). Examples: 15m, 1h, 6h")),
	)
}

func investigateHandler(watchStore store.WatchStore, logStore store.LogStore, metrics *watcher.WatchMetrics) func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		alertID, _ := args["alert_id"].(string)
		service, _ := args["service"].(string)

		// Mode A: investigate a specific alert
		if alertID != "" {
			alert, err := watchStore.GetAlert(ctx, alertID)
			if err != nil {
				return nil, fmt.Errorf("getting alert: %w", err)
			}
			data, _ := json.MarshalIndent(alert, "", "  ")
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

		// Collect data
		type investigation struct {
			Service       string                       `json:"service"`
			Window        string                       `json:"window"`
			ErrorRate     float64                      `json:"error_rate"`
			LogCount      float64                      `json:"log_count"`
			ErrorCount    float64                      `json:"error_count"`
			RecentErrors  []store.WatchEvidenceError   `json:"recent_errors,omitempty"`
			RecentLogs    []store.WatchEvidenceLog     `json:"recent_logs,omitempty"`
		}

		inv := investigation{
			Service: service,
			Window:  windowStr,
		}

		// Metrics
		inv.ErrorRate, _ = metrics.Measure(ctx, store.WatchMetricErrorRate, service, "", window)
		inv.LogCount, _ = metrics.Measure(ctx, store.WatchMetricLogCount, service, "", window)
		inv.ErrorCount, _ = metrics.Measure(ctx, store.WatchMetricErrorCount, service, "", window)

		// Recent errors
		errorLogs, err := logStore.Search(ctx, store.LogSearchParams{
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
		logs, err := logStore.Search(ctx, store.LogSearchParams{
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

		data, _ := json.MarshalIndent(inv, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}

// --- dismiss_watch (write tool) ---

func dismissWatchTool() mcp.Tool {
	return mcp.NewTool("dismiss_watch",
		mcp.WithDescription(`Stop a watch or dismiss/acknowledge an alert.

To stop a watch: provide watch_id + action=stop
To dismiss an alert: provide alert_id + action=dismiss + reason
To acknowledge an alert: provide alert_id + action=acknowledge`),
		mcp.WithString("watch_id", mcp.Description("Watch ID to stop")),
		mcp.WithString("alert_id", mcp.Description("Alert ID to dismiss or acknowledge")),
		mcp.WithString("action", mcp.Required(), mcp.Description("Action: stop, dismiss, acknowledge")),
		mcp.WithString("reason", mcp.Description("Reason for dismissal (required for dismiss action)")),
	)
}

func dismissWatchHandler(watchStore store.WatchStore) func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		watchID, _ := args["watch_id"].(string)
		alertID, _ := args["alert_id"].(string)
		action, _ := args["action"].(string)
		reason, _ := args["reason"].(string)

		switch action {
		case "stop":
			if watchID == "" {
				return nil, fmt.Errorf("watch_id is required for stop action")
			}
			if err := watchStore.Delete(ctx, watchID); err != nil {
				return nil, fmt.Errorf("stopping watch: %w", err)
			}
			return mcp.NewToolResultText(fmt.Sprintf(`{"status":"stopped","watch_id":"%s"}`, watchID)), nil

		case "dismiss":
			if alertID == "" {
				return nil, fmt.Errorf("alert_id is required for dismiss action")
			}
			if reason == "" {
				reason = "dismissed by agent"
			}
			if err := watchStore.DismissAlert(ctx, alertID, reason); err != nil {
				return nil, fmt.Errorf("dismissing alert: %w", err)
			}
			return mcp.NewToolResultText(fmt.Sprintf(`{"status":"dismissed","alert_id":"%s","reason":"%s"}`, alertID, reason)), nil

		case "acknowledge":
			if alertID == "" {
				return nil, fmt.Errorf("alert_id is required for acknowledge action")
			}
			if err := watchStore.AcknowledgeAlert(ctx, alertID); err != nil {
				return nil, fmt.Errorf("acknowledging alert: %w", err)
			}
			return mcp.NewToolResultText(fmt.Sprintf(`{"status":"acknowledged","alert_id":"%s"}`, alertID)), nil

		default:
			return nil, fmt.Errorf("unknown action: %s (expected: stop, dismiss, acknowledge)", action)
		}
	}
}
