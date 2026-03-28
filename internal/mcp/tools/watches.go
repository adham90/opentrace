package tools

import (
	"context"
	"fmt"
	"time"


	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// WatchesDeps holds the stores needed by the watches tool.
type WatchesDeps struct {
	WatchStore   store.WatchStore
	LogStore     store.LogStore
	WatchMetrics *watcher.WatchMetrics
}

// WatchesHandler returns a handler for the consolidated watches tool.
func WatchesHandler(d WatchesDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action := ArgString(args, "action")

		switch action {
		case "status":
			return HandleWatchStatus(ctx, d, args)
		case "create":
			return HandleWatchCreate(ctx, d, args)
		case "delete":
			return HandleWatchDelete(ctx, d, args)
		case "alerts":
			return HandleWatchAlerts(ctx, d, args)
		case "dismiss":
			return HandleWatchDismiss(ctx, d, args)
		case "acknowledge":
			return HandleWatchAcknowledge(ctx, d, args)
		case "investigate":
			return HandleWatchInvestigate(ctx, d, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use status, create, delete, alerts, dismiss, acknowledge, investigate)", action)), nil
		}
	}
}

func HandleWatchStatus(ctx context.Context, d WatchesDeps, args map[string]any) (*CallToolResult, error) {
	params := store.ListWatchParams{}
	if v := ArgString(args, "status"); v != "" {
		params.Status = store.WatchStatus(v)
	}
	params.Service = ArgString(args, "service")
	params.SessionID = ArgString(args, "session_id")

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
			suggestions = append(suggestions, Suggest("logs", "Search error logs for triggered service", map[string]any{
				"action":  "search",
				"level":   "error",
				"service": w.Service,
			}))
			break
		}
	}
	return JSONResult(result, suggestions...)
}

func HandleWatchCreate(ctx context.Context, d WatchesDeps, args map[string]any) (*CallToolResult, error) {
	params := store.CreateWatchParams{
		Metric:    store.WatchMetric(ArgString(args, "metric")),
		Operator:  store.WatchOperator(ArgString(args, "operator")),
		Threshold: ArgFloat(args, "threshold", 0),
	}

	params.Service = ArgString(args, "service")
	params.Endpoint = ArgString(args, "endpoint")
	params.Environment = ArgString(args, "environment")
	params.CommitHash = ArgString(args, "commit_hash")
	params.Duration = ArgString(args, "duration")
	if v := ArgString(args, "urgency"); v != "" {
		params.Urgency = store.WatchUrgency(v)
	}
	params.CheckInterval = ArgString(args, "check_interval")
	params.MinConsecutive = ArgInt(args, "min_consecutive", 0, 100)
	params.SessionID = ArgString(args, "session_id")
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

	return JSONResult(w)
}

func HandleWatchDelete(ctx context.Context, d WatchesDeps, args map[string]any) (*CallToolResult, error) {
	watchID := ArgString(args, "watch_id")
	if watchID == "" {
		return NewToolResultError("watch_id is required for delete action"), nil
	}
	if err := d.WatchStore.Delete(ctx, watchID); err != nil {
		return nil, fmt.Errorf("stopping watch: %w", err)
	}
	return NewToolResultText(fmt.Sprintf(`{"status":"stopped","watch_id":"%s"}`, watchID)), nil
}

func HandleWatchAlerts(ctx context.Context, d WatchesDeps, args map[string]any) (*CallToolResult, error) {
	service := ArgString(args, "service")
	alerts, err := d.WatchStore.ListAlerts(ctx, "", "pending", 20)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list alerts: %v", err)), nil
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

	return JSONResult(map[string]any{
		"count":  len(filtered),
		"alerts": filtered,
	})
}

func HandleWatchDismiss(ctx context.Context, d WatchesDeps, args map[string]any) (*CallToolResult, error) {
	alertID := ArgString(args, "alert_id")
	if alertID == "" {
		return NewToolResultError("alert_id is required for dismiss action"), nil
	}
	reason := ArgStringDefault(args, "reason", "dismissed by agent")
	if err := d.WatchStore.DismissAlert(ctx, alertID, reason); err != nil {
		return nil, fmt.Errorf("dismissing alert: %w", err)
	}
	return NewToolResultText(fmt.Sprintf(`{"status":"dismissed","alert_id":"%s","reason":"%s"}`, alertID, reason)), nil
}

func HandleWatchAcknowledge(ctx context.Context, d WatchesDeps, args map[string]any) (*CallToolResult, error) {
	alertID := ArgString(args, "alert_id")
	if alertID == "" {
		return NewToolResultError("alert_id is required for acknowledge action"), nil
	}
	if err := d.WatchStore.AcknowledgeAlert(ctx, alertID); err != nil {
		return nil, fmt.Errorf("acknowledging alert: %w", err)
	}
	return NewToolResultText(fmt.Sprintf(`{"status":"acknowledged","alert_id":"%s"}`, alertID)), nil
}

func HandleWatchInvestigate(ctx context.Context, d WatchesDeps, args map[string]any) (*CallToolResult, error) {
	alertID := ArgString(args, "alert_id")
	service := ArgString(args, "service")

	// Mode A: investigate a specific alert
	if alertID != "" {
		alert, err := d.WatchStore.GetAlert(ctx, alertID)
		if err != nil {
			return nil, fmt.Errorf("getting alert: %w", err)
		}

		resp := map[string]any{"alert": alert}
		var suggestions []ToolSuggestion

		if w, err := d.WatchStore.GetByID(ctx, alert.WatchID); err == nil && w.Service != "" {
			suggestions = append(suggestions, Suggest("logs", "Search error logs for this service", map[string]any{
				"action":  "search",
				"level":   "error",
				"service": w.Service,
			}))
			suggestions = append(suggestions, Suggest("overview", "Full investigation for this service", map[string]any{
				"action":  "diagnose",
				"service": w.Service,
			}))
		}
		return JSONResult(resp, suggestions...)
	}

	// Mode B: one-shot investigation
	if service == "" {
		return nil, fmt.Errorf("either alert_id or service is required")
	}

	windowStr := ArgStringDefault(args, "window", "1h")
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
		suggestions = append(suggestions, Suggest("logs", "View error logs for this service", map[string]any{
			"action":  "search",
			"level":   "error",
			"service": service,
		}))
	}
	if len(inv.RecentErrors) > 0 && inv.RecentErrors[0].ExceptionClass != "" && inv.RecentErrors[0].ExceptionClass != "Unknown" {
		suggestions = append(suggestions, Suggest("logs", "Search by exception class", map[string]any{
			"action":          "search",
			"exception_class": inv.RecentErrors[0].ExceptionClass,
			"service":         service,
		}))
	}
	return JSONResult(resp, suggestions...)
}
