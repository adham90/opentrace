package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// DeadmanEvaluator alerts when expected events are NOT seen.
type DeadmanEvaluator struct {
	registry *connector.Registry
	logStore store.LogStore
}

// NewDeadmanEvaluator creates a new deadman evaluator.
func NewDeadmanEvaluator(registry *connector.Registry, logStore store.LogStore) *DeadmanEvaluator {
	return &DeadmanEvaluator{
		registry: registry,
		logStore: logStore,
	}
}

// Evaluate checks that at least MinCount events occurred within the window.
func (de *DeadmanEvaluator) Evaluate(ctx context.Context, w store.Watcher) (*EvalResult, error) {
	if len(w.TypeConfig) == 0 {
		return nil, fmt.Errorf("deadman watcher %s has no type_config", w.ID)
	}

	var cfg DeadmanConfig
	if err := json.Unmarshal(w.TypeConfig, &cfg); err != nil {
		return nil, fmt.Errorf("parsing deadman config: %w", err)
	}

	switch cfg.Source {
	case "logs":
		return de.evaluateLogs(ctx, w, &cfg)
	case "query":
		return de.evaluateQuery(ctx, w, &cfg)
	default:
		return nil, fmt.Errorf("unknown deadman source: %q", cfg.Source)
	}
}

func (de *DeadmanEvaluator) evaluateLogs(ctx context.Context, w store.Watcher, cfg *DeadmanConfig) (*EvalResult, error) {
	window := ParseTimeRange(cfg.Window)
	start := time.Now().Add(-window)

	params := store.LogSearchParams{
		Start: &start,
		Limit: cfg.MinCount + 1,
	}
	if cfg.Filter != nil {
		params.Service = cfg.Filter.Service
		params.Level = cfg.Filter.Level
		params.Query = cfg.Filter.Query
		params.Environment = cfg.Filter.Environment
	}

	logs, err := de.logStore.Search(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("searching logs: %w", err)
	}

	count := len(logs)
	triggered := count < cfg.MinCount

	var summary string
	if triggered {
		summary = fmt.Sprintf("DEADMAN ALERT: Expected at least %d events in last %s but found %d", cfg.MinCount, window, count)
	} else {
		summary = fmt.Sprintf("OK: Found %d events in last %s (minimum: %d)", count, window, cfg.MinCount)
	}

	return &EvalResult{
		HasAlert: triggered,
		Summary:  summary,
		Details: map[string]any{
			"source":    "logs",
			"filter":    cfg.Filter,
			"window":    cfg.Window,
			"count":     count,
			"min_count": cfg.MinCount,
			"triggered": triggered,
		},
	}, nil
}

func (de *DeadmanEvaluator) evaluateQuery(ctx context.Context, w store.Watcher, cfg *DeadmanConfig) (*EvalResult, error) {
	ds := de.registry.Get(connector.ConnectorDatabase)
	if ds == nil {
		return nil, fmt.Errorf("no database connector available")
	}

	qe, ok := ds.(connector.QueryExecutor)
	if !ok {
		return nil, fmt.Errorf("database connector does not support direct queries")
	}

	result, err := qe.ExecuteReadQuery(ctx, cfg.Query)
	if err != nil {
		return nil, fmt.Errorf("executing query: %w", err)
	}

	var measured float64
	switch cfg.Metric {
	case "row_count":
		measured = float64(result.RowCount)
	case "value":
		if result.RowCount == 0 || len(result.Rows[0]) == 0 {
			measured = 0
		} else {
			measured, err = toFloat64(result.Rows[0][0])
			if err != nil {
				return nil, fmt.Errorf("converting query result to number: %w", err)
			}
		}
	default:
		return nil, fmt.Errorf("unknown metric: %q", cfg.Metric)
	}

	triggered := int(measured) < cfg.MinCount

	var summary string
	if triggered {
		summary = fmt.Sprintf("DEADMAN ALERT: Expected at least %d but query returned %.0f", cfg.MinCount, measured)
	} else {
		summary = fmt.Sprintf("OK: Query returned %.0f (minimum: %d)", measured, cfg.MinCount)
	}

	return &EvalResult{
		HasAlert: triggered,
		Summary:  summary,
		Value:    &measured,
		Details: map[string]any{
			"source":    "query",
			"query":     cfg.Query,
			"metric":    cfg.Metric,
			"value":     measured,
			"min_count": cfg.MinCount,
			"triggered": triggered,
		},
	}, nil
}
