package store

import (
	"context"
	"testing"
	"time"
)

func TestAnalyticsStore_AggregateEndpointStats(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	as := NewAnalyticsStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	entries := []LogEntry{
		{
			Timestamp: now.Add(-40 * time.Minute),
			Level:     "INFO",
			Service:   "api",
			Message:   "request 1",
			RequestSummary: &RequestSummary{
				Controller: "Users",
				Action:     "index",
				Method:     "GET",
				Path:       "/users",
				Status:     200,
				DurationMs: 120,
				SQLCount:   5,
			},
		},
		{
			Timestamp: now.Add(-30 * time.Minute),
			Level:     "ERROR",
			Service:   "api",
			Message:   "request 2",
			RequestSummary: &RequestSummary{
				Controller: "Users",
				Action:     "show",
				Method:     "GET",
				Path:       "/users/1",
				Status:     500,
				DurationMs: 800,
				SQLCount:   15,
			},
		},
		{
			Timestamp: now.Add(-20 * time.Minute),
			Level:     "INFO",
			Service:   "api",
			Message:   "request 3",
			RequestSummary: &RequestSummary{
				Controller: "Users",
				Action:     "index",
				Method:     "GET",
				Path:       "/users",
				Status:     200,
				DurationMs: 95,
				SQLCount:   4,
			},
		},
		{
			Timestamp: now.Add(-15 * time.Minute),
			Level:     "INFO",
			Service:   "api",
			Message:   "request 4",
			RequestSummary: &RequestSummary{
				Controller: "Posts",
				Action:     "create",
				Method:     "POST",
				Path:       "/posts",
				Status:     201,
				DurationMs: 250,
				SQLCount:   8,
			},
		},
	}

	count, err := ls.BatchInsert(ctx, entries)
	if err != nil {
		t.Fatalf("batch insert: %v", err)
	}
	if count != 4 {
		t.Fatalf("expected 4, got %d", count)
	}

	err = as.AggregateEndpointStats(ctx, "1h", now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	// Query top endpoints by request count
	endpoints, err := as.TopEndpoints(ctx, TopEndpointParams{
		Since:  now.Add(-2 * time.Hour),
		Until:  now.Add(time.Hour),
		SortBy: "request_count",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("top endpoints: %v", err)
	}
	if len(endpoints) < 2 {
		t.Fatalf("expected >= 2 endpoints, got %d", len(endpoints))
	}

	// Users#index should have 2 requests
	found := false
	for _, e := range endpoints {
		if e.Controller == "Users" && e.Action == "index" {
			found = true
			if e.RequestCount != 2 {
				t.Errorf("Users#index request_count = %d, want 2", e.RequestCount)
			}
		}
	}
	if !found {
		t.Error("Users#index not found in results")
	}
}

func TestAnalyticsStore_TopEndpoints_SortByErrorRate(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	as := NewAnalyticsStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	entries := []LogEntry{
		{Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api", Message: "ok",
			RequestSummary: &RequestSummary{Controller: "Good", Action: "index", Method: "GET", Status: 200, DurationMs: 50}},
		{Timestamp: now.Add(-29 * time.Minute), Level: "INFO", Service: "api", Message: "ok",
			RequestSummary: &RequestSummary{Controller: "Good", Action: "index", Method: "GET", Status: 200, DurationMs: 60}},
		{Timestamp: now.Add(-28 * time.Minute), Level: "ERROR", Service: "api", Message: "fail",
			RequestSummary: &RequestSummary{Controller: "Bad", Action: "index", Method: "GET", Status: 500, DurationMs: 100}},
		{Timestamp: now.Add(-27 * time.Minute), Level: "ERROR", Service: "api", Message: "fail",
			RequestSummary: &RequestSummary{Controller: "Bad", Action: "index", Method: "GET", Status: 500, DurationMs: 200}},
	}
	ls.BatchInsert(ctx, entries)
	as.AggregateEndpointStats(ctx, "1h", now.Add(-time.Hour))

	endpoints, err := as.TopEndpoints(ctx, TopEndpointParams{
		Since:  now.Add(-2 * time.Hour),
		Until:  now.Add(time.Hour),
		SortBy: "error_rate",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("top endpoints: %v", err)
	}
	if len(endpoints) < 2 {
		t.Fatalf("expected >= 2 endpoints, got %d", len(endpoints))
	}

	// Bad#index should be first (100% error rate)
	if endpoints[0].Controller != "Bad" {
		t.Errorf("expected Bad#index first (highest error rate), got %s#%s", endpoints[0].Controller, endpoints[0].Action)
	}
}

func TestAnalyticsStore_TrafficSummary(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	as := NewAnalyticsStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	entries := []LogEntry{
		{Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api", Message: "ok",
			RequestSummary: &RequestSummary{Controller: "A", Action: "b", Method: "GET", Status: 200, DurationMs: 100}},
		{Timestamp: now.Add(-20 * time.Minute), Level: "INFO", Service: "api", Message: "ok",
			RequestSummary: &RequestSummary{Controller: "A", Action: "b", Method: "POST", Status: 201, DurationMs: 200}},
		{Timestamp: now.Add(-10 * time.Minute), Level: "ERROR", Service: "api", Message: "err",
			RequestSummary: &RequestSummary{Controller: "A", Action: "c", Method: "GET", Status: 500, DurationMs: 500}},
	}
	ls.BatchInsert(ctx, entries)

	summary, err := as.TrafficSummary(ctx, AnalyticsParams{
		Since: now.Add(-1 * time.Hour),
		Until: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("traffic summary: %v", err)
	}
	if summary.TotalRequests != 3 {
		t.Errorf("total_requests = %d, want 3", summary.TotalRequests)
	}
	if summary.ErrorRate < 0.3 || summary.ErrorRate > 0.34 {
		t.Errorf("error_rate = %f, want ~0.33", summary.ErrorRate)
	}
	if summary.StatusBreakdown["2xx"] != 2 {
		t.Errorf("2xx = %d, want 2", summary.StatusBreakdown["2xx"])
	}
	if summary.StatusBreakdown["5xx"] != 1 {
		t.Errorf("5xx = %d, want 1", summary.StatusBreakdown["5xx"])
	}
	if summary.MethodBreakdown["GET"] != 2 {
		t.Errorf("GET = %d, want 2", summary.MethodBreakdown["GET"])
	}
}

func TestAnalyticsStore_TrafficHeatmap(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	as := NewAnalyticsStore(db)
	ctx := context.Background()

	// Use a fixed time in the middle of an hour so both entries land in the same cell
	now := time.Now().UTC().Truncate(time.Hour).Add(30 * time.Minute)
	entries := []LogEntry{
		{Timestamp: now.Add(-10 * time.Minute), Level: "INFO", Service: "api", Message: "req1",
			RequestSummary: &RequestSummary{Controller: "A", Action: "b", Status: 200, DurationMs: 100}},
		{Timestamp: now.Add(-5 * time.Minute), Level: "INFO", Service: "api", Message: "req2",
			RequestSummary: &RequestSummary{Controller: "A", Action: "b", Status: 200, DurationMs: 50}},
	}
	ls.BatchInsert(ctx, entries)

	err := as.UpdateTrafficHeatmap(ctx, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("update heatmap: %v", err)
	}

	cells, err := as.TrafficHeatmap(ctx, "api")
	if err != nil {
		t.Fatalf("query heatmap: %v", err)
	}
	if len(cells) == 0 {
		t.Fatal("expected at least 1 heatmap cell")
	}

	// Verify the cell matches current hour
	found := false
	for _, c := range cells {
		if c.RequestCount >= 2 {
			found = true
		}
	}
	if !found {
		t.Error("expected a cell with >= 2 requests")
	}
}

func TestAnalyticsStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	as := NewAnalyticsStore(db)
	ctx := context.Background()

	old := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Hour)
	entries := []LogEntry{
		{Timestamp: old, Level: "INFO", Service: "api", Message: "old",
			RequestSummary: &RequestSummary{Controller: "A", Action: "b", Status: 200, DurationMs: 100}},
	}
	ls.BatchInsert(ctx, entries)
	as.AggregateEndpointStats(ctx, "1h", old)

	pruned, err := as.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned == 0 {
		t.Error("expected some pruned rows")
	}
}

func TestAnalyticsStore_TopEndpoints_ServiceFilter(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	as := NewAnalyticsStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	entries := []LogEntry{
		{Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api", Message: "api",
			RequestSummary: &RequestSummary{Controller: "A", Action: "b", Method: "GET", Status: 200, DurationMs: 100}},
		{Timestamp: now.Add(-29 * time.Minute), Level: "INFO", Service: "worker", Message: "worker",
			RequestSummary: &RequestSummary{Controller: "W", Action: "x", Method: "GET", Status: 200, DurationMs: 50}},
	}
	ls.BatchInsert(ctx, entries)
	as.AggregateEndpointStats(ctx, "1h", now.Add(-time.Hour))

	endpoints, err := as.TopEndpoints(ctx, TopEndpointParams{
		Service: "api",
		Since:   now.Add(-2 * time.Hour),
		Until:   now.Add(time.Hour),
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("top endpoints: %v", err)
	}
	for _, e := range endpoints {
		if e.Service != "api" {
			t.Errorf("expected service=api, got %q", e.Service)
		}
	}
}
