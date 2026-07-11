package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/engine"
	"github.com/adham90/opentrace/internal/logstore/ingest"
	"github.com/adham90/opentrace/pkg/store"
)

func newTestAdapter(t *testing.T) *LogStore {
	t.Helper()
	dir := t.TempDir()
	e, err := engine.NewStore(dir, nil, ingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { e.Close() })
	return New(e)
}

func TestAdapterBatchInsertAndSearch(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)

	now := time.Now().UTC()
	entries := []store.LogEntry{
		{Timestamp: now, Level: "info", Service: "api", Message: "request started"},
		{Timestamp: now, Level: "error", Service: "api", Message: "card declined", TraceID: "trace-1"},
		{Timestamp: now, Level: "info", Service: "worker", Message: "job completed"},
	}

	n, err := a.BatchInsert(ctx, entries)
	if err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}
	if n != 3 {
		t.Errorf("inserted: want 3, got %d", n)
	}

	// Search all
	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)
	result, err := a.Search(ctx, store.LogSearchParams{Start: &start, End: &end, Limit: 50})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("search all: want 3, got %d", len(result))
	}

	// Search by level
	result, err = a.Search(ctx, store.LogSearchParams{Level: "error", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("search error: want 1, got %d", len(result))
	}
	if len(result) > 0 && result[0].Message != "card declined" {
		t.Errorf("message: %q", result[0].Message)
	}

	// Search by trace_id
	result, err = a.Search(ctx, store.LogSearchParams{TraceID: "trace-1", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("search trace: want 1, got %d", len(result))
	}
}

func TestAdapterGetByID(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)

	now := time.Now().UTC()
	entries := []store.LogEntry{
		{Timestamp: now, Level: "info", Service: "api", Message: "hello", TraceID: "trace-x"},
	}

	n, err := a.BatchInsert(ctx, entries)
	if err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}
	if n != 1 {
		t.Fatalf("inserted: want 1, got %d", n)
	}

	// Search to get the ID
	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)
	result, err := a.Search(ctx, store.LogSearchParams{Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("no results from search")
	}

	id := result[0].ID
	entry, err := a.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if entry.Message != "hello" {
		t.Errorf("message: %q", entry.Message)
	}
	if entry.TraceID != "trace-x" {
		t.Errorf("trace_id: %q", entry.TraceID)
	}
}

func TestAdapterCountByLevel(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)

	now := time.Now().UTC()
	entries := []store.LogEntry{
		{Timestamp: now, Level: "info", Service: "api", Message: "msg1"},
		{Timestamp: now, Level: "info", Service: "api", Message: "msg2"},
		{Timestamp: now, Level: "error", Service: "api", Message: "msg3"},
	}

	a.BatchInsert(ctx, entries)

	counts, err := a.CountByLevel(ctx, store.LogCountParams{
		Since: now.Add(-time.Minute),
		Until: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CountByLevel: %v", err)
	}

	if counts["info"] != 2 {
		t.Errorf("info: want 2, got %d", counts["info"])
	}
	if counts["error"] != 1 {
		t.Errorf("error: want 1, got %d", counts["error"])
	}
}

func TestAdapterCountByService(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)

	now := time.Now().UTC()
	entries := []store.LogEntry{
		{Timestamp: now, Level: "info", Service: "api", Message: "msg1"},
		{Timestamp: now, Level: "info", Service: "api", Message: "msg2"},
		{Timestamp: now, Level: "info", Service: "worker", Message: "msg3"},
	}

	a.BatchInsert(ctx, entries)

	counts, err := a.CountByService(ctx, store.LogCountParams{
		Since: now.Add(-time.Minute),
		Until: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CountByService: %v", err)
	}

	if len(counts) != 2 {
		t.Errorf("services: want 2, got %d", len(counts))
	}
}

func TestAdapterHistogram(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)

	now := time.Now().UTC()
	entries := []store.LogEntry{
		{Timestamp: now, Level: "info", Service: "api", Message: "msg1"},
		{Timestamp: now, Level: "error", Service: "api", Message: "msg2"},
	}

	a.BatchInsert(ctx, entries)

	buckets, err := a.Histogram(ctx, store.LogHistogramParams{
		Since:    now.Add(-5 * time.Minute),
		Until:    now.Add(5 * time.Minute),
		Interval: time.Minute,
	})
	if err != nil {
		t.Fatalf("Histogram: %v", err)
	}

	total := 0
	for _, b := range buckets {
		total += b.Total
	}
	if total != 2 {
		t.Errorf("histogram total: want 2, got %d", total)
	}
}

func TestAdapterBatchDedup(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)

	// Unseen batch is not found.
	if batch, err := a.GetBatch(ctx, "batch-1"); err != nil || batch != nil {
		t.Errorf("GetBatch(unseen): want nil,nil got %+v,%v", batch, err)
	}

	// Recording makes it findable, so the handler can dedup a retry.
	if err := a.RecordBatch(ctx, "batch-1", 10); err != nil {
		t.Errorf("RecordBatch: %v", err)
	}
	batch, err := a.GetBatch(ctx, "batch-1")
	if err != nil {
		t.Errorf("GetBatch: %v", err)
	}
	if batch == nil || batch.LogCount != 10 {
		t.Errorf("GetBatch(recorded): want count 10, got %+v", batch)
	}

	// PruneBatches with a large window keeps the just-recorded batch.
	if n, err := a.PruneBatches(ctx, time.Hour); err != nil || n != 0 {
		t.Errorf("PruneBatches(1h): want 0,nil got %d,%v", n, err)
	}
}

func TestAdapterMetadataKeys(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)

	keys, err := a.MetadataKeys(ctx, store.LogCountParams{})
	if err != nil {
		t.Errorf("MetadataKeys: %v", err)
	}
	if keys != nil {
		t.Errorf("MetadataKeys should return nil (deprecated)")
	}
}

func TestAdapterInterfaceCompliance(t *testing.T) {
	// Compile-time check that LogStore implements store.LogStore
	var _ store.LogStore = (*LogStore)(nil)
}
