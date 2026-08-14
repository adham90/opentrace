package watcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestCompare(t *testing.T) {
	tests := []struct {
		name      string
		value     float64
		op        store.WatchOperator
		threshold float64
		want      bool
	}{
		// GreaterThan
		{"gt: 5 > 3 is true", 5.0, store.WatchOpGreaterThan, 3.0, true},
		{"gt: 3 > 5 is false", 3.0, store.WatchOpGreaterThan, 5.0, false},
		{"gt: 3 > 3 is false (not strictly greater)", 3.0, store.WatchOpGreaterThan, 3.0, false},

		// GreaterThanEqual
		{"gte: 5 >= 3 is true", 5.0, store.WatchOpGreaterThanEqual, 3.0, true},
		{"gte: 3 >= 3 is true", 3.0, store.WatchOpGreaterThanEqual, 3.0, true},
		{"gte: 2 >= 3 is false", 2.0, store.WatchOpGreaterThanEqual, 3.0, false},

		// LessThan
		{"lt: 2 < 5 is true", 2.0, store.WatchOpLessThan, 5.0, true},
		{"lt: 5 < 2 is false", 5.0, store.WatchOpLessThan, 2.0, false},
		{"lt: 3 < 3 is false (not strictly less)", 3.0, store.WatchOpLessThan, 3.0, false},

		// LessThanEqual
		{"lte: 2 <= 5 is true", 2.0, store.WatchOpLessThanEqual, 5.0, true},
		{"lte: 3 <= 3 is true", 3.0, store.WatchOpLessThanEqual, 3.0, true},
		{"lte: 5 <= 3 is false", 5.0, store.WatchOpLessThanEqual, 3.0, false},

		// Equal
		{"eq: 3 == 3 is true", 3.0, store.WatchOpEqual, 3.0, true},
		{"eq: 3 == 5 is false", 3.0, store.WatchOpEqual, 5.0, false},

		// NotEqual
		{"neq: 3 != 5 is true", 3.0, store.WatchOpNotEqual, 5.0, true},
		{"neq: 3 != 3 is false", 3.0, store.WatchOpNotEqual, 3.0, false},

		// Edge cases with zero
		{"gt: 0 > 0 is false", 0.0, store.WatchOpGreaterThan, 0.0, false},
		{"gte: 0 >= 0 is true", 0.0, store.WatchOpGreaterThanEqual, 0.0, true},
		{"lt: 0 < 0 is false", 0.0, store.WatchOpLessThan, 0.0, false},
		{"lte: 0 <= 0 is true", 0.0, store.WatchOpLessThanEqual, 0.0, true},
		{"eq: 0 == 0 is true", 0.0, store.WatchOpEqual, 0.0, true},
		{"neq: 0 != 0 is false", 0.0, store.WatchOpNotEqual, 0.0, false},

		// Edge cases with negative values
		{"gt: -1 > -5 is true", -1.0, store.WatchOpGreaterThan, -5.0, true},
		{"lt: -5 < -1 is true", -5.0, store.WatchOpLessThan, -1.0, true},
		{"eq: -3 == -3 is true", -3.0, store.WatchOpEqual, -3.0, true},
		{"neq: -3 != -5 is true", -3.0, store.WatchOpNotEqual, -5.0, true},

		// Edge cases with very small differences
		{"gt: 1.0001 > 1.0 is true", 1.0001, store.WatchOpGreaterThan, 1.0, true},
		{"lt: 0.9999 < 1.0 is true", 0.9999, store.WatchOpLessThan, 1.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := compare(tt.value, tt.op, tt.threshold)
			if err != nil {
				t.Fatalf("compare(%v, %q, %v): %v", tt.value, tt.op, tt.threshold, err)
			}
			if got != tt.want {
				t.Errorf("compare(%v, %q, %v) = %v, want %v", tt.value, tt.op, tt.threshold, got, tt.want)
			}
		})
	}
}

// TestCompare_UnknownOperatorErrors pins the fix for the silently-false
// operator: a typo'd op used to make a watch run green forever. It must now be
// reported as a configuration error so the evaluator disables the watch.
func TestCompare_UnknownOperatorErrors(t *testing.T) {
	for _, op := range []store.WatchOperator{"greater_than", "unknown", "", ">"} {
		t.Run(string(op), func(t *testing.T) {
			got, err := compare(5.0, op, 3.0)
			if err == nil {
				t.Fatalf("compare with op %q returned (%v, nil), want a config error", op, got)
			}
			if !errors.Is(err, ErrInvalidWatchConfig) {
				t.Errorf("error = %v, want it to wrap ErrInvalidWatchConfig", err)
			}
		})
	}
}

// TestParseCountQuery_ValidatesLevelValue pins the silent-green fix: the field
// name `level` used to be enough, so `level:critical` was accepted, matched
// nothing in CountByLevel, and reported 0 on every check forever. An unknown
// level must now be a configuration error (so recordFailedCheck disables the
// watch), while every level ingest accepts — including the "warning" alias
// ingest canonicalises to "warn" — must be accepted and canonicalised.
func TestParseCountQuery_ValidatesLevelValue(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantErr   bool
		wantLevel string
	}{
		{name: "critical is not a level we store", query: "level:critical", wantErr: true},
		{name: "err typo rejected", query: "level:err", wantErr: true},
		{name: "empty-ish level rejected", query: "level: ", wantErr: true},
		{name: "warn accepted", query: "level:warn", wantLevel: "warn"},
		{name: "warning aliases to warn", query: "level:warning", wantLevel: "warn"},
		{name: "uppercase normalised", query: "level:ERROR", wantLevel: "error"},
		{name: "debug accepted", query: "level:debug", wantLevel: "debug"},
		{name: "info accepted", query: "level:info", wantLevel: "info"},
		{name: "fatal accepted", query: "level:fatal", wantLevel: "fatal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCountQuery(tt.query, "")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseCountQuery(%q) = %+v, want a config error", tt.query, got)
				}
				if !errors.Is(err, ErrInvalidWatchConfig) {
					t.Errorf("error = %v, want it to wrap ErrInvalidWatchConfig", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCountQuery(%q): %v", tt.query, err)
			}
			if got.Level != tt.wantLevel {
				t.Errorf("level = %q, want %q", got.Level, tt.wantLevel)
			}
		})
	}
}

// TestEvalCount_WarningQueryMatchesWarnRows proves a watch querying
// level:warning behaves exactly like one querying level:warn, because ingest
// stores "warning" as "warn". Before the fix the raw "warning" reached
// CountByLevel and matched nothing.
func TestEvalCount_WarningQueryMatchesWarnRows(t *testing.T) {
	for _, query := range []string{"level:warn", "level:warning"} {
		t.Run(query, func(t *testing.T) {
			ls := &rangeLogStore{countsFor: func(p store.LogCountParams) map[string]int {
				if p.Level != "warn" {
					return map[string]int{}
				}
				return map[string]int{"warn": 7}
			}}
			c := &Condition{Type: "count", Query: query, Op: store.WatchOpGreaterThan, Value: 5, Window: "1h"}
			got, err := EvaluateCondition(context.Background(), c, NewWatchMetrics(ls), nil, "prod", time.Minute)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if got.Value != 7 || !got.Breached {
				t.Errorf("got (value=%v, breached=%v), want (7, true)", got.Value, got.Breached)
			}
			calls := ls.calls()
			if len(calls) != 1 || calls[0].Level != "warn" {
				t.Fatalf("count params = %+v, want a single query with level=warn", calls)
			}
		})
	}
}

// TestEvalCount_InvalidLevelIsConfigError proves the bad level surfaces through
// the evaluator as ErrInvalidWatchConfig rather than a green count of 0.
func TestEvalCount_InvalidLevelIsConfigError(t *testing.T) {
	ls := &rangeLogStore{countsFor: func(store.LogCountParams) map[string]int { return map[string]int{} }}
	c := &Condition{Type: "count", Query: "level:critical", Op: store.WatchOpGreaterThan, Value: 100, Window: "1h"}
	got, err := EvaluateCondition(context.Background(), c, NewWatchMetrics(ls), nil, "prod", time.Minute)
	if err == nil {
		t.Fatalf("eval = %+v, want a config error", got)
	}
	if !errors.Is(err, ErrInvalidWatchConfig) {
		t.Errorf("error = %v, want it to wrap ErrInvalidWatchConfig", err)
	}
	if n := len(ls.calls()); n != 0 {
		t.Errorf("log store was queried %d times, want 0 — the config error must short-circuit", n)
	}
}

func TestBaselineValueForMetric(t *testing.T) {
	baseline := &store.WatchBaseline{
		ErrorRate:     0.15,
		AvgResponseMs: 250.5,
		P95ResponseMs: 800.3,
		LogCount:      1500,
		ErrorCount:    75,
		SQLCount:      12.4,
		CacheHitRate:  0.85,
	}

	tests := []struct {
		name   string
		metric store.WatchMetric
		want   float64
	}{
		{"ErrorRate", store.WatchMetricErrorRate, 0.15},
		{"ResponseTime", store.WatchMetricResponseTime, 250.5},
		{"P95Response", store.WatchMetricP95Response, 800.3},
		{"LogCount", store.WatchMetricLogCount, 1500.0},
		{"ErrorCount", store.WatchMetricErrorCount, 75.0},
		{"SQLCount", store.WatchMetricSQLCount, 12.4},
		{"CacheHitRate", store.WatchMetricCacheHitRate, 0.85},
		{"Unknown metric", store.WatchMetric("unknown_metric"), 0},
		{"Heartbeat (not in baseline)", store.WatchMetricHeartbeat, 0},
		{"Empty metric", store.WatchMetric(""), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := baselineValueForMetric(baseline, tt.metric)
			if got != tt.want {
				t.Errorf("baselineValueForMetric() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBaselineValueForMetric_NilBaseline(t *testing.T) {
	// baselineValueForMetric should handle being called with a valid baseline that has all zeros
	baseline := &store.WatchBaseline{}
	metrics := []store.WatchMetric{
		store.WatchMetricErrorRate,
		store.WatchMetricResponseTime,
		store.WatchMetricP95Response,
		store.WatchMetricLogCount,
		store.WatchMetricErrorCount,
		store.WatchMetricSQLCount,
		store.WatchMetricCacheHitRate,
	}

	for _, metric := range metrics {
		t.Run(string(metric)+"_zero_baseline", func(t *testing.T) {
			got := baselineValueForMetric(baseline, metric)
			if got != 0 {
				t.Errorf("baselineValueForMetric() for %s with zero baseline = %v, want 0", metric, got)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short string unchanged", "hello", 10, "hello"},
		{"exact length unchanged", "hello", 5, "hello"},
		{"long string truncated with ellipsis", "hello world", 5, "hello..."},
		{"empty string unchanged", "", 10, ""},
		{"single char within limit", "a", 1, "a"},
		{"single char truncated", "ab", 1, "a..."},
		{"maxLen zero truncates all", "hello", 0, "..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
