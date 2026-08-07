package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// --- investigate action ---

func HandleOverviewInvestigate(ctx context.Context, d OverviewDeps, args map[string]any) (*CallToolResult, error) {
	service := ArgString(args, "service")
	if service == "" {
		return NewToolResultError("service is required for the investigate action"), nil
	}

	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	since := GetSinceParam(args, 1*time.Hour)
	now := time.Now().UTC()

	var wg sync.WaitGroup
	var mu sync.Mutex
	resp := map[string]any{
		"service":     service,
		"environment": envLabel(env),
		"since":       since.Format(time.RFC3339),
		"until":       now.Format(time.RFC3339),
	}

	var unresolvedCount int
	var topErrorsList []map[string]any
	var recentErrorLogs []map[string]any
	var alertsList []map[string]any
	var logVolume map[string]any

	// 1. Top errors by occurrence count.
	if d.ErrorGroupStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
				Service:     service,
				Environment: env,
				Limit:       5,
				SortBy:      "occurrence_count",
			})
			if err != nil {
				return
			}
			uc, _ := d.ErrorGroupStore.Count(ctx, store.ErrorGroupUnresolved, env)

			mu.Lock()
			unresolvedCount = uc
			for _, g := range groups {
				topErrorsList = append(topErrorsList, map[string]any{
					"fingerprint":      g.Fingerprint,
					"exception_class":  g.ExceptionClass,
					"message":          Truncate(g.Message, 100),
					"occurrence_count": g.OccurrenceCount,
					"last_seen_at":     g.LastSeenAt.Format(time.RFC3339),
					"status":           string(g.Status),
				})
			}
			mu.Unlock()
		}()
	}

	// 2. Recent error logs.
	if d.LogStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sinceTime := since
			logs, err := d.LogStore.Search(ctx, store.LogSearchParams{
				Service: service,
				Level:   "error",
				Start:   &sinceTime,
				Limit:   10,
			})
			if err != nil {
				return
			}
			mu.Lock()
			for _, l := range logs {
				recentErrorLogs = append(recentErrorLogs, map[string]any{
					"id":        l.ID,
					"timestamp": l.Timestamp.Format(time.RFC3339),
					"level":     l.Level,
					"message":   Truncate(l.Message, 150),
				})
			}
			mu.Unlock()
		}()
	}

	// 3. Active watch alerts.
	if d.WatchStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			alerts, err := d.WatchStore.ListAlerts(ctx, "", "pending", 20)
			if err != nil {
				return
			}
			mu.Lock()
			for _, a := range alerts {
				// Alert doesn't have service directly; include all and let
				// the summary note them. Filter via watch if possible.
				alertsList = append(alertsList, map[string]any{
					"id":              a.ID,
					"summary":         a.Summary,
					"urgency":         string(a.Urgency),
					"trigger_metric":  a.TriggerMetric(),
					"trigger_value":   a.TriggerValue(),
					"threshold_value": a.ThresholdValue(),
					"created_at":      a.CreatedAt.Format(time.RFC3339),
				})
			}
			mu.Unlock()
		}()
	}

	// 4. Log volume by level.
	if d.LogStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			byLevel, err := d.LogStore.CountByLevel(ctx, store.LogCountParams{
				Since:   since,
				Until:   now,
				Service: service,
			})
			if err != nil {
				return
			}
			total := 0
			for _, c := range byLevel {
				total += c
			}
			mu.Lock()
			logVolume = map[string]any{
				"total":    total,
				"by_level": byLevel,
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	resp["errors"] = map[string]any{
		"unresolved_count": unresolvedCount,
		"top_errors":       topErrorsList,
	}
	resp["recent_logs"] = recentErrorLogs
	resp["alerts"] = alertsList
	if logVolume != nil {
		resp["log_volume"] = logVolume
	}

	// Build summary.
	errorLogCount := len(recentErrorLogs)
	alertCount := len(alertsList)
	resp["summary"] = fmt.Sprintf("%d unresolved errors, %d error logs in last hour, %d active alerts",
		unresolvedCount, errorLogCount, alertCount)

	// Suggestions.
	var suggestions []ToolSuggestion
	if len(topErrorsList) > 0 {
		if fp, ok := topErrorsList[0]["fingerprint"].(string); ok && fp != "" {
			suggestions = append(suggestions, Suggest("errors", "Investigate the top error", map[string]any{
				"action":      "detail",
				"fingerprint": fp,
			}))
		}
	}
	if errorLogCount > 0 {
		suggestions = append(suggestions, Suggest("logs", "Search error logs for this service", map[string]any{
			"action":  "search",
			"service": service,
			"level":   "error",
		}))
	}
	return JSONResult(resp, suggestions...)
}
