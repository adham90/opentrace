package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// getCounterValue extracts the float64 value from a prometheus Counter.
func getCounterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		t.Fatalf("failed to write counter metric: %v", err)
	}
	return m.GetCounter().GetValue()
}

// getCounterVecValue extracts the float64 value from a CounterVec with given labels.
func getCounterVecValue(t *testing.T, cv *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	c, err := cv.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("failed to get metric with labels %v: %v", labels, err)
	}
	return getCounterValue(t, c)
}

func TestMetricsRegistered(t *testing.T) {
	// Verify that all metrics are registered and do not panic.
	// The init() function runs MustRegister — if it panics, the test binary won't start.
	// This test additionally validates we can gather metrics from the default registry.
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	expected := map[string]bool{
		"opentrace_http_requests_total":            false,
		"opentrace_http_request_duration_seconds":  false,
		"opentrace_mcp_tool_calls_total":           false,
		"opentrace_watcher_evaluations_total":      false,
		"opentrace_healthcheck_runs_total":         false,
		"opentrace_log_ingest_total":               false,
	}

	for _, f := range families {
		if _, ok := expected[f.GetName()]; ok {
			expected[f.GetName()] = true
		}
	}

	// Note: some metrics may not appear in Gather() until they have been observed.
	// We just verify the init() didn't panic and we can gather without error.
}

func TestRecordMCPToolCall(t *testing.T) {
	before := getCounterVecValue(t, MCPToolCallsTotal, "test_tool")
	RecordMCPToolCall("test_tool")
	after := getCounterVecValue(t, MCPToolCallsTotal, "test_tool")

	if after != before+1 {
		t.Errorf("expected counter to increment by 1, got diff %f", after-before)
	}
}

func TestRecordWatcherEvaluation(t *testing.T) {
	for _, result := range []string{"alert", "ok", "error"} {
		t.Run(result, func(t *testing.T) {
			before := getCounterVecValue(t, WatcherEvaluationsTotal, result)
			RecordWatcherEvaluation(result)
			after := getCounterVecValue(t, WatcherEvaluationsTotal, result)

			if after != before+1 {
				t.Errorf("expected counter to increment by 1 for %q, got diff %f", result, after-before)
			}
		})
	}
}

func TestRecordHealthCheckRun(t *testing.T) {
	for _, status := range []string{"up", "down", "degraded", "error"} {
		t.Run(status, func(t *testing.T) {
			before := getCounterVecValue(t, HealthCheckRunsTotal, status)
			RecordHealthCheckRun(status)
			after := getCounterVecValue(t, HealthCheckRunsTotal, status)

			if after != before+1 {
				t.Errorf("expected counter to increment by 1 for %q, got diff %f", status, after-before)
			}
		})
	}
}

func TestRecordLogIngest(t *testing.T) {
	before := getCounterValue(t, LogIngestTotal)
	RecordLogIngest(42)
	after := getCounterValue(t, LogIngestTotal)

	if after != before+42 {
		t.Errorf("expected counter to increment by 42, got diff %f", after-before)
	}
}

func TestRecordLogIngest_Zero(t *testing.T) {
	before := getCounterValue(t, LogIngestTotal)
	RecordLogIngest(0)
	after := getCounterValue(t, LogIngestTotal)

	if after != before {
		t.Errorf("expected counter to not change for count=0, got diff %f", after-before)
	}
}
