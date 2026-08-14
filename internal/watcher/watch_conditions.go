// Package watcher provides the alerting engine.
//
// Watch conditions use a JSON tree structure that the AI agent can generate
// to express complex alert rules. Each leaf node is a typed condition
// (threshold, relative, delta, count), and nodes can be combined with
// all (AND), any (OR), and not operators.
//
// Examples:
//
//	Simple threshold:
//	  {"type":"threshold","metric":"error_rate","op":"gt","value":0.05,"service":"api"}
//
//	Compound AND:
//	  {"all":[
//	    {"type":"threshold","metric":"error_rate","op":"gt","value":0.05,"service":"api"},
//	    {"type":"threshold","metric":"response_time","op":"gt","value":500,"service":"api"}
//	  ]}
//
//	Relative to baseline:
//	  {"type":"relative","metric":"error_rate","op":"gt","baseline_multiple":2.0,"service":"api"}
//
//	Rate of change:
//	  {"type":"delta","metric":"error_rate","compare_window":"1h","op":"gt","change_pct":50}
//
//	Count distinct:
//	  {"type":"count","query":"level:error","field":"error_fingerprint","distinct":true,"op":"gt","value":10,"window":"1h"}
package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// Condition is the JSON-serializable tree node for watch conditions.
// It is either a leaf (Type is set) or a combinator (All/Any/Not is set).
type Condition struct {
	// Combinators — at most one should be set.
	All []*Condition `json:"all,omitempty"`
	Any []*Condition `json:"any,omitempty"`
	Not *Condition   `json:"not,omitempty"`

	// Leaf condition fields.
	Type string `json:"type,omitempty"` // "threshold", "relative", "delta", "count"

	// Common fields (used by threshold, relative, delta).
	Metric   store.WatchMetric   `json:"metric,omitempty"`
	Op       store.WatchOperator `json:"op,omitempty"`
	Service  string              `json:"service,omitempty"`
	Endpoint string              `json:"endpoint,omitempty"`

	// threshold: metric <op> value
	Value float64 `json:"value,omitempty"`

	// relative: metric <op> baseline * baseline_multiple
	BaselineMultiple float64 `json:"baseline_multiple,omitempty"`

	// delta: metric changed by <op> change_pct% vs compare_window ago
	ChangePct     float64 `json:"change_pct,omitempty"`
	CompareWindow string  `json:"compare_window,omitempty"` // e.g. "1h", "30m"

	// count: count (distinct) <field> where <query> <op> value
	Field    string `json:"field,omitempty"`    // e.g. "error_fingerprint", "user_id"
	Distinct bool   `json:"distinct,omitempty"` // COUNT DISTINCT vs COUNT
	Query    string `json:"query,omitempty"`    // log search query filter (e.g. "level:error")
	Window   string `json:"window,omitempty"`   // time window for count
}

// defaultCountWindow is the window a count condition uses when it does not
// declare one.
const defaultCountWindow = 1 * time.Hour

// ConditionResult holds the evaluation result of a single condition node.
type ConditionResult struct {
	Breached bool    `json:"breached"`
	Summary  string  `json:"summary"`
	Value    float64 `json:"value,omitempty"`
}

// ParseCondition parses a JSON condition tree.
func ParseCondition(raw json.RawMessage) (*Condition, error) {
	if len(raw) == 0 {
		return nil, configErrorf("empty condition")
	}
	var c Condition
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, configErrorf("parsing condition: %v", err)
	}
	return &c, nil
}

// EvaluateCondition recursively evaluates a condition tree.
// environment scopes every underlying metric query; callers should pass the
// owning watch's Environment so metrics are computed against that env's
// traffic only.
func EvaluateCondition(ctx context.Context, c *Condition, metrics *WatchMetrics, baseline *store.WatchBaseline, environment string, checkWindow time.Duration) (*ConditionResult, error) {
	// Combinators
	if len(c.All) > 0 {
		return evalAll(ctx, c.All, metrics, baseline, environment, checkWindow)
	}
	if len(c.Any) > 0 {
		return evalAny(ctx, c.Any, metrics, baseline, environment, checkWindow)
	}
	if c.Not != nil {
		r, err := EvaluateCondition(ctx, c.Not, metrics, baseline, environment, checkWindow)
		if err != nil {
			return nil, err
		}
		return &ConditionResult{
			Breached: !r.Breached,
			Summary:  fmt.Sprintf("NOT(%s)", r.Summary),
			Value:    r.Value,
		}, nil
	}

	// Leaf conditions
	switch c.Type {
	case "threshold":
		return evalThreshold(ctx, c, metrics, environment, checkWindow)
	case "relative":
		return evalRelative(ctx, c, metrics, baseline, environment, checkWindow)
	case "delta":
		return evalDelta(ctx, c, metrics, environment, checkWindow)
	case "count":
		return evalCount(ctx, c, metrics, environment)
	default:
		return nil, configErrorf("unknown condition type: %q", c.Type)
	}
}

func evalAll(ctx context.Context, conditions []*Condition, metrics *WatchMetrics, baseline *store.WatchBaseline, environment string, checkWindow time.Duration) (*ConditionResult, error) {
	var summaries []string
	allBreached := true
	for _, c := range conditions {
		r, err := EvaluateCondition(ctx, c, metrics, baseline, environment, checkWindow)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, r.Summary)
		if !r.Breached {
			allBreached = false
		}
	}
	return &ConditionResult{
		Breached: allBreached,
		Summary:  fmt.Sprintf("ALL(%v): %v", allBreached, summaries),
	}, nil
}

func evalAny(ctx context.Context, conditions []*Condition, metrics *WatchMetrics, baseline *store.WatchBaseline, environment string, checkWindow time.Duration) (*ConditionResult, error) {
	var summaries []string
	anyBreached := false
	for _, c := range conditions {
		r, err := EvaluateCondition(ctx, c, metrics, baseline, environment, checkWindow)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, r.Summary)
		if r.Breached {
			anyBreached = true
		}
	}
	return &ConditionResult{
		Breached: anyBreached,
		Summary:  fmt.Sprintf("ANY(%v): %v", anyBreached, summaries),
	}, nil
}

// --- Leaf evaluators ---

func evalThreshold(ctx context.Context, c *Condition, metrics *WatchMetrics, environment string, checkWindow time.Duration) (*ConditionResult, error) {
	value, err := metrics.Measure(ctx, c.Metric, c.Service, c.Endpoint, environment, checkWindow)
	if err != nil {
		return nil, fmt.Errorf("measuring %s: %w", c.Metric, err)
	}
	breached, err := compare(value, c.Op, c.Value)
	if err != nil {
		return nil, err
	}
	return &ConditionResult{
		Breached: breached,
		Value:    value,
		Summary:  fmt.Sprintf("%s = %.4f %s %.4f", c.Metric, value, c.Op, c.Value),
	}, nil
}

func evalRelative(ctx context.Context, c *Condition, metrics *WatchMetrics, baseline *store.WatchBaseline, environment string, checkWindow time.Duration) (*ConditionResult, error) {
	if baseline == nil {
		return &ConditionResult{Summary: "no baseline available for relative comparison"}, nil
	}

	// Measure over the same window the baseline was captured over. Comparing a
	// 30s log_count against a baseline captured over an hour is meaningless;
	// the baseline records the window it used, so use it.
	window := parseDurationOr(baseline.WindowDuration, checkWindow)
	value, err := metrics.Measure(ctx, c.Metric, c.Service, c.Endpoint, environment, window)
	if err != nil {
		return nil, fmt.Errorf("measuring %s: %w", c.Metric, err)
	}

	baselineValue := baselineValueForMetric(baseline, c.Metric)
	if baselineValue == 0 {
		return &ConditionResult{
			Value:   value,
			Summary: fmt.Sprintf("%s = %.4f (baseline is 0, cannot compare)", c.Metric, value),
		}, nil
	}

	threshold := baselineValue * c.BaselineMultiple
	breached, err := compare(value, c.Op, threshold)
	if err != nil {
		return nil, err
	}
	return &ConditionResult{
		Breached: breached,
		Value:    value,
		Summary:  fmt.Sprintf("%s = %.4f %s %.4f (baseline %.4f × %.1f)", c.Metric, value, c.Op, threshold, baselineValue, c.BaselineMultiple),
	}, nil
}

// evalDelta compares the metric over the current measurement window against the
// same metric over the compare_window that immediately precedes it.
//
// The previous window is measured directly — [now-check-compare, now-check) —
// rather than being approximated from a wide window that contains the current
// one. The old approximation made previousValue a blend of both halves, which
// bounded a rate delta inside ±100% and made any change_pct >= 100 threshold
// mathematically unsatisfiable. Count metrics are extensive quantities, so when
// the two windows have different lengths the previous count is scaled to the
// current window's length before the comparison.
func evalDelta(ctx context.Context, c *Condition, metrics *WatchMetrics, environment string, checkWindow time.Duration) (*ConditionResult, error) {
	compareWindow := parseDurationOr(c.CompareWindow, checkWindow)

	now := time.Now().UTC()
	currentStart := now.Add(-checkWindow)
	previousStart := currentStart.Add(-compareWindow)

	currentValue, err := metrics.MeasureRange(ctx, c.Metric, c.Service, c.Endpoint, environment, currentStart, now)
	if err != nil {
		return nil, fmt.Errorf("measuring current %s: %w", c.Metric, err)
	}

	previousValue, err := metrics.MeasureRange(ctx, c.Metric, c.Service, c.Endpoint, environment, previousStart, currentStart)
	if err != nil {
		return nil, fmt.Errorf("measuring previous %s: %w", c.Metric, err)
	}

	// Normalize extensive (count) metrics for unequal window lengths so a 24h
	// compare window is not reported as a permanent -95% "drop".
	if isCountMetric(c.Metric) && compareWindow != checkWindow && compareWindow > 0 {
		previousValue *= checkWindow.Seconds() / compareWindow.Seconds()
	}

	if previousValue == 0 {
		return &ConditionResult{
			Value:   currentValue,
			Summary: fmt.Sprintf("%s = %.4f (previous was 0, cannot compute delta)", c.Metric, currentValue),
		}, nil
	}

	deltaPct := ((currentValue - previousValue) / math.Abs(previousValue)) * 100
	breached, err := compare(math.Abs(deltaPct), c.Op, c.ChangePct)
	if err != nil {
		return nil, err
	}
	return &ConditionResult{
		Breached: breached,
		Value:    deltaPct,
		Summary:  fmt.Sprintf("%s changed %.1f%% (%.4f → %.4f) %s %.1f%%", c.Metric, deltaPct, previousValue, currentValue, c.Op, c.ChangePct),
	}, nil
}

func evalCount(ctx context.Context, c *Condition, metrics *WatchMetrics, environment string) (*ConditionResult, error) {
	if metrics.logStore == nil {
		return nil, fmt.Errorf("LogStore not available for count condition")
	}

	window := parseDurationOr(c.Window, defaultCountWindow)

	// The documented `query` filter (e.g. "level:error") is applied for real.
	// Anything the log store cannot express is rejected — silently ignoring it
	// turned "count errors > 100" into "count all logs > 100".
	filter, err := parseCountQuery(c.Query, c.Service)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	params := filter.apply(store.LogCountParams{
		Since:       now.Add(-window),
		Until:       now,
		Service:     c.Service,
		Environment: environment,
	})

	if c.Distinct && c.Field != "" {
		// COUNT DISTINCT via DistinctValues
		field := c.Field
		values, err := metrics.logStore.DistinctValues(ctx, field, params)
		if err != nil {
			return nil, fmt.Errorf("counting distinct %s: %w", field, err)
		}
		count := float64(len(values))
		breached, err := compare(count, c.Op, c.Value)
		if err != nil {
			return nil, err
		}
		return &ConditionResult{
			Breached: breached,
			Value:    count,
			Summary:  fmt.Sprintf("distinct(%s) = %.0f %s %.0f in %s", field, count, c.Op, c.Value, window),
		}, nil
	}

	// Plain count: use log count, honouring the query's level filter.
	counts, err := metrics.logStore.CountByLevel(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("counting logs: %w", err)
	}

	total := 0
	for _, count := range counts {
		total += count
	}
	value := float64(total)
	breached, err := compare(value, c.Op, c.Value)
	if err != nil {
		return nil, err
	}
	return &ConditionResult{
		Breached: breached,
		Value:    value,
		Summary:  fmt.Sprintf("count = %.0f %s %.0f in %s", value, c.Op, c.Value, window),
	}, nil
}

// --- Helpers ---

func baselineValueForMetric(b *store.WatchBaseline, metric store.WatchMetric) float64 {
	switch metric {
	case store.WatchMetricErrorRate:
		return b.ErrorRate
	case store.WatchMetricResponseTime:
		return b.AvgResponseMs
	case store.WatchMetricP95Response:
		return b.P95ResponseMs
	case store.WatchMetricLogCount:
		return float64(b.LogCount)
	case store.WatchMetricErrorCount:
		return float64(b.ErrorCount)
	case store.WatchMetricSQLCount:
		return b.SQLCount
	case store.WatchMetricCacheHitRate:
		return b.CacheHitRate
	default:
		return 0
	}
}

func isCountMetric(m store.WatchMetric) bool {
	switch m {
	case store.WatchMetricLogCount, store.WatchMetricErrorCount:
		return true
	default:
		return false
	}
}
