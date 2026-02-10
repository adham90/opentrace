package store

import (
	"context"
	"testing"
	"time"
)

func TestMetricStore_BatchInsertAndQuery(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ms := NewMetricStore(db)
	ctx := context.Background()

	srv, err := ss.Register(ctx, RegisterServerParams{Hostname: "metric-test"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	ts := time.Now().UTC()
	samples := []MetricSample{
		{Name: "cpu.usage_percent", Value: 42.5, Unit: "percent"},
		{Name: "memory.used_bytes", Value: 1073741824, Unit: "bytes"},
		{Name: "disk.usage_percent", Value: 65.3, Unit: "percent", Labels: map[string]string{"mount": "/"}},
	}

	n, err := ms.BatchInsert(ctx, srv.ID, ts, samples)
	if err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}
	if n != 3 {
		t.Errorf("inserted = %d, want 3", n)
	}

	// Query all metrics for this server
	points, err := ms.Query(ctx, MetricQuery{ServerID: srv.ID})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(points) != 3 {
		t.Errorf("query results = %d, want 3", len(points))
	}

	// Query by metric name
	cpuPoints, err := ms.Query(ctx, MetricQuery{
		ServerID:   srv.ID,
		MetricName: "cpu.usage_percent",
	})
	if err != nil {
		t.Fatalf("Query by name: %v", err)
	}
	if len(cpuPoints) != 1 {
		t.Errorf("cpu query results = %d, want 1", len(cpuPoints))
	}
	if cpuPoints[0].MetricValue != 42.5 {
		t.Errorf("cpu value = %f, want 42.5", cpuPoints[0].MetricValue)
	}
}

func TestMetricStore_QueryTimeRange(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ms := NewMetricStore(db)
	ctx := context.Background()

	srv, _ := ss.Register(ctx, RegisterServerParams{Hostname: "time-range-test"})

	// Insert metrics at two different times
	t1 := time.Now().UTC().Add(-2 * time.Hour)
	t2 := time.Now().UTC()

	ms.BatchInsert(ctx, srv.ID, t1, []MetricSample{
		{Name: "cpu.usage_percent", Value: 30.0},
	})
	ms.BatchInsert(ctx, srv.ID, t2, []MetricSample{
		{Name: "cpu.usage_percent", Value: 80.0},
	})

	// Query only recent metrics
	oneHourAgo := time.Now().UTC().Add(-1 * time.Hour)
	points, err := ms.Query(ctx, MetricQuery{
		ServerID: srv.ID,
		Start:    &oneHourAgo,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(points) != 1 {
		t.Errorf("time-range query results = %d, want 1", len(points))
	}
	if len(points) > 0 && points[0].MetricValue != 80.0 {
		t.Errorf("value = %f, want 80.0", points[0].MetricValue)
	}
}

func TestMetricStore_LatestByServer(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ms := NewMetricStore(db)
	ctx := context.Background()

	srv, _ := ss.Register(ctx, RegisterServerParams{Hostname: "latest-test"})

	// Insert two batches at different times
	t1 := time.Now().UTC().Add(-1 * time.Hour)
	t2 := time.Now().UTC()

	ms.BatchInsert(ctx, srv.ID, t1, []MetricSample{
		{Name: "cpu.usage_percent", Value: 30.0},
		{Name: "memory.usage_percent", Value: 50.0},
	})
	ms.BatchInsert(ctx, srv.ID, t2, []MetricSample{
		{Name: "cpu.usage_percent", Value: 80.0},
		{Name: "memory.usage_percent", Value: 70.0},
	})

	latest, err := ms.LatestByServer(ctx, srv.ID)
	if err != nil {
		t.Fatalf("LatestByServer: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("latest results = %d, want 2", len(latest))
	}

	// Should return the newest values
	for _, p := range latest {
		switch p.MetricName {
		case "cpu.usage_percent":
			if p.MetricValue != 80.0 {
				t.Errorf("cpu latest = %f, want 80.0", p.MetricValue)
			}
		case "memory.usage_percent":
			if p.MetricValue != 70.0 {
				t.Errorf("memory latest = %f, want 70.0", p.MetricValue)
			}
		}
	}
}

func TestMetricStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ms := NewMetricStore(db)
	ctx := context.Background()

	srv, _ := ss.Register(ctx, RegisterServerParams{Hostname: "prune-test"})

	ts := time.Now().UTC()
	ms.BatchInsert(ctx, srv.ID, ts, []MetricSample{
		{Name: "cpu.usage_percent", Value: 50.0},
	})

	// Backdate the created_at to 10 days ago
	tenDaysAgo := time.Now().UTC().Add(-10 * 24 * time.Hour).Format(time.RFC3339)
	db.ExecContext(ctx, `UPDATE metrics SET created_at = ?`, tenDaysAgo)

	// Prune metrics older than 7 days
	pruned, err := ms.Prune(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want 1", pruned)
	}

	// Verify no metrics remain
	points, _ := ms.Query(ctx, MetricQuery{ServerID: srv.ID})
	if len(points) != 0 {
		t.Errorf("remaining = %d, want 0", len(points))
	}
}

func TestMetricStore_BatchInsertEmpty(t *testing.T) {
	db := setupTestDB(t)
	ms := NewMetricStore(db)
	ctx := context.Background()

	n, err := ms.BatchInsert(ctx, [16]byte{}, time.Now(), nil)
	if err != nil {
		t.Fatalf("BatchInsert empty: %v", err)
	}
	if n != 0 {
		t.Errorf("inserted = %d, want 0", n)
	}
}

func TestMetricStore_Labels(t *testing.T) {
	db := setupTestDB(t)
	ss := NewServerStore(db)
	ms := NewMetricStore(db)
	ctx := context.Background()

	srv, _ := ss.Register(ctx, RegisterServerParams{Hostname: "label-test"})

	ts := time.Now().UTC()
	ms.BatchInsert(ctx, srv.ID, ts, []MetricSample{
		{Name: "disk.usage_percent", Value: 80.0, Labels: map[string]string{"mount": "/data"}},
	})

	points, _ := ms.Query(ctx, MetricQuery{ServerID: srv.ID})
	if len(points) != 1 {
		t.Fatalf("results = %d, want 1", len(points))
	}
	if points[0].Labels["mount"] != "/data" {
		t.Errorf("labels[mount] = %q, want %q", points[0].Labels["mount"], "/data")
	}
}
