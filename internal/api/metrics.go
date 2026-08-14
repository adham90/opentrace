package api

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/metrics"
)

// uuidPattern matches UUID-like path segments (8-4-4-4-12 hex with dashes).
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// numericPattern matches purely numeric path segments.
var numericPattern = regexp.MustCompile(`^[0-9]+$`)

// normalizePath replaces UUID and numeric ID segments with ":id" to avoid
// high-cardinality Prometheus labels. It preserves the first two segments
// for grouping (e.g., "/api/logs/:id").
func normalizePath(path string) string {
	// Skip static assets and well-known paths entirely
	if strings.HasPrefix(path, "/static/") {
		return "/static/*"
	}

	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, seg := range segments {
		if uuidPattern.MatchString(seg) || numericPattern.MatchString(seg) {
			segments[i] = ":id"
		}
	}
	return "/" + strings.Join(segments, "/")
}

// unmatchedPathLabel is the single label value used for requests that matched
// no route. Every 404 from a scanner would otherwise mint a permanent
// Prometheus time series keyed on an attacker-chosen path.
const unmatchedPathLabel = "unmatched"

// metricPath returns the bounded label value for a request. The chi route
// pattern ("/api/cli/logs/{id}") is used when available: it comes from the
// route table, so the set of values is bounded by the number of routes. Only
// requests served outside chi fall back to normalizePath.
func metricPath(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return normalizePath(r.URL.Path)
	}
	if pattern := rctx.RoutePattern(); pattern != "" {
		return pattern
	}
	return unmatchedPathLabel
}

// otherMethodLabel is the single label value used for any request method
// outside the known set.
const otherMethodLabel = "other"

// knownMethods bounds the Prometheus method label. net/http accepts any RFC 7230
// token as a method and hands it to the handler, so r.Method is raw client input
// exactly like the path was: without folding, `curl -X <random>` mints a
// permanent time series per request, and PrometheusMiddleware runs ahead of all
// authentication, so an unauthenticated scanner can do it.
var knownMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodPatch:   {},
	http.MethodDelete:  {},
	http.MethodConnect: {},
	http.MethodOptions: {},
	http.MethodTrace:   {},
}

// metricMethod returns the bounded label value for a request method.
func metricMethod(method string) string {
	if _, ok := knownMethods[method]; ok {
		return method
	}
	return otherMethodLabel
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
// It implements http.Flusher so SSE streaming works through this middleware.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code and delegates to the underlying writer.
func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush delegates to the underlying ResponseWriter if it supports flushing.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter for middleware compatibility.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// PrometheusMiddleware records HTTP request counts and duration as Prometheus metrics.
func PrometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip metrics for the /metrics endpoint itself to avoid self-referential loops
		if r.URL.Path == "/debug/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rec, r)

		duration := time.Since(start).Seconds()
		path := metricPath(r)
		method := metricMethod(r.Method)
		status := strconv.Itoa(rec.statusCode)

		metrics.HTTPRequestsTotal.WithLabelValues(method, path, status).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(method, path).Observe(duration)
	})
}
