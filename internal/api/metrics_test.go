package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func getCounterVecValue(t *testing.T, labels ...string) float64 {
	t.Helper()
	c, err := metrics.HTTPRequestsTotal.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("failed to get metric with labels %v: %v", labels, err)
	}
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		t.Fatalf("failed to write counter metric: %v", err)
	}
	return m.GetCounter().GetValue()
}

func TestPrometheusMiddleware_RecordsRequestCount(t *testing.T) {
	handler := PrometheusMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	before := getCounterVecValue(t, "GET", "/api/logs", "200")

	req := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	after := getCounterVecValue(t, "GET", "/api/logs", "200")

	if after != before+1 {
		t.Errorf("expected request counter to increment by 1, got diff %f", after-before)
	}
}

func TestPrometheusMiddleware_RecordsStatusCode(t *testing.T) {
	handler := PrometheusMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	before := getCounterVecValue(t, "POST", "/api/connectors", "404")

	req := httptest.NewRequest(http.MethodPost, "/api/connectors", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	after := getCounterVecValue(t, "POST", "/api/connectors", "404")

	if after != before+1 {
		t.Errorf("expected request counter to increment by 1 for 404, got diff %f", after-before)
	}
}

func TestPrometheusMiddleware_SkipsMetricsEndpoint(t *testing.T) {
	innerCalled := false
	handler := PrometheusMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/debug/metrics", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !innerCalled {
		t.Error("expected inner handler to be called for /debug/metrics")
	}
	// The /debug/metrics path itself should not be recorded
	val := getCounterVecValue(t, "GET", "/debug/metrics", "200")
	if val > 0 {
		t.Errorf("expected no metric recording for /debug/metrics, got %f", val)
	}
}

// collectMethodLabels returns every distinct value of the "method" label
// currently present in the HTTPRequestsTotal series set.
func collectMethodLabels(t *testing.T) map[string]bool {
	t.Helper()
	ch := make(chan prometheus.Metric, 1024)
	go func() {
		metrics.HTTPRequestsTotal.Collect(ch)
		close(ch)
	}()
	seen := make(map[string]bool)
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatalf("writing metric: %v", err)
		}
		for _, lp := range pb.GetLabel() {
			if lp.GetName() == "method" {
				seen[lp.GetValue()] = true
			}
		}
	}
	return seen
}

// TestPrometheusMiddleware_BoundsMethodLabel proves the method label is bounded.
// net/http hands any RFC 7230 token through to the handler, and this middleware
// runs before any authentication, so an unauthenticated `curl -X <random>` loop
// would otherwise mint one permanent time series per request.
func TestPrometheusMiddleware_BoundsMethodLabel(t *testing.T) {
	handler := PrometheusMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const junkRequests = 50
	before := getCounterVecValue(t, otherMethodLabel, "/api/logs", "200")

	for i := 0; i < junkRequests; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
		// Bypass httptest.NewRequest's method validation the same way a raw
		// socket client does: set the field directly.
		req.Method = fmt.Sprintf("ZZQQ%04d", i)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	for method := range collectMethodLabels(t) {
		if strings.HasPrefix(method, "ZZQQ") {
			t.Fatalf("attacker-chosen method %q became a permanent label value", method)
		}
	}

	after := getCounterVecValue(t, otherMethodLabel, "/api/logs", "200")
	if after != before+junkRequests {
		t.Errorf("%q series incremented by %v, want %d", otherMethodLabel, after-before, junkRequests)
	}
}

// TestMetricMethod_KnownMethodsPreserved guards against the fold being so broad
// it destroys the label's usefulness.
func TestMetricMethod_KnownMethodsPreserved(t *testing.T) {
	for _, m := range []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodConnect,
		http.MethodOptions, http.MethodTrace,
	} {
		if got := metricMethod(m); got != m {
			t.Errorf("metricMethod(%q) = %q, want %q", m, got, m)
		}
	}
	for _, m := range []string{"", "get", "X-RANDOM-9f8a7b", "PROPFIND"} {
		if got := metricMethod(m); got != otherMethodLabel {
			t.Errorf("metricMethod(%q) = %q, want %q", m, got, otherMethodLabel)
		}
	}
}

func TestNormalizePath_UUID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/api/logs/550e8400-e29b-41d4-a716-446655440000", "/api/logs/:id"},
		{"/api/connectors/550E8400-E29B-41D4-A716-446655440000", "/api/connectors/:id"},
		{"/api/watches/550e8400-e29b-41d4-a716-446655440000/runs", "/api/watches/:id/runs"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizePath(tt.input)
			if got != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizePath_NumericID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/api/logs/12345", "/api/logs/:id"},
		{"/api/servers/99/metrics", "/api/servers/:id/metrics"},
		{"/api/watches/42/runs/99", "/api/watches/:id/runs/:id"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizePath(tt.input)
			if got != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizePath_Static(t *testing.T) {
	got := normalizePath("/static/css/app.css")
	if got != "/static/*" {
		t.Errorf("normalizePath(/static/css/app.css) = %q, want /static/*", got)
	}
}

func TestNormalizePath_NoIDs(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/api/logs", "/api/logs"},
		{"/healthz", "/healthz"},
		{"/", "/"},
		{"/api/connectors", "/api/connectors"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizePath(tt.input)
			if got != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
