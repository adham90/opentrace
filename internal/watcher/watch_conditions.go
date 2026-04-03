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

// ConditionResult holds the evaluation result of a single condition node.
type ConditionResult struct {
	Breached bool    `json:"breached"`
	Summary  string  `json:"summary"`
	Value    float64 `json:"value,omitempty"`
}

// ParseCondition parses a JSON condition tree.
func ParseCondition(raw json.RawMessage) (*Condition, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty condition")
	}
	var c Condition
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parsing condition: %w", err)
	}
	return &c, nil
}

// EvaluateCondition recursively evaluates a condition tree.
func EvaluateCondition(ctx context.Context, c *Condition, metrics *WatchMetrics, baseline *store.WatchBaseline, checkWindow time.Duration) (*ConditionResult, error) {
	// Combinators
	if len(c.All) > 0 {
		return evalAll(ctx, c.All, metrics, baseline, checkWindow)
	}
	if len(c.Any) > 0 {
		return evalAny(ctx, c.Any, metrics, baseline, checkWindow)
	}
	if c.Not != nil {
		r, err := EvaluateCondition(ctx, c.Not, metrics, baseline, checkWindow)
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
		return evalThreshold(ctx, c, metrics, checkWindow)
	case "relative":
		return evalRelative(ctx, c, metrics, baseline, checkWindow)
	case "delta":
		return evalDelta(ctx, c, metrics, checkWindow)
	case "count":
		return evalCount(ctx, c, metrics)
	default:
		return nil, fmt.Errorf("unknown condition type: %q", c.Type)
	}
}

func evalAll(ctx context.Context, conditions []*Condition, metrics *WatchMetrics, baseline *store.WatchBaseline, checkWindow time.Duration) (*ConditionResult, error) {
	var summaries []string
	allBreached := true
	for _, c := range conditions {
		r, err := EvaluateCondition(ctx, c, metrics, baseline, checkWindow)
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

func evalAny(ctx context.Context, conditions []*Condition, metrics *WatchMetrics, baseline *store.WatchBaseline, checkWindow time.Duration) (*ConditionResult, error) {
	var summaries []string
	anyBreached := false
	for _, c := range conditions {
		r, err := EvaluateCondition(ctx, c, metrics, baseline, checkWindow)
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

func evalThreshold(ctx context.Context, c *Condition, metrics *WatchMetrics, checkWindow time.Duration) (*ConditionResult, error) {
	value, err := metrics.Measure(ctx, c.Metric, c.Service, c.Endpoint, checkWindow)
	if err != nil {
		return nil, fmt.Errorf("measuring %s: %w", c.Metric, err)
	}
	breached := compare(value, c.Op, c.Value)
	return &ConditionResult{
		Breached: breached,
		Value:    value,
		Summary:  fmt.Sprintf("%s = %.4f %s %.4f", c.Metric, value, c.Op, c.Value),
	}, nil
}

func evalRelative(ctx context.Context, c *Condition, metrics *WatchMetrics, baseline *store.WatchBaseline, checkWindow time.Duration) (*ConditionResult, error) {
	if baseline == nil {
		return &ConditionResult{Summary: "no baseline available for relative comparison"}, nil
	}

	value, err := metrics.Measure(ctx, c.Metric, c.Service, c.Endpoint, checkWindow)
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
	breached := compare(value, c.Op, threshold)
	return &ConditionResult{
		Breached: breached,
		Value:    value,
		Summary:  fmt.Sprintf("%s = %.4f %s %.4f (baseline %.4f × %.1f)", c.Metric, value, c.Op, threshold, baselineValue, c.BaselineMultiple),
	}, nil
}

func evalDelta(ctx context.Context, c *Condition, metrics *WatchMetrics, checkWindow time.Duration) (*ConditionResult, error) {
	compareWindow := checkWindow
	if c.CompareWindow != "" {
		if d, err := time.ParseDuration(c.CompareWindow); err == nil {
			compareWindow = d
		}
	}

	currentValue, err := metrics.Measure(ctx, c.Metric, c.Service, c.Endpoint, checkWindow)
	if err != nil {
		return nil, fmt.Errorf("measuring current %s: %w", c.Metric, err)
	}

	// Measure the same metric for the previous window by shifting time.
	// We can't directly shift time in WatchMetrics, so we use 2x the window
	// and approximate: previousValue ≈ metric over (2*window) minus current.
	// This is a pragmatic approximation for SQLite.
	wideValue, err := metrics.Measure(ctx, c.Metric, c.Service, c.Endpoint, checkWindow+compareWindow)
	if err != nil {
		return nil, fmt.Errorf("measuring wide %s: %w", c.Metric, err)
	}

	// For rate metrics (error_rate, cache_hit_rate), the wide window is already an average.
	// For count metrics, we need to subtract. Approximate previous = wide - current.
	previousValue := wideValue
	if isCountMetric(c.Metric) {
		previousValue = math.Max(0, wideValue-currentValue)
	}

	if previousValue == 0 {
		return &ConditionResult{
			Value:   currentValue,
			Summary: fmt.Sprintf("%s = %.4f (previous was 0, cannot compute delta)", c.Metric, currentValue),
		}, nil
	}

	deltaPct := ((currentValue - previousValue) / math.Abs(previousValue)) * 100
	breached := compare(math.Abs(deltaPct), c.Op, c.ChangePct)
	return &ConditionResult{
		Breached: breached,
		Value:    deltaPct,
		Summary:  fmt.Sprintf("%s changed %.1f%% (%.4f → %.4f) %s %.1f%%", c.Metric, deltaPct, previousValue, currentValue, c.Op, c.ChangePct),
	}, nil
}

func evalCount(ctx context.Context, c *Condition, metrics *WatchMetrics) (*ConditionResult, error) {
	if metrics.logStore == nil {
		return nil, fmt.Errorf("LogStore not available for count condition")
	}

	window := 1 * time.Hour
	if c.Window != "" {
		if d, err := time.ParseDuration(c.Window); err == nil {
			window = d
		}
	}

	now := time.Now().UTC()
	start := now.Add(-window)

	if c.Distinct && c.Field != "" {
		// COUNT DISTINCT via DistinctValues
		field := c.Field
		values, err := metrics.logStore.DistinctValues(ctx, field, store.LogCountParams{
			Since:   start,
			Until:   now,
			Service: c.Service,
		})
		if err != nil {
			return nil, fmt.Errorf("counting distinct %s: %w", field, err)
		}
		count := float64(len(values))
		breached := compare(count, c.Op, c.Value)
		return &ConditionResult{
			Breached: breached,
			Value:    count,
			Summary:  fmt.Sprintf("distinct(%s) = %.0f %s %.0f in %s", field, count, c.Op, c.Value, window),
		}, nil
	}

	// Plain count: use log count
	counts, err := metrics.logStore.CountByLevel(ctx, store.LogCountParams{
		Since:   start,
		Until:   now,
		Service: c.Service,
	})
	if err != nil {
		return nil, fmt.Errorf("counting logs: %w", err)
	}

	total := 0
	for _, count := range counts {
		total += count
	}
	value := float64(total)
	breached := compare(value, c.Op, c.Value)
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
