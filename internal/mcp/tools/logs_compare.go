package tools

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/adham90/opentrace/internal/domain/logs"
	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// action: compare — compare metrics between two time periods (from comparePeriodsHandler)
// ---------------------------------------------------------------------------

func LogsCompare(ctx context.Context, args map[string]any, deps LogsDeps) (*CallToolResult, error) {
	InitLogsDeps(&deps)
	metric := ArgString(args, "metric")
	if metric == "" {
		return NewToolResultError("metric is required (errors, log_volume)"), nil
	}

	currentPeriod := ArgStringDefault(args, "current_period", "last_1h")
	baselinePeriod := ArgStringDefault(args, "baseline_period", "previous")
	serviceFilter := ArgString(args, "service")

	now := time.Now().UTC()
	currentStart, currentEnd, err := logsResolvePeriod(currentPeriod, now)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid current_period: %v", err)), nil
	}

	baseStart, baseEnd, err := logsResolveBaseline(baselinePeriod, currentStart, currentEnd)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid baseline_period: %v", err)), nil
	}

	switch metric {
	case "errors":
		return LogsCompareErrors(ctx, deps.Logs, currentStart, currentEnd, baseStart, baseEnd, serviceFilter)
	case "log_volume":
		return LogsCompareLogVolume(ctx, deps.Logs, currentStart, currentEnd, baseStart, baseEnd, serviceFilter)
	default:
		return NewToolResultError(fmt.Sprintf("invalid metric: %q (use errors or log_volume)", metric)), nil
	}
}

func LogsCompareErrors(ctx context.Context, svc *logs.Service, curStart, curEnd, baseStart, baseEnd time.Time, service string) (*CallToolResult, error) {
	curParams := store.LogCountParams{Since: curStart, Until: curEnd, Service: service}
	baseParams := store.LogCountParams{Since: baseStart, Until: baseEnd, Service: service}

	curSvc, err := svc.CountByService(ctx, curParams)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to count current errors: %v", err)), nil
	}
	baseSvc, err := svc.CountByService(ctx, baseParams)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to count baseline errors: %v", err)), nil
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

	changePct, direction := logsCalcChange(baseTotal, curTotal)

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
		pct, _ := logsCalcChange(from, to)
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
			"period":       logsPeriodInfo(curStart, curEnd),
			"total_errors": curTotal,
			"by_service":   curByService,
		},
		"baseline": map[string]any{
			"period":       logsPeriodInfo(baseStart, baseEnd),
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

	return JSONResult(resp)
}

func LogsCompareLogVolume(ctx context.Context, svc *logs.Service, curStart, curEnd, baseStart, baseEnd time.Time, service string) (*CallToolResult, error) {
	curParams := store.LogCountParams{Since: curStart, Until: curEnd, Service: service}
	baseParams := store.LogCountParams{Since: baseStart, Until: baseEnd, Service: service}

	curLC, err := svc.CountByLevel(ctx, curParams)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to count current logs: %v", err)), nil
	}
	baseLC, err := svc.CountByLevel(ctx, baseParams)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to count baseline logs: %v", err)), nil
	}

	curLevels := curLC.ByLevel
	baseLevels := baseLC.ByLevel
	curTotal := curLC.Total
	baseTotal := baseLC.Total

	changePct, direction := logsCalcChange(baseTotal, curTotal)

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
		pct, _ := logsCalcChange(from, to)
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
			"period":   logsPeriodInfo(curStart, curEnd),
			"total":    curTotal,
			"by_level": curLevels,
		},
		"baseline": map[string]any{
			"period":   logsPeriodInfo(baseStart, baseEnd),
			"total":    baseTotal,
			"by_level": baseLevels,
		},
		"changes": map[string]any{
			"total_change_pct": changePct,
			"direction":        direction,
			"by_level":         levelChanges,
		},
	}

	return JSONResult(resp)
}
