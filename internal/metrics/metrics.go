package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// HTTPRequestsTotal counts total HTTP requests by method, path, and status.
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "opentrace_http_requests_total",
			Help: "Total number of HTTP requests.",
		},
		[]string{"method", "path", "status"},
	)

	// HTTPRequestDuration measures HTTP request duration in seconds.
	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "opentrace_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	// MCPToolCallsTotal counts total MCP tool calls by tool name.
	MCPToolCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "opentrace_mcp_tool_calls_total",
			Help: "Total MCP tool calls.",
		},
		[]string{"tool"},
	)

	// WatcherEvaluationsTotal counts total watcher evaluations by result.
	WatcherEvaluationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "opentrace_watcher_evaluations_total",
			Help: "Total watcher evaluations.",
		},
		[]string{"result"},
	)

	// HealthCheckRunsTotal counts total health check runs by status.
	HealthCheckRunsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "opentrace_healthcheck_runs_total",
			Help: "Total health check runs.",
		},
		[]string{"status"},
	)

	// LogIngestTotal counts total logs ingested.
	LogIngestTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "opentrace_log_ingest_total",
			Help: "Total logs ingested.",
		},
	)
)

func init() {
	prometheus.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,
		MCPToolCallsTotal,
		WatcherEvaluationsTotal,
		HealthCheckRunsTotal,
		LogIngestTotal,
	)
}

// RecordMCPToolCall increments the MCP tool call counter for the given tool name.
func RecordMCPToolCall(tool string) {
	MCPToolCallsTotal.WithLabelValues(tool).Inc()
}

// RecordWatcherEvaluation increments the watcher evaluation counter.
// result should be "alert", "ok", or "error".
func RecordWatcherEvaluation(result string) {
	WatcherEvaluationsTotal.WithLabelValues(result).Inc()
}

// RecordHealthCheckRun increments the health check run counter.
// status should be "up", "down", "degraded", or "error".
func RecordHealthCheckRun(status string) {
	HealthCheckRunsTotal.WithLabelValues(status).Inc()
}

// RecordLogIngest adds count to the log ingest counter.
func RecordLogIngest(count int) {
	LogIngestTotal.Add(float64(count))
}
