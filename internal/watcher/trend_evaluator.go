package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// TrendEvaluator detects metric changes relative to a rolling baseline.
type TrendEvaluator struct {
	registry *connector.Registry
	logStore store.LogStore
	runStore store.WatcherRunStore
}

// NewTrendEvaluator creates a new trend evaluator.
func NewTrendEvaluator(
	registry *connector.Registry,
	logStore store.LogStore,
	runStore store.WatcherRunStore,
) *TrendEvaluator {
	return &TrendEvaluator{
		registry: registry,
		logStore: logStore,
		runStore: runStore,
	}
}

// trendSnapshot stores the current value for future baseline computation.
type trendSnapshot struct {
	Value float64 `json:"value"`
}

// Evaluate compares current value against rolling baseline.
func (te *TrendEvaluator) Evaluate(ctx context.Context, w store.Watcher) (*EvalResult, error) {
	if len(w.TypeConfig) == 0 {
		return nil, fmt.Errorf("trend watcher %s has no type_config", w.ID)
	}

	var cfg TrendConfig
	if err := json.Unmarshal(w.TypeConfig, &cfg); err != nil {
		return nil, fmt.Errorf("parsing trend config: %w", err)
	}

	// Get current value
	currentValue, err := te.getCurrentValue(ctx, &cfg)
	if err != nil {
		return nil, fmt.Errorf("getting current value: %w", err)
	}

	snap := trendSnapshot{Value: currentValue}

	// Get baseline from previous runs
	baselineValues, err := te.getBaseline(ctx, w, cfg.BaselineRuns)
	if err != nil {
		return nil, fmt.Errorf("getting baseline: %w", err)
	}

	if len(baselineValues) < cfg.BaselineRuns {
		// Insufficient baseline — store value, no alert
		return &EvalResult{
			HasAlert: false,
			Summary:  fmt.Sprintf("Building baseline: %.2f (need %d runs, have %d)", currentValue, cfg.BaselineRuns, len(baselineValues)),
			Details:  snap,
		}, nil
	}

	// Calculate average baseline
	var sum float64
	for _, v := range baselineValues {
		sum += v
	}
	avg := sum / float64(len(baselineValues))

	// Calculate percent change
	var changePct float64
	if avg != 0 {
		changePct = math.Abs(currentValue-avg) / math.Abs(avg) * 100
	} else if currentValue != 0 {
		changePct = 100 // infinite change from 0
	}

	// Check direction
	triggered := false
	if changePct > cfg.ChangePercent {
		switch cfg.Direction {
		case "increase":
			triggered = currentValue > avg
		case "decrease":
			triggered = currentValue < avg
		case "both", "":
			triggered = true
		}
	}

	var summary string
	if triggered {
		dir := "changed"
		if currentValue > avg {
			dir = "increased"
		} else if currentValue < avg {
			dir = "decreased"
		}
		summary = fmt.Sprintf("TREND ALERT: Value %s by %.1f%% (current: %.2f, baseline avg: %.2f, threshold: %.0f%%)",
			dir, changePct, currentValue, avg, cfg.ChangePercent)
	} else {
		summary = fmt.Sprintf("OK: Value %.2f within %.0f%% of baseline %.2f (change: %.1f%%)",
			currentValue, cfg.ChangePercent, avg, changePct)
	}

	return &EvalResult{
		HasAlert: triggered,
		Summary:  summary,
		Value:    &currentValue,
		Details:  snap,
	}, nil
}

func (te *TrendEvaluator) getCurrentValue(ctx context.Context, cfg *TrendConfig) (float64, error) {
	switch cfg.Source {
	case "query":
		return te.queryValue(ctx, cfg)
	case "logs":
		return te.logCount(ctx, cfg)
	default:
		return 0, fmt.Errorf("unknown trend source: %q", cfg.Source)
	}
}

func (te *TrendEvaluator) queryValue(ctx context.Context, cfg *TrendConfig) (float64, error) {
	ds := te.registry.Get(connector.ConnectorDatabase)
	if ds == nil {
		return 0, fmt.Errorf("no database connector available")
	}

	qe, ok := ds.(connector.QueryExecutor)
	if !ok {
		return 0, fmt.Errorf("database connector does not support direct queries")
	}

	result, err := qe.ExecuteReadQuery(ctx, cfg.Query)
	if err != nil {
		return 0, fmt.Errorf("executing query: %w", err)
	}

	switch cfg.Metric {
	case "row_count":
		return float64(result.RowCount), nil
	case "value":
		if result.RowCount == 0 || len(result.Rows[0]) == 0 {
			return 0, nil
		}
		return toFloat64(result.Rows[0][0])
	default:
		return 0, fmt.Errorf("unknown metric: %q", cfg.Metric)
	}
}

func (te *TrendEvaluator) logCount(ctx context.Context, cfg *TrendConfig) (float64, error) {
	window := ParseTimeRange(cfg.Window)
	start := time.Now().Add(-window)

	params := store.LogSearchParams{
		Start: &start,
		Limit: 10000,
	}
	if cfg.Filter != nil {
		params.Service = cfg.Filter.Service
		params.Level = cfg.Filter.Level
		params.Query = cfg.Filter.Query
	}

	logs, err := te.logStore.Search(ctx, params)
	if err != nil {
		return 0, fmt.Errorf("searching logs: %w", err)
	}

	return float64(len(logs)), nil
}

func (te *TrendEvaluator) getBaseline(ctx context.Context, w store.Watcher, n int) ([]float64, error) {
	runs, err := te.runStore.List(ctx, w.ID, n+5) // fetch extra in case some are incomplete
	if err != nil {
		return nil, err
	}

	var values []float64
	for _, r := range runs {
		if r.Status != "completed" || len(r.Details) == 0 {
			continue
		}
		var snap trendSnapshot
		if err := json.Unmarshal(r.Details, &snap); err != nil {
			continue
		}
		values = append(values, snap.Value)
		if len(values) >= n {
			break
		}
	}

	return values, nil
}
