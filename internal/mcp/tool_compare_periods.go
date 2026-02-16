package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// comparePeriodsHandler returns a handler that compares metrics between two
// time periods: error rates and log volumes.
func comparePeriodsHandler(ls store.LogStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		metric, _ := args["metric"].(string)
		if metric == "" {
			return mcp.NewToolResultError("metric is required (errors, log_volume)"), nil
		}

		currentPeriod := "last_1h"
		if v, ok := args["current_period"].(string); ok && v != "" {
			currentPeriod = v
		}

		baselinePeriod := "previous"
		if v, ok := args["baseline_period"].(string); ok && v != "" {
			baselinePeriod = v
		}

		serviceFilter, _ := args["service"].(string)

		now := time.Now().UTC()
		currentStart, currentEnd, err := resolvePeriod(currentPeriod, now)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid current_period: %v", err)), nil
		}

		baseStart, baseEnd, err := resolveBaseline(baselinePeriod, currentStart, currentEnd)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid baseline_period: %v", err)), nil
		}

		switch metric {
		case "errors":
			return compareErrors(ctx, ls, currentStart, currentEnd, baseStart, baseEnd, serviceFilter)
		case "log_volume":
			return compareLogVolume(ctx, ls, currentStart, currentEnd, baseStart, baseEnd, serviceFilter)
		default:
			return mcp.NewToolResultError(fmt.Sprintf("invalid metric: %q (use errors or log_volume)", metric)), nil
		}
	}
}

func compareErrors(ctx context.Context, ls store.LogStore, curStart, curEnd, baseStart, baseEnd time.Time, service string) (*mcp.CallToolResult, error) {
	curParams := store.LogCountParams{Since: curStart, Until: curEnd, Service: service}
	baseParams := store.LogCountParams{Since: baseStart, Until: baseEnd, Service: service}

	curSvc, err := ls.CountByService(ctx, curParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to count current errors: %v", err)), nil
	}
	baseSvc, err := ls.CountByService(ctx, baseParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to count baseline errors: %v", err)), nil
	}

	curTotal := 0
	curByService := map[string]int{}
	for _, s := range curSvc {
		curTotal += s.ErrorCount
		if s.ErrorCount > 0 {
			curByService[s.Service] = s.ErrorCount
		}
	}

	baseTotal := 0
	baseByService := map[string]int{}
	for _, s := range baseSvc {
		baseTotal += s.ErrorCount
		if s.ErrorCount > 0 {
			baseByService[s.Service] = s.ErrorCount
		}
	}

	changePct, direction := calcChange(baseTotal, curTotal)

	var movers []map[string]any
	allServices := map[string]bool{}
	for svc := range curByService {
		allServices[svc] = true
	}
	for svc := range baseByService {
		allServices[svc] = true
	}
	for svc := range allServices {
		from := baseByService[svc]
		to := curByService[svc]
		pct, _ := calcChange(from, to)
		movers = append(movers, map[string]any{
			"service":    svc,
			"from":       from,
			"to":         to,
			"change_pct": pct,
		})
	}
	sort.Slice(movers, func(i, j int) bool {
		return math.Abs(movers[i]["change_pct"].(float64)) > math.Abs(movers[j]["change_pct"].(float64))
	})
	if len(movers) > 5 {
		movers = movers[:5]
	}

	var warnings []string
	if direction == "increase" && math.Abs(changePct) > 50 {
		warnings = append(warnings, fmt.Sprintf("Error rate increased %.0f%% compared to the baseline period", changePct))
	}
	if len(movers) > 0 {
		topMover := movers[0]
		topPct := math.Abs(topMover["change_pct"].(float64))
		if topPct > 100 {
			warnings = append(warnings, fmt.Sprintf("Service '%s' errors changed %.0f%% — largest contributor", topMover["service"], topMover["change_pct"]))
		}
	}

	resp := map[string]any{
		"metric": "errors",
		"current": map[string]any{
			"period":       periodInfo(curStart, curEnd),
			"total_errors": curTotal,
			"by_service":   curByService,
		},
		"baseline": map[string]any{
			"period":       periodInfo(baseStart, baseEnd),
			"total_errors": baseTotal,
			"by_service":   baseByService,
		},
		"changes": map[string]any{
			"total_change_pct": changePct,
			"direction":        direction,
			"biggest_movers":   movers,
		},
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func compareLogVolume(ctx context.Context, ls store.LogStore, curStart, curEnd, baseStart, baseEnd time.Time, service string) (*mcp.CallToolResult, error) {
	curParams := store.LogCountParams{Since: curStart, Until: curEnd, Service: service}
	baseParams := store.LogCountParams{Since: baseStart, Until: baseEnd, Service: service}

	curLevels, err := ls.CountByLevel(ctx, curParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to count current logs: %v", err)), nil
	}
	baseLevels, err := ls.CountByLevel(ctx, baseParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to count baseline logs: %v", err)), nil
	}

	curTotal := 0
	for _, c := range curLevels {
		curTotal += c
	}
	baseTotal := 0
	for _, c := range baseLevels {
		baseTotal += c
	}

	changePct, direction := calcChange(baseTotal, curTotal)

	var levelChanges []map[string]any
	allLevels := map[string]bool{}
	for l := range curLevels {
		allLevels[l] = true
	}
	for l := range baseLevels {
		allLevels[l] = true
	}
	for l := range allLevels {
		from := baseLevels[l]
		to := curLevels[l]
		pct, _ := calcChange(from, to)
		levelChanges = append(levelChanges, map[string]any{
			"level":      l,
			"from":       from,
			"to":         to,
			"change_pct": pct,
		})
	}

	resp := map[string]any{
		"metric": "log_volume",
		"current": map[string]any{
			"period":   periodInfo(curStart, curEnd),
			"total":    curTotal,
			"by_level": curLevels,
		},
		"baseline": map[string]any{
			"period":   periodInfo(baseStart, baseEnd),
			"total":    baseTotal,
			"by_level": baseLevels,
		},
		"changes": map[string]any{
			"total_change_pct": changePct,
			"direction":        direction,
			"by_level":         levelChanges,
		},
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func resolvePeriod(period string, now time.Time) (time.Time, time.Time, error) {
	switch period {
	case "last_1h":
		return now.Add(-time.Hour), now, nil
	case "last_6h":
		return now.Add(-6 * time.Hour), now, nil
	case "last_24h":
		return now.Add(-24 * time.Hour), now, nil
	case "today":
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return start, now, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unknown period %q (use last_1h, last_6h, last_24h, today)", period)
	}
}

func resolveBaseline(baseline string, curStart, curEnd time.Time) (time.Time, time.Time, error) {
	duration := curEnd.Sub(curStart)
	switch baseline {
	case "previous":
		return curStart.Add(-duration), curStart, nil
	case "yesterday_same_time":
		return curStart.Add(-24 * time.Hour), curEnd.Add(-24 * time.Hour), nil
	case "last_week_same_time":
		return curStart.Add(-168 * time.Hour), curEnd.Add(-168 * time.Hour), nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unknown baseline %q (use previous, yesterday_same_time, last_week_same_time)", baseline)
	}
}

func calcChange(baseline, current int) (float64, string) {
	if baseline == 0 {
		if current == 0 {
			return 0, "unchanged"
		}
		return 100, "new"
	}
	pct := float64(current-baseline) / float64(baseline) * 100
	pct = round2(pct)
	if pct > 0 {
		return pct, "increase"
	} else if pct < 0 {
		return pct, "decrease"
	}
	return 0, "unchanged"
}

func periodInfo(start, end time.Time) map[string]string {
	return map[string]string{
		"start": start.Format(time.RFC3339),
		"end":   end.Format(time.RFC3339),
	}
}
