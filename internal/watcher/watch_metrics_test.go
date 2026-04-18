package watcher

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// insertTestRequestSummaries inserts log entries with attached request summaries.
func insertTestRequestSummaries(t *testing.T, logStore store.LogStore, service string, summaries []store.RequestSummary) {
	t.Helper()
	ctx := context.Background()
	entries := make([]store.LogEntry, len(summaries))
	for i, rs := range summaries {
		rsCopy := rs
		entries[i] = store.LogEntry{
			Timestamp:      time.Now().UTC().Add(-time.Duration(i) * time.Second),
			Level:          "info",
			Service:        service,
			Message:        "request",
			EventType:      "request",
			RequestSummary: &rsCopy,
		}
	}
	_, err := logStore.BatchInsert(ctx, entries)
	if err != nil {
		t.Fatalf("inserting test request summaries: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 1. Constructor
// ---------------------------------------------------------------------------

func TestNewWatchMetrics(t *testing.T) {
	_, logStore := setupWatchTestDB(t)
	m := NewWatchMetrics(logStore)
	if m == nil {
		t.Fatal("NewWatchMetrics returned nil")
	}
	if m.logStore == nil {
		t.Fatal("logStore field is nil")
	}
}

// ---------------------------------------------------------------------------
// 2. ErrorRate
// ---------------------------------------------------------------------------

func TestMeasure_ErrorRate(t *testing.T) {
	t.Run("no logs returns 0", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		val, err := m.Measure(ctx, store.WatchMetricErrorRate, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 0 {
			t.Errorf("got %v, want 0", val)
		}
	})

	t.Run("all info logs returns 0", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		insertTestLogs(t, logStore, "web", 10, "info")

		val, err := m.Measure(ctx, store.WatchMetricErrorRate, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 0 {
			t.Errorf("got %v, want 0", val)
		}
	})

	t.Run("mixed 8 info 2 error returns 0.2", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		insertTestLogs(t, logStore, "web", 8, "info")
		insertTestLogs(t, logStore, "web", 2, "error")

		val, err := m.Measure(ctx, store.WatchMetricErrorRate, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 0.2 {
			t.Errorf("got %v, want 0.2", val)
		}
	})

	t.Run("all errors returns 1.0", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		insertTestLogs(t, logStore, "web", 5, "error")

		val, err := m.Measure(ctx, store.WatchMetricErrorRate, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 1.0 {
			t.Errorf("got %v, want 1.0", val)
		}
	})
}

// ---------------------------------------------------------------------------
// 3. LogCount
// ---------------------------------------------------------------------------

func TestMeasure_LogCount(t *testing.T) {
	t.Run("no logs returns 0", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		val, err := m.Measure(ctx, store.WatchMetricLogCount, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 0 {
			t.Errorf("got %v, want 0", val)
		}
	})

	t.Run("10 logs returns 10", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		insertTestLogs(t, logStore, "web", 10, "info")

		val, err := m.Measure(ctx, store.WatchMetricLogCount, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 10 {
			t.Errorf("got %v, want 10", val)
		}
	})
}

// ---------------------------------------------------------------------------
// 4. ErrorCount
// ---------------------------------------------------------------------------

func TestMeasure_ErrorCount(t *testing.T) {
	t.Run("no error logs returns 0", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		insertTestLogs(t, logStore, "web", 10, "info")

		val, err := m.Measure(ctx, store.WatchMetricErrorCount, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 0 {
			t.Errorf("got %v, want 0", val)
		}
	})

	t.Run("5 error logs returns 5", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		insertTestLogs(t, logStore, "web", 5, "error")

		val, err := m.Measure(ctx, store.WatchMetricErrorCount, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 5 {
			t.Errorf("got %v, want 5", val)
		}
	})
}

// ---------------------------------------------------------------------------
// 5. ResponseTime (average)
// ---------------------------------------------------------------------------

func TestMeasure_ResponseTime(t *testing.T) {
	t.Run("no summaries returns 0", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		val, err := m.Measure(ctx, store.WatchMetricResponseTime, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 0 {
			t.Errorf("got %v, want 0", val)
		}
	})

	t.Run("summaries with known durations returns correct average", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		// Insert summaries: 100, 200, 300 → avg = 200
		insertTestRequestSummaries(t, logStore, "web", []store.RequestSummary{
			{Path: "/api/a", DurationMs: 100.0},
			{Path: "/api/b", DurationMs: 200.0},
			{Path: "/api/c", DurationMs: 300.0},
		})

		val, err := m.Measure(ctx, store.WatchMetricResponseTime, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 200.0 {
			t.Errorf("got %v, want 200.0", val)
		}
	})
}

// ---------------------------------------------------------------------------
// 6. P95 Response Time
// ---------------------------------------------------------------------------

func TestMeasure_P95Response(t *testing.T) {
	t.Run("no summaries returns 0", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		val, err := m.Measure(ctx, store.WatchMetricP95Response, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 0 {
			t.Errorf("got %v, want 0", val)
		}
	})

	t.Run("20 summaries with durations 1-20 returns p95 around 19", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		// Insert 20 summaries with durations 1.0, 2.0, ..., 20.0
		summaries := make([]store.RequestSummary, 20)
		for i := 0; i < 20; i++ {
			summaries[i] = store.RequestSummary{
				Path:       "/api/test",
				DurationMs: float64(i + 1),
			}
		}
		insertTestRequestSummaries(t, logStore, "web", summaries)

		val, err := m.Measure(ctx, store.WatchMetricP95Response, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}

		// p95 of [1..20]: ceil(20*0.95)-1 = ceil(19)-1 = 19-1 = 18 → durations[18] = 19.0
		if val != 19.0 {
			t.Errorf("got %v, want 19.0", val)
		}
	})
}

// ---------------------------------------------------------------------------
// 7. Heartbeat
// ---------------------------------------------------------------------------

func TestMeasure_Heartbeat(t *testing.T) {
	t.Run("no logs in window returns window seconds", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		window := time.Hour
		val, err := m.Measure(ctx, store.WatchMetricHeartbeat, "web", "", "", window)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != window.Seconds() {
			t.Errorf("got %v, want %v", val, window.Seconds())
		}
	})

	t.Run("recent log returns small value", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		// Insert a log just now
		insertTestLogs(t, logStore, "web", 1, "info")

		val, err := m.Measure(ctx, store.WatchMetricHeartbeat, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		// The log was inserted at time.Now() minus 0 seconds, so the value should be very small.
		// Allow up to 5 seconds for test execution overhead.
		if val > 5 {
			t.Errorf("got %v, want < 5 (seconds since recent log)", val)
		}
	})
}

// ---------------------------------------------------------------------------
// 8. SQL Count (average per request)
// ---------------------------------------------------------------------------

func TestMeasure_SQLCount(t *testing.T) {
	t.Run("no summaries returns 0", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		val, err := m.Measure(ctx, store.WatchMetricSQLCount, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 0 {
			t.Errorf("got %v, want 0", val)
		}
	})

	t.Run("summaries with known SQL counts returns correct average", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		// Insert summaries: SQL counts 4, 8, 12 → avg = 8
		insertTestRequestSummaries(t, logStore, "web", []store.RequestSummary{
			{Path: "/api/a", DurationMs: 100.0, SQLCount: 4},
			{Path: "/api/b", DurationMs: 100.0, SQLCount: 8},
			{Path: "/api/c", DurationMs: 100.0, SQLCount: 12},
		})

		val, err := m.Measure(ctx, store.WatchMetricSQLCount, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 8.0 {
			t.Errorf("got %v, want 8.0", val)
		}
	})
}

// ---------------------------------------------------------------------------
// 9. Cache Hit Rate
// ---------------------------------------------------------------------------

func TestMeasure_CacheHitRate(t *testing.T) {
	t.Run("no summaries returns 0", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		val, err := m.Measure(ctx, store.WatchMetricCacheHitRate, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 0 {
			t.Errorf("got %v, want 0", val)
		}
	})

	t.Run("summaries with cache reads and hits returns correct ratio", func(t *testing.T) {
		t.Skip("cache metrics not yet in segmented log store flat schema — needs column promotion")
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		// Summary 1: 10 reads, 8 hits. Summary 2: 10 reads, 6 hits.
		// Total: 20 reads, 14 hits → ratio = 0.7
		insertTestRequestSummaries(t, logStore, "web", []store.RequestSummary{
			{Path: "/api/a", DurationMs: 100.0, CacheReads: 10, CacheHits: 8},
			{Path: "/api/b", DurationMs: 100.0, CacheReads: 10, CacheHits: 6},
		})

		val, err := m.Measure(ctx, store.WatchMetricCacheHitRate, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 0.7 {
			t.Errorf("got %v, want 0.7", val)
		}
	})

	t.Run("zero cache reads returns 0", func(t *testing.T) {
		_, logStore := setupWatchTestDB(t)
		m := NewWatchMetrics(logStore)
		ctx := context.Background()

		// Summaries with no cache reads at all
		insertTestRequestSummaries(t, logStore, "web", []store.RequestSummary{
			{Path: "/api/a", DurationMs: 100.0, CacheReads: 0, CacheHits: 0},
			{Path: "/api/b", DurationMs: 100.0, CacheReads: 0, CacheHits: 0},
		})

		val, err := m.Measure(ctx, store.WatchMetricCacheHitRate, "web", "", "", time.Hour)
		if err != nil {
			t.Fatalf("Measure: %v", err)
		}
		if val != 0 {
			t.Errorf("got %v, want 0", val)
		}
	})
}

// ---------------------------------------------------------------------------
// 10. Unknown Metric
// ---------------------------------------------------------------------------

func TestMeasure_UnknownMetric(t *testing.T) {
	_, logStore := setupWatchTestDB(t)
	m := NewWatchMetrics(logStore)
	ctx := context.Background()

	_, err := m.Measure(ctx, store.WatchMetric("nonexistent_metric"), "web", "", "", time.Hour)
	if err == nil {
		t.Fatal("expected error for unknown metric, got nil")
	}
}

// ---------------------------------------------------------------------------
// 11. CaptureBaseline
// ---------------------------------------------------------------------------

func TestCaptureBaseline_WithMixedLogs(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()
	metrics := NewWatchMetrics(logStore)

	// Insert 80 info + 20 error = 100 logs
	insertTestLogs(t, logStore, "api", 80, "info")
	insertTestLogs(t, logStore, "api", 20, "error")

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.3, "api"),
		Service:        "api",
	})
	if err != nil {
		t.Fatalf("Create watch: %v", err)
	}

	baseline, err := CaptureBaseline(ctx, logStore, metrics, w)
	if err != nil {
		t.Fatalf("CaptureBaseline: %v", err)
	}

	// Error rate = 20 / 100 = 0.2
	if baseline.ErrorRate != 0.2 {
		t.Errorf("error_rate = %v, want 0.2", baseline.ErrorRate)
	}
	if baseline.LogCount != 100 {
		t.Errorf("log_count = %d, want 100", baseline.LogCount)
	}
	if baseline.ErrorCount != 20 {
		t.Errorf("error_count = %d, want 20", baseline.ErrorCount)
	}
	if baseline.WindowDuration != "1h" {
		t.Errorf("window_duration = %q, want 1h", baseline.WindowDuration)
	}
	if baseline.CapturedAt.IsZero() {
		t.Error("captured_at should not be zero")
	}
}

func TestCaptureBaseline_WithRequestSummaries(t *testing.T) {
	t.Skip("cache metrics not yet in segmented log store flat schema — needs column promotion")
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()
	metrics := NewWatchMetrics(logStore)

	// Insert request summaries across two endpoints
	insertTestRequestSummaries(t, logStore, "api", []store.RequestSummary{
		{Path: "/api/users", DurationMs: 100.0, SQLCount: 4, CacheReads: 10, CacheHits: 8},
		{Path: "/api/users", DurationMs: 200.0, SQLCount: 6, CacheReads: 10, CacheHits: 7},
		{Path: "/api/orders", DurationMs: 300.0, SQLCount: 10, CacheReads: 20, CacheHits: 15},
	})

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("response_time", "gt", 500, "api"),
		Service:        "api",
	})
	if err != nil {
		t.Fatalf("Create watch: %v", err)
	}

	baseline, err := CaptureBaseline(ctx, logStore, metrics, w)
	if err != nil {
		t.Fatalf("CaptureBaseline: %v", err)
	}

	// Avg response: (100 + 200 + 300) / 3 = 200
	if math.Abs(baseline.AvgResponseMs-200.0) > 0.01 {
		t.Errorf("avg_response_ms = %v, want 200.0", baseline.AvgResponseMs)
	}

	// Avg SQL count: (4 + 6 + 10) / 3 ≈ 6.667
	expectedSQL := 20.0 / 3.0
	if math.Abs(baseline.SQLCount-expectedSQL) > 0.01 {
		t.Errorf("sql_count = %v, want ~%v", baseline.SQLCount, expectedSQL)
	}

	// Cache hit rate: total hits 30, total reads 40 → 0.75
	if math.Abs(baseline.CacheHitRate-0.75) > 0.01 {
		t.Errorf("cache_hit_rate = %v, want 0.75", baseline.CacheHitRate)
	}

	// P95 should be set (with 3 entries it should be the highest: 300)
	if baseline.P95ResponseMs == 0 {
		t.Error("p95_response_ms should not be 0 when request summaries exist")
	}

	// Endpoints should be captured
	if len(baseline.Endpoints) == 0 {
		t.Fatal("expected endpoints in baseline")
	}

	// Verify per-endpoint data
	endpointMap := make(map[string]store.WatchEndpointBaseline)
	for _, ep := range baseline.Endpoints {
		endpointMap[ep.Path] = ep
	}

	usersEP, ok := endpointMap["/api/users"]
	if !ok {
		t.Fatal("expected /api/users endpoint in baseline")
	}
	// /api/users: avg duration (100+200)/2 = 150
	if math.Abs(usersEP.AvgDurationMs-150.0) > 0.01 {
		t.Errorf("/api/users avg_duration_ms = %v, want 150.0", usersEP.AvgDurationMs)
	}
	// /api/users: avg SQL (4+6)/2 = 5
	if math.Abs(usersEP.AvgSQLCount-5.0) > 0.01 {
		t.Errorf("/api/users avg_sql_count = %v, want 5.0", usersEP.AvgSQLCount)
	}
	if usersEP.RequestCount != 2 {
		t.Errorf("/api/users request_count = %d, want 2", usersEP.RequestCount)
	}

	ordersEP, ok := endpointMap["/api/orders"]
	if !ok {
		t.Fatal("expected /api/orders endpoint in baseline")
	}
	// /api/orders: only 1 entry with duration 300
	if math.Abs(ordersEP.AvgDurationMs-300.0) > 0.01 {
		t.Errorf("/api/orders avg_duration_ms = %v, want 300.0", ordersEP.AvgDurationMs)
	}
	if ordersEP.RequestCount != 1 {
		t.Errorf("/api/orders request_count = %d, want 1", ordersEP.RequestCount)
	}
}

// ---------------------------------------------------------------------------
// Additional edge case tests
// ---------------------------------------------------------------------------

func TestMeasure_ErrorRate_FatalCountsAsError(t *testing.T) {
	_, logStore := setupWatchTestDB(t)
	m := NewWatchMetrics(logStore)
	ctx := context.Background()

	// 5 info, 5 fatal → error rate should be 0.5
	insertTestLogs(t, logStore, "web", 5, "info")
	insertTestLogs(t, logStore, "web", 5, "fatal")

	val, err := m.Measure(ctx, store.WatchMetricErrorRate, "web", "", "", time.Hour)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if val != 0.5 {
		t.Errorf("got %v, want 0.5 (fatal logs count as errors)", val)
	}
}

func TestMeasure_ServiceFilter(t *testing.T) {
	_, logStore := setupWatchTestDB(t)
	m := NewWatchMetrics(logStore)
	ctx := context.Background()

	// Insert logs for different services
	insertTestLogs(t, logStore, "web", 10, "info")
	insertTestLogs(t, logStore, "api", 5, "error")

	// Count for "web" should be 10 (only info logs)
	val, err := m.Measure(ctx, store.WatchMetricLogCount, "web", "", "", time.Hour)
	if err != nil {
		t.Fatalf("Measure web: %v", err)
	}
	if val != 10 {
		t.Errorf("web log_count = %v, want 10", val)
	}

	// Error count for "web" should be 0
	val, err = m.Measure(ctx, store.WatchMetricErrorCount, "web", "", "", time.Hour)
	if err != nil {
		t.Fatalf("Measure web errors: %v", err)
	}
	if val != 0 {
		t.Errorf("web error_count = %v, want 0", val)
	}

	// Error count for "api" should be 5
	val, err = m.Measure(ctx, store.WatchMetricErrorCount, "api", "", "", time.Hour)
	if err != nil {
		t.Fatalf("Measure api errors: %v", err)
	}
	if val != 5 {
		t.Errorf("api error_count = %v, want 5", val)
	}
}

func TestMeasure_ResponseTime_ServiceFilter(t *testing.T) {
	_, logStore := setupWatchTestDB(t)
	m := NewWatchMetrics(logStore)
	ctx := context.Background()

	// Insert summaries for two different services
	insertTestRequestSummaries(t, logStore, "web", []store.RequestSummary{
		{Path: "/web/page", DurationMs: 100.0},
	})
	insertTestRequestSummaries(t, logStore, "api", []store.RequestSummary{
		{Path: "/api/data", DurationMs: 500.0},
	})

	// Avg response for "web" should be 100
	val, err := m.Measure(ctx, store.WatchMetricResponseTime, "web", "", "", time.Hour)
	if err != nil {
		t.Fatalf("Measure web: %v", err)
	}
	if val != 100.0 {
		t.Errorf("web response_time = %v, want 100.0", val)
	}

	// Avg response for "api" should be 500
	val, err = m.Measure(ctx, store.WatchMetricResponseTime, "api", "", "", time.Hour)
	if err != nil {
		t.Fatalf("Measure api: %v", err)
	}
	if val != 500.0 {
		t.Errorf("api response_time = %v, want 500.0", val)
	}
}

func TestCaptureBaseline_EmptyDB(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()
	metrics := NewWatchMetrics(logStore)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.5, "empty-service"),
		Service:        "empty-service",
	})
	if err != nil {
		t.Fatalf("Create watch: %v", err)
	}

	baseline, err := CaptureBaseline(ctx, logStore, metrics, w)
	if err != nil {
		t.Fatalf("CaptureBaseline: %v", err)
	}

	if baseline.ErrorRate != 0 {
		t.Errorf("error_rate = %v, want 0", baseline.ErrorRate)
	}
	if baseline.LogCount != 0 {
		t.Errorf("log_count = %d, want 0", baseline.LogCount)
	}
	if baseline.ErrorCount != 0 {
		t.Errorf("error_count = %d, want 0", baseline.ErrorCount)
	}
	if baseline.AvgResponseMs != 0 {
		t.Errorf("avg_response_ms = %v, want 0", baseline.AvgResponseMs)
	}
	if baseline.SQLCount != 0 {
		t.Errorf("sql_count = %v, want 0", baseline.SQLCount)
	}
	if baseline.CacheHitRate != 0 {
		t.Errorf("cache_hit_rate = %v, want 0", baseline.CacheHitRate)
	}
	if len(baseline.Endpoints) != 0 {
		t.Errorf("endpoints = %v, want empty", baseline.Endpoints)
	}
}
