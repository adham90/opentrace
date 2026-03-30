package tools

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// --- diagnose action ---

func HandleDiagnose(ctx context.Context, d OverviewDeps, args map[string]any) (*CallToolResult, error) {
	service := ArgString(args, "service")

	// Parse timeframe (default 1h)
	timeframe := ArgStringDefault(args, "timeframe", "1h")
	duration, err := ParseTimeRange(timeframe)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid timeframe %q: %v", timeframe, err)), nil
	}

	now := time.Now().UTC()
	since := now.Add(-duration)

	// Collect sections in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex
	resp := map[string]any{
		"service":   service,
		"timeframe": timeframe,
		"since":     since.Format(time.RFC3339),
		"until":     now.Format(time.RFC3339),
	}

	// 1. Error summary
	if d.ErrorGroupStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			section := collectErrors(ctx, d, service, since)
			mu.Lock()
			resp["error_summary"] = section
			mu.Unlock()
		}()
	}

	// 2. Log volume
	if d.LogStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			section := collectLogVolume(ctx, d, service, since, now)
			mu.Lock()
			resp["log_volume"] = section
			mu.Unlock()
		}()
	}

	// 3. Request performance
	if d.LogStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			section := collectPerformance(ctx, d, service, since, now)
			mu.Lock()
			resp["request_performance"] = section
			mu.Unlock()
		}()
	}

	// 4. Watch alerts
	if d.WatchStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			section := collectWatchAlerts(ctx, d, service)
			mu.Lock()
			resp["watch_alerts"] = section
			mu.Unlock()
		}()
	}

	// 5. Health check status
	if d.HealthCheckStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			section := collectHealthChecks(ctx, d, since)
			mu.Lock()
			resp["healthcheck_status"] = section
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Build suggested next tools
	resp["suggested_tools"] = buildDiagnoseSuggestions(resp, service)

	return JSONResult(resp)
}

func buildDiagnoseSuggestions(resp map[string]any, service string) []map[string]any {
	var suggestions []map[string]any

	if es, ok := resp["error_summary"].(map[string]any); ok {
		if topErrors, ok := es["top_errors"].([]map[string]any); ok && len(topErrors) > 0 {
			if fp, ok := topErrors[0]["fingerprint"].(string); ok && fp != "" {
				suggestions = append(suggestions, map[string]any{
					"tool": "errors",
					"args": map[string]any{"action": "detail", "fingerprint": fp},
				})
			}
		}
	}

	if lv, ok := resp["log_volume"].(map[string]any); ok {
		if ec, ok := lv["error_count"].(int); ok && ec > 0 {
			args := map[string]any{"action": "search", "level": "error", "limit": float64(10)}
			if service != "" {
				args["service"] = service
			}
			suggestions = append(suggestions, map[string]any{
				"tool": "logs",
				"args": args,
			})
		}
	}

	if wa, ok := resp["watch_alerts"].(map[string]any); ok {
		if tc, ok := wa["triggered_count"].(int); ok && tc > 0 {
			suggestions = append(suggestions, map[string]any{
				"tool": "watches",
				"args": map[string]any{"action": "status", "status": "triggered"},
			})
		}
	}

	if hc, ok := resp["healthcheck_status"].(map[string]any); ok {
		if dc, ok := hc["down"].(int); ok && dc > 0 {
			suggestions = append(suggestions, map[string]any{
				"tool": "healthchecks",
				"args": map[string]any{"action": "list"},
			})
		}
	}

	return suggestions
}
