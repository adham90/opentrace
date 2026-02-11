package watcher

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// RuleEvaluator evaluates rule-based monitors (query, logs, health).
type RuleEvaluator struct {
	registry *connector.Registry
	logStore store.LogStore
	dsStore  store.DataSourceStore
}

// NewRuleEvaluator creates a new rule evaluator.
func NewRuleEvaluator(
	registry *connector.Registry,
	logStore store.LogStore,
	dsStore store.DataSourceStore,
) *RuleEvaluator {
	return &RuleEvaluator{
		registry: registry,
		logStore: logStore,
		dsStore:  dsStore,
	}
}

// Evaluate dispatches to the appropriate evaluation method based on rule source.
func (re *RuleEvaluator) Evaluate(ctx context.Context, w store.Watcher) (*EvalResult, error) {
	if w.RuleConfig == nil {
		return nil, fmt.Errorf("rule monitor %s has no rule_config", w.ID)
	}

	switch w.RuleConfig.Source {
	case store.RuleSourceQuery:
		return re.evaluateQuery(ctx, w)
	case store.RuleSourceLogs:
		return re.evaluateLogs(ctx, w)
	case store.RuleSourceHealth:
		return re.evaluateHealth(ctx, w)
	default:
		return nil, fmt.Errorf("unknown rule source: %s", w.RuleConfig.Source)
	}
}

// evaluateQuery runs a SQL query via the database connector and compares the result.
func (re *RuleEvaluator) evaluateQuery(ctx context.Context, w store.Watcher) (*EvalResult, error) {
	cfg := w.RuleConfig

	// Get the database connector from the registry
	ds := re.registry.Get(connector.ConnectorDatabase)
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
		return nil, fmt.Errorf("unknown metric: %s", cfg.Metric)
	}

	triggered := compareValues(measured, cfg.Operator, cfg.Threshold)
	summary := formatQuerySummary(cfg, measured, triggered)

	return &EvalResult{
		HasAlert: triggered,
		Summary:  summary,
		Value:    &measured,
		Details: map[string]any{
			"query":     cfg.Query,
			"metric":    cfg.Metric,
			"value":     measured,
			"operator":  cfg.Operator,
			"threshold": cfg.Threshold,
			"triggered": triggered,
		},
	}, nil
}

// evaluateLogs counts matching logs within a time window and compares against threshold.
func (re *RuleEvaluator) evaluateLogs(ctx context.Context, w store.Watcher) (*EvalResult, error) {
	cfg := w.RuleConfig

	window := ParseTimeRange(cfg.TimeWindow)
	start := time.Now().Add(-window)

	// Cap fetch limit based on threshold to avoid fetching excessive rows.
	// We only need threshold+1 rows to determine if the condition triggers.
	limit := int(cfg.Threshold) + 1
	if limit < 100 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}

	params := store.LogSearchParams{
		Start: &start,
		Limit: limit,
	}
	if cfg.Filter != nil {
		params.Service = cfg.Filter.Service
		params.Level = cfg.Filter.Level
		params.Query = cfg.Filter.Query
		params.Environment = cfg.Filter.Environment
	}

	logs, err := re.logStore.Search(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("searching logs: %w", err)
	}

	measured := float64(len(logs))
	triggered := compareValues(measured, cfg.Operator, cfg.Threshold)
	summary := formatLogSummary(cfg, measured, triggered, window)

	return &EvalResult{
		HasAlert: triggered,
		Summary:  summary,
		Value:    &measured,
		Details: map[string]any{
			"filter":      cfg.Filter,
			"time_window": cfg.TimeWindow,
			"count":       measured,
			"operator":    cfg.Operator,
			"threshold":   cfg.Threshold,
			"triggered":   triggered,
		},
	}, nil
}

// evaluateHealth checks connectivity and latency of a data source.
func (re *RuleEvaluator) evaluateHealth(ctx context.Context, w store.Watcher) (*EvalResult, error) {
	cfg := w.RuleConfig

	ds := re.registry.Get(connector.ConnectorDatabase)
	if ds == nil {
		return nil, fmt.Errorf("no database connector available")
	}

	hc, ok := ds.(connector.HealthChecker)
	if !ok {
		return nil, fmt.Errorf("database connector does not support health checks")
	}

	pr := hc.Ping(ctx)

	triggered := false
	var reasons []string

	for _, check := range cfg.Checks {
		switch check {
		case "connection":
			if !pr.Reachable {
				triggered = true
				reasons = append(reasons, fmt.Sprintf("connection failed: %s", pr.Error))
			}
		case "latency":
			threshold := int64(cfg.LatencyThreshold)
			if threshold <= 0 {
				threshold = 2000
			}
			if pr.LatencyMS > threshold {
				triggered = true
				reasons = append(reasons, fmt.Sprintf("latency %dms exceeds %dms", pr.LatencyMS, threshold))
			}
		}
	}

	summary := formatHealthSummary(pr, triggered, reasons)
	latency := float64(pr.LatencyMS)

	return &EvalResult{
		HasAlert: triggered,
		Summary:  summary,
		Value:    &latency,
		Details: map[string]any{
			"reachable":  pr.Reachable,
			"latency_ms": pr.LatencyMS,
			"error":      pr.Error,
			"triggered":  triggered,
			"reasons":    reasons,
		},
	}, nil
}

// compareValues applies the operator to compare measured against threshold.
func compareValues(measured float64, op store.RuleOperator, threshold float64) bool {
	switch op {
	case store.OpGreaterThan:
		return measured > threshold
	case store.OpGreaterThanEqual:
		return measured >= threshold
	case store.OpLessThan:
		return measured < threshold
	case store.OpLessThanEqual:
		return measured <= threshold
	case store.OpEqual:
		return measured == threshold
	case store.OpNotEqual:
		return measured != threshold
	default:
		return false
	}
}

// toFloat64 converts a database value to float64.
func toFloat64(v any) (float64, error) {
	switch val := v.(type) {
	case int:
		return float64(val), nil
	case int16:
		return float64(val), nil
	case int32:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case float32:
		return float64(val), nil
	case float64:
		return val, nil
	case string:
		var f float64
		_, err := fmt.Sscanf(val, "%f", &f)
		return f, err
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}

func formatQuerySummary(cfg *store.RuleConfig, value float64, triggered bool) string {
	opStr := operatorString(cfg.Operator)
	if triggered {
		return fmt.Sprintf("ALERT: %s is %.0f (%s %.0f)", cfg.Metric, value, opStr, cfg.Threshold)
	}
	return fmt.Sprintf("OK: %s is %.0f (threshold: %s %.0f)", cfg.Metric, value, opStr, cfg.Threshold)
}

func formatLogSummary(cfg *store.RuleConfig, count float64, triggered bool, window time.Duration) string {
	if triggered {
		return fmt.Sprintf("ALERT: %0.f matching logs in last %s (threshold: %s %.0f)",
			count, window, operatorString(cfg.Operator), cfg.Threshold)
	}
	return fmt.Sprintf("OK: %.0f matching logs in last %s (threshold: %s %.0f)",
		count, window, operatorString(cfg.Operator), cfg.Threshold)
}

func formatHealthSummary(pr connector.PingResult, triggered bool, reasons []string) string {
	if triggered {
		msg := "ALERT: Health check failed"
		if len(reasons) > 0 {
			msg += " — " + reasons[0]
			for _, r := range reasons[1:] {
				msg += "; " + r
			}
		}
		return msg
	}
	return fmt.Sprintf("OK: Database reachable (latency: %dms)", pr.LatencyMS)
}

func operatorString(op store.RuleOperator) string {
	switch op {
	case store.OpGreaterThan:
		return ">"
	case store.OpGreaterThanEqual:
		return ">="
	case store.OpLessThan:
		return "<"
	case store.OpLessThanEqual:
		return "<="
	case store.OpEqual:
		return "="
	case store.OpNotEqual:
		return "!="
	default:
		return string(op)
	}
}
