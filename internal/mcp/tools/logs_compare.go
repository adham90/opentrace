package tools

import (
	"context"
	"fmt"
	"log/slog"
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

	// Resolve env scope so comparisons never mix in unauthorized environments.
	environment, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

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
		return LogsCompareErrors(ctx, deps.Logs, currentStart, currentEnd, baseStart, baseEnd, serviceFilter, environment)
	case "log_volume":
		return LogsCompareLogVolume(ctx, deps.Logs, currentStart, currentEnd, baseStart, baseEnd, serviceFilter, environment)
	default:
		return NewToolResultError(fmt.Sprintf("invalid metric: %q (use errors or log_volume)", metric)), nil
	}
}

func LogsCompareErrors(ctx context.Context, svc *logs.Service, curStart, curEnd, baseStart, baseEnd time.Time, service, environment string) (*CallToolResult, error) {
	curParams := store.LogCountParams{Since: curStart, Until: curEnd, Service: service, Environment: environment}
	baseParams := store.LogCountParams{Since: baseStart, Until: baseEnd, Service: service, Environment: environment}

	// Error counts come from CountByLevel, per service. ServiceLogCount.ErrorCount
	// is not populated by the production adapter, so summing it reported
	// "0 errors, unchanged" during an incident with thousands of errors —
	// CountByService is used only to enumerate which services were active.
	curByService, curTotal, err := logsErrorsByService(ctx, svc, curParams)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to count current errors: %v", err)), nil
	}
	baseByService, baseTotal, err := logsErrorsByService(ctx, svc, baseParams)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to count baseline errors: %v", err)), nil
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
	switch {
	case direction == "new":
		// Errors appearing where the baseline had none is the loudest signal
		// there is; it used to be silent because the label wasn't "increase".
		warnings = append(warnings, fmt.Sprintf(
			"Errors appeared where the baseline period had none: 0 → %d", curTotal))
	case direction == "increase" && math.Abs(changePct) > 50:
		warnings = append(warnings, fmt.Sprintf("Error rate increased %.0f%% compared to the baseline period", changePct))
	}
	if len(movers) > 0 {
		topMover := movers[0]
		topPct := math.Abs(topMover["change_pct"].(float64))
		if topPct > 100 {
			if topMover["from"].(int) == 0 {
				warnings = append(warnings, fmt.Sprintf(
					"Service '%s' went from 0 to %d errors — largest contributor", topMover["service"], topMover["to"]))
			} else {
				warnings = append(warnings, fmt.Sprintf("Service '%s' errors changed %.0f%% — largest contributor", topMover["service"], topMover["change_pct"]))
			}
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

// logsErrorsByService returns error+fatal counts per service for a window,
// plus their total. Services are enumerated with CountByService (whose only
// reliable field is Service) and counted with CountByLevel, which honours
// Service/Level/Environment.
func logsErrorsByService(ctx context.Context, svc *logs.Service, params store.LogCountParams) (map[string]int, int, error) {
	services, err := svc.CountByService(ctx, params)
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Total > services[j].Total })
	if len(services) > maxServiceBreakdown {
		services = services[:maxServiceBreakdown]
	}

	byService := make(map[string]int, len(services))
	total := 0
	for _, s := range services {
		p := params
		p.Service = s.Service
		lc, lcErr := svc.CountByLevel(ctx, p)
		if lcErr != nil {
			slog.Warn("compare per-service error count failed",
				"event", "logs_compare_service_count_failed",
				"service", s.Service,
				"error", lcErr,
			)
			continue
		}
		if lc.ErrorCount > 0 {
			byService[s.Service] = lc.ErrorCount
		}
		total += lc.ErrorCount
	}
	return byService, total, nil
}

func LogsCompareLogVolume(ctx context.Context, svc *logs.Service, curStart, curEnd, baseStart, baseEnd time.Time, service, environment string) (*CallToolResult, error) {
	curParams := store.LogCountParams{Since: curStart, Until: curEnd, Service: service, Environment: environment}
	baseParams := store.LogCountParams{Since: baseStart, Until: baseEnd, Service: service, Environment: environment}

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
