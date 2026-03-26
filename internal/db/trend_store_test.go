package db

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestTrendStore_AggregateBuckets(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	ts := NewTrendStore(db)
	ctx := context.Background()

	// Insert test log entries with request summaries
	now := time.Now().UTC().Truncate(time.Hour)
	entries := []store.LogEntry{
		{
			Timestamp:  now.Add(-30 * time.Minute),
			Level:      "INFO",
			Service:    "api",
			CommitHash: "abc123",
			Message:    "request completed",
			RequestSummary: &store.RequestSummary{
				Controller: "Users",
				Action:     "index",
				Method:     "GET",
				Path:       "/users",
				Status:     200,
				DurationMs: 120,
				SQLCount:   5,
				DBTimeMs:   20,
			},
		},
		{
			Timestamp:  now.Add(-20 * time.Minute),
			Level:      "ERROR",
			Service:    "api",
			CommitHash: "abc123",
			Message:    "request failed",
			RequestSummary: &store.RequestSummary{
				Controller: "Users",
				Action:     "show",
				Method:     "GET",
				Path:       "/users/1",
				Status:     500,
				DurationMs: 450,
				SQLCount:   12,
				DBTimeMs:   80,
			},
		},
		{
			Timestamp:  now.Add(-10 * time.Minute),
			Level:      "INFO",
			Service:    "api",
			CommitHash: "def456",
			Message:    "request completed",
			RequestSummary: &store.RequestSummary{
				Controller: "Users",
				Action:     "index",
				Method:     "GET",
				Path:       "/users",
				Status:     200,
				DurationMs: 85,
				SQLCount:   3,
				DBTimeMs:   10,
			},
		},
	}

	count, err := ls.BatchInsert(ctx, entries)
	if err != nil {
		t.Fatalf("batch insert: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3, got %d", count)
	}

	// Aggregate
	err = ts.AggregateBuckets(ctx, "1h", now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	// Query trends
	buckets, err := ts.QueryTrends(ctx, store.TrendQueryParams{
		Interval: "1h",
		Since:    now.Add(-2 * time.Hour),
		Until:    now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("query trends: %v", err)
	}
	if len(buckets) == 0 {
		t.Fatal("expected at least 1 bucket")
	}

	// Verify the bucket has our data
	bucket := buckets[0]
	if bucket.Service != "api" {
		t.Errorf("service = %q, want api", bucket.Service)
	}
	if bucket.RequestCount < 2 {
		t.Errorf("request_count = %d, want >= 2", bucket.RequestCount)
	}
	if bucket.ErrorCount < 1 {
		t.Errorf("error_count = %d, want >= 1", bucket.ErrorCount)
	}
	if bucket.LogCount < 2 {
		t.Errorf("log_count = %d, want >= 2", bucket.LogCount)
	}
}

func TestTrendStore_AggregateBuckets_MultipleBuckets(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	ts := NewTrendStore(db)
	ctx := context.Background()

	// Insert data spanning 2 hours
	baseTime := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)

	entries := []store.LogEntry{
		{
			Timestamp: baseTime.Add(10 * time.Minute),
			Level:     "INFO",
			Service:   "web",
			Message:   "request 1",
			RequestSummary: &store.RequestSummary{
				Controller: "Home",
				Action:     "index",
				Status:     200,
				DurationMs: 100,
			},
		},
		{
			Timestamp: baseTime.Add(70 * time.Minute), // second hour
			Level:     "INFO",
			Service:   "web",
			Message:   "request 2",
			RequestSummary: &store.RequestSummary{
				Controller: "Home",
				Action:     "index",
				Status:     200,
				DurationMs: 200,
			},
		},
	}

	ls.BatchInsert(ctx, entries)
	err := ts.AggregateBuckets(ctx, "1h", baseTime)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	buckets, err := ts.QueryTrends(ctx, store.TrendQueryParams{
		Interval: "1h",
		Since:    baseTime.Add(-time.Hour),
		Until:    time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(buckets) < 2 {
		t.Fatalf("expected >= 2 buckets, got %d", len(buckets))
	}
}

func TestTrendStore_DeployMarkers(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	ts := NewTrendStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	entries := []store.LogEntry{
		{
			Timestamp:  now.Add(-30 * time.Minute),
			Level:      "INFO",
			Service:    "api",
			CommitHash: "abc123",
			Message:    "deployed",
		},
		{
			Timestamp:  now.Add(-10 * time.Minute),
			Level:      "INFO",
			Service:    "api",
			CommitHash: "def456",
			Message:    "deployed again",
		},
	}
	ls.BatchInsert(ctx, entries)

	err := ts.AggregateBuckets(ctx, "1h", now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	markers, err := ts.ListDeployMarkers(ctx, "api", now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("list deploy markers: %v", err)
	}
	if len(markers) != 2 {
		t.Fatalf("expected 2 deploy markers, got %d", len(markers))
	}

	hashes := map[string]bool{}
	for _, m := range markers {
		hashes[m.CommitHash] = true
	}
	if !hashes["abc123"] || !hashes["def456"] {
		t.Error("expected both commit hashes to be present")
	}
}

func TestTrendStore_QueryTrends_ServiceFilter(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	ts := NewTrendStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	entries := []store.LogEntry{
		{Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api", Message: "api req",
			RequestSummary: &store.RequestSummary{Controller: "A", Action: "b", Status: 200, DurationMs: 100}},
		{Timestamp: now.Add(-20 * time.Minute), Level: "INFO", Service: "worker", Message: "worker req",
			RequestSummary: &store.RequestSummary{Controller: "W", Action: "x", Status: 200, DurationMs: 50}},
	}
	ls.BatchInsert(ctx, entries)
	ts.AggregateBuckets(ctx, "1h", now.Add(-1*time.Hour))

	// Filter by service=api
	buckets, err := ts.QueryTrends(ctx, store.TrendQueryParams{
		Interval: "1h",
		Service:  "api",
		Since:    now.Add(-2 * time.Hour),
		Until:    now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for _, b := range buckets {
		if b.Service != "api" {
			t.Errorf("expected service=api, got %q", b.Service)
		}
	}
}

func TestTrendStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	ts := NewTrendStore(db)
	ctx := context.Background()

	old := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
	entries := []store.LogEntry{
		{Timestamp: old, Level: "INFO", Service: "api", Message: "old",
			RequestSummary: &store.RequestSummary{Controller: "A", Action: "b", Status: 200, DurationMs: 100}},
	}
	ls.BatchInsert(ctx, entries)
	ts.AggregateBuckets(ctx, "1h", old)

	pruned, err := ts.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned == 0 {
		t.Error("expected some pruned rows")
	}
}

func TestTrendStore_Percentiles(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	ts := NewTrendStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)

	// Insert enough requests to make percentiles meaningful
	var entries []store.LogEntry
	durations := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 500, 1000}
	for i, dur := range durations {
		entries = append(entries, store.LogEntry{
			Timestamp: now.Add(-time.Duration(30-i) * time.Minute),
			Level:     "INFO",
			Service:   "api",
			Message:   "request",
			RequestSummary: &store.RequestSummary{
				Controller: "Test",
				Action:     "index",
				Status:     200,
				DurationMs: dur,
			},
		})
	}
	ls.BatchInsert(ctx, entries)
	ts.AggregateBuckets(ctx, "1h", now.Add(-time.Hour))

	buckets, _ := ts.QueryTrends(ctx, store.TrendQueryParams{
		Interval: "1h",
		Service:  "api",
		Since:    now.Add(-2 * time.Hour),
		Until:    now.Add(time.Hour),
	})

	if len(buckets) == 0 {
		t.Fatal("expected buckets")
	}
	b := buckets[0]
	if b.P95DurationMs <= b.P50DurationMs {
		t.Errorf("p95 (%.1f) should be > p50 (%.1f)", b.P95DurationMs, b.P50DurationMs)
	}
	if b.P99DurationMs < b.P95DurationMs {
		t.Errorf("p99 (%.1f) should be >= p95 (%.1f)", b.P99DurationMs, b.P95DurationMs)
	}
}
