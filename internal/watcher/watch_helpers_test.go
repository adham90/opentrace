package watcher

import (
	"testing"

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

		// Unknown operator
		{"unknown op returns false", 5.0, store.WatchOperator("unknown"), 3.0, false},
		{"empty op returns false", 5.0, store.WatchOperator(""), 3.0, false},

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
			got := compare(tt.value, tt.op, tt.threshold)
			if got != tt.want {
				t.Errorf("compare(%v, %q, %v) = %v, want %v", tt.value, tt.op, tt.threshold, got, tt.want)
			}
		})
	}
}

func TestGetBaselineMetricValue(t *testing.T) {
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
		watch  *store.Watch
		want   float64
	}{
		{
			name: "ErrorRate returns BaselineJSON.ErrorRate",
			watch: &store.Watch{
				Metric:       store.WatchMetricErrorRate,
				BaselineJSON: baseline,
			},
			want: 0.15,
		},
		{
			name: "ResponseTime returns BaselineJSON.AvgResponseMs",
			watch: &store.Watch{
				Metric:       store.WatchMetricResponseTime,
				BaselineJSON: baseline,
			},
			want: 250.5,
		},
		{
			name: "P95Response returns BaselineJSON.P95ResponseMs",
			watch: &store.Watch{
				Metric:       store.WatchMetricP95Response,
				BaselineJSON: baseline,
			},
			want: 800.3,
		},
		{
			name: "LogCount returns float64(BaselineJSON.LogCount)",
			watch: &store.Watch{
				Metric:       store.WatchMetricLogCount,
				BaselineJSON: baseline,
			},
			want: 1500.0,
		},
		{
			name: "ErrorCount returns float64(BaselineJSON.ErrorCount)",
			watch: &store.Watch{
				Metric:       store.WatchMetricErrorCount,
				BaselineJSON: baseline,
			},
			want: 75.0,
		},
		{
			name: "SQLCount returns BaselineJSON.SQLCount",
			watch: &store.Watch{
				Metric:       store.WatchMetricSQLCount,
				BaselineJSON: baseline,
			},
			want: 12.4,
		},
		{
			name: "CacheHitRate returns BaselineJSON.CacheHitRate",
			watch: &store.Watch{
				Metric:       store.WatchMetricCacheHitRate,
				BaselineJSON: baseline,
			},
			want: 0.85,
		},
		{
			name: "nil BaselineJSON returns 0",
			watch: &store.Watch{
				Metric:       store.WatchMetricErrorRate,
				BaselineJSON: nil,
			},
			want: 0,
		},
		{
			name: "unknown metric returns 0",
			watch: &store.Watch{
				Metric:       store.WatchMetric("unknown_metric"),
				BaselineJSON: baseline,
			},
			want: 0,
		},
		{
			name: "heartbeat metric returns 0 (not in baseline)",
			watch: &store.Watch{
				Metric:       store.WatchMetricHeartbeat,
				BaselineJSON: baseline,
			},
			want: 0,
		},
		{
			name: "empty metric string returns 0",
			watch: &store.Watch{
				Metric:       store.WatchMetric(""),
				BaselineJSON: baseline,
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := baselineMetricValue(tt.watch)
			if got != tt.want {
				t.Errorf("baselineMetricValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetBaselineMetricValue_ZeroBaseline(t *testing.T) {
	// Test with a baseline where all values are zero
	baseline := &store.WatchBaseline{
		ErrorRate:     0,
		AvgResponseMs: 0,
		P95ResponseMs: 0,
		LogCount:      0,
		ErrorCount:    0,
		SQLCount:      0,
		CacheHitRate:  0,
	}

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
			w := &store.Watch{
				Metric:       metric,
				BaselineJSON: baseline,
			}
			got := baselineMetricValue(w)
			if got != 0 {
				t.Errorf("baselineMetricValue() for %s with zero baseline = %v, want 0", metric, got)
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
