package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// --- changes action ---

func HandleChanges(ctx context.Context, d OverviewDeps, args map[string]any) (*CallToolResult, error) {
	// Changes is a listing, so it takes the caller's env scope like status,
	// triage and diagnose do. Querying unfiltered put production fingerprints,
	// log lines and volume counts in front of a staging-scoped token.
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	since := GetSinceParam(args, 2*time.Hour)
	now := time.Now().UTC()
	windowDuration := now.Sub(since)
	service := ArgString(args, "service")

	resp := map[string]any{
		"since":       since.Format(time.RFC3339),
		"until":       now.Format(time.RFC3339),
		"environment": envLabel(env),
	}
	if service != "" {
		resp["service"] = service
	}

	// 1. New error fingerprints (first seen after since).
	var newErrors []map[string]any
	if d.ErrorGroupStore != nil {
		groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
			Service:     service,
			Environment: env,
			Limit:       20,
			SortBy:      "first_seen_at",
		})
		if err == nil {
			for _, g := range groups {
				if g.FirstSeenAt.After(since) {
					newErrors = append(newErrors, map[string]any{
						"fingerprint":      g.Fingerprint,
						"exception_class":  g.ExceptionClass,
						"message":          Truncate(g.Message, 100),
						"service":          g.Service,
						"occurrence_count": g.OccurrenceCount,
						"first_seen_at":    g.FirstSeenAt.Format(time.RFC3339),
					})
				}
			}
		}
	}
	resp["new_errors"] = newErrors

	// 2. Recent error/fatal log entries.
	var recentErrorLogs []map[string]any
	if d.LogStore != nil {
		sinceTime := since
		logs, err := d.LogStore.Search(ctx, store.LogSearchParams{
			Service:     service,
			Environment: env,
			Level:       "error",
			Start:       &sinceTime,
			Limit:       10,
		})
		if err == nil {
			for _, l := range logs {
				recentErrorLogs = append(recentErrorLogs, map[string]any{
					"id":        l.ID,
					"timestamp": l.Timestamp.Format(time.RFC3339),
					"level":     l.Level,
					"message":   Truncate(l.Message, 150),
					"service":   l.Service,
				})
			}
		}
	}
	resp["recent_error_logs"] = recentErrorLogs

	// 3. Volume comparison: current window vs previous window of same duration.
	if d.LogStore != nil {
		prevStart := since.Add(-windowDuration)

		currentCounts, currErr := d.LogStore.CountByLevel(ctx, store.LogCountParams{
			Since:       since,
			Until:       now,
			Service:     service,
			Environment: env,
		})
		prevCounts, prevErr := d.LogStore.CountByLevel(ctx, store.LogCountParams{
			Since:       prevStart,
			Until:       since,
			Service:     service,
			Environment: env,
		})

		currentErrors := 0
		previousErrors := 0
		if currErr == nil {
			currentErrors = currentCounts["error"] + currentCounts["fatal"]
		}
		if prevErr == nil {
			previousErrors = prevCounts["error"] + prevCounts["fatal"]
		}

		var changePct float64
		if previousErrors > 0 {
			changePct = float64(currentErrors-previousErrors) / float64(previousErrors) * 100
		} else if currentErrors > 0 {
			changePct = 100.0 // went from 0 to some errors
		}

		resp["volume_comparison"] = map[string]any{
			"current_errors":  currentErrors,
			"previous_errors": previousErrors,
			"change_pct":      fmt.Sprintf("%.1f", changePct),
		}
	}

	// Suggestions.
	var suggestions []ToolSuggestion
	if len(newErrors) > 0 {
		if fp, ok := newErrors[0]["fingerprint"].(string); ok && fp != "" {
			suggestions = append(suggestions, Suggest("errors", "Investigate the newest error", map[string]any{
				"action":      "detail",
				"fingerprint": fp,
			}))
		}
	}
	if len(recentErrorLogs) > 0 {
		suggestions = append(suggestions, Suggest("overview", "Full service investigation", map[string]any{
			"action":  "investigate",
			"service": service,
		}))
	}
	return JSONResult(resp, suggestions...)
}
