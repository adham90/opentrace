package engine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/ingest"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir, nil, ingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStoreIngestAndSearch(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().UTC()
	entries := []chunk.Entry{
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Message: "request started"},
		{Ts: now.UnixMilli(), Level: "error", Service: "api", Message: "card declined"},
		{Ts: now.UnixMilli(), Level: "info", Service: "worker", Message: "job completed"},
	}

	result, err := s.Ingest(entries)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("ingest result: want 3, got %d", len(result))
	}

	// IDs should be assigned
	for i, e := range result {
		if e.ID == 0 {
			t.Errorf("entry %d: ID should not be 0", i)
		}
		if e.ReceivedAt == 0 {
			t.Errorf("entry %d: ReceivedAt should not be 0", i)
		}
	}

	// Search from active WAL
	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)

	// Search all
	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 50})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 3 {
		t.Errorf("search all: want 3, got %d", res.Total)
	}

	// Search by level
	res, err = s.Search(SearchParams{Level: "error", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("search error: want 1, got %d", res.Total)
	}
	if res.Entries[0].Message != "card declined" {
		t.Errorf("search error: wrong message %q", res.Entries[0].Message)
	}

	// Search by service
	res, err = s.Search(SearchParams{Service: "worker", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("search worker: want 1, got %d", res.Total)
	}

	// Search by query (substring match on active WAL)
	res, err = s.Search(SearchParams{Query: "card", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("search 'card': want 1, got %d", res.Total)
	}
}

func TestStoreSealAndSearch(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().UTC()
	entries := []chunk.Entry{
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Message: "request started"},
		{Ts: now.UnixMilli(), Level: "error", Service: "api", Message: "timeout connecting to redis"},
		{Ts: now.UnixMilli(), Level: "warn", Service: "api", Message: "slow query detected"},
		{Ts: now.UnixMilli(), Level: "info", Service: "worker", Message: "job completed"},
	}

	_, err := s.Ingest(entries)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// Seal
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if s.segments.SegmentCount() != 1 {
		t.Fatalf("segment count: want 1, got %d", s.segments.SegmentCount())
	}

	// Search sealed data
	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)

	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 50})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 4 {
		t.Errorf("search all: want 4, got %d", res.Total)
	}

	// Search by level on sealed data
	res, err = s.Search(SearchParams{Level: "error", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("search error: want 1, got %d", res.Total)
	}

	// FTS on sealed data (inverted index)
	res, err = s.Search(SearchParams{Query: "timeout redis", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("search 'timeout redis': want 1, got %d", res.Total)
	}
	if len(res.Entries) > 0 && res.Entries[0].Message != "timeout connecting to redis" {
		t.Errorf("search FTS: wrong message %q", res.Entries[0].Message)
	}
}

func TestStoreGetByID(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().UTC()
	body := json.RawMessage(`{"queries":[{"sql":"SELECT 1"}]}`)
	entries := []chunk.Entry{
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Message: "request 1", TraceID: "trace-1"},
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Message: "request 2", TraceID: "trace-2", Body: body},
	}

	result, err := s.Ingest(entries)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// GetByID from active WAL
	entry, err := s.GetByID(result[1].ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if entry.Message != "request 2" {
		t.Errorf("message: want %q, got %q", "request 2", entry.Message)
	}
	if entry.TraceID != "trace-2" {
		t.Errorf("trace_id: want %q, got %q", "trace-2", entry.TraceID)
	}

	// Seal and GetByID from sealed segment
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	entry, err = s.GetByID(result[0].ID)
	if err != nil {
		t.Fatalf("GetByID sealed: %v", err)
	}
	if entry.Message != "request 1" {
		t.Errorf("sealed message: want %q, got %q", "request 1", entry.Message)
	}

	// GetByID with body
	entry, err = s.GetByID(result[1].ID)
	if err != nil {
		t.Fatalf("GetByID with body: %v", err)
	}
	if len(entry.Body) == 0 {
		t.Error("body should not be empty")
	}
}

func TestStoreCountByLevel(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().UTC()
	entries := []chunk.Entry{
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Message: "msg1"},
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Message: "msg2"},
		{Ts: now.UnixMilli(), Level: "error", Service: "api", Message: "msg3"},
		{Ts: now.UnixMilli(), Level: "warn", Service: "api", Message: "msg4"},
	}

	if _, err := s.Ingest(entries); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)
	counts, err := s.CountByLevel(start, end, "", "")
	if err != nil {
		t.Fatalf("CountByLevel: %v", err)
	}

	if counts["info"] != 2 {
		t.Errorf("info count: want 2, got %d", counts["info"])
	}
	if counts["error"] != 1 {
		t.Errorf("error count: want 1, got %d", counts["error"])
	}
}

func TestStoreHistogram(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().UTC()
	entries := make([]chunk.Entry, 10)
	for i := range entries {
		entries[i] = chunk.Entry{
			Ts: now.UnixMilli(), Level: "info", Service: "api", Message: "test",
		}
	}
	entries[3].Level = "error"
	entries[7].Level = "error"

	if _, err := s.Ingest(entries); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	start := now.Add(-5 * time.Minute)
	end := now.Add(5 * time.Minute)
	buckets, err := s.Histogram(start, end, time.Minute)
	if err != nil {
		t.Fatalf("Histogram: %v", err)
	}

	totalEntries := 0
	totalErrors := 0
	for _, b := range buckets {
		totalEntries += b.Total
		totalErrors += b.Errors
	}

	if totalEntries != 10 {
		t.Errorf("histogram total: want 10, got %d", totalEntries)
	}
	if totalErrors != 2 {
		t.Errorf("histogram errors: want 2, got %d", totalErrors)
	}
}

func TestStoreDistinctValues(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().UTC()
	entries := []chunk.Entry{
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Message: "msg"},
		{Ts: now.UnixMilli(), Level: "error", Service: "worker", Message: "msg"},
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Message: "msg"},
	}

	if _, err := s.Ingest(entries); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)
	vals, err := s.DistinctValues("service", start, end)
	if err != nil {
		t.Fatalf("DistinctValues: %v", err)
	}

	if len(vals) != 2 {
		t.Errorf("distinct services: want 2, got %d (%v)", len(vals), vals)
	}
}

func TestStoreTail(t *testing.T) {
	s := newTestStore(t)

	// Subscribe first
	snapshot, ch, unsub := s.Tail()
	if len(snapshot) != 0 {
		t.Errorf("initial snapshot: want 0, got %d", len(snapshot))
	}

	// Ingest
	now := time.Now().UTC()
	go func() {
		time.Sleep(10 * time.Millisecond)
		s.Ingest([]chunk.Entry{
			{Ts: now.UnixMilli(), Level: "info", Service: "api", Message: "live entry"},
		})
	}()

	select {
	case received := <-ch:
		if len(received) != 1 {
			t.Errorf("received: want 1, got %d", len(received))
		}
		if received[0].Message != "live entry" {
			t.Errorf("message: %q", received[0].Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tail did not receive within 2s")
	}

	unsub()
}

func TestStorePrune(t *testing.T) {
	// Prune is tested thoroughly in TestSegmentManagerPrune.
	// Here we just verify the Store.Prune method delegates correctly.
	s := newTestStore(t)

	now := time.Now().UTC()
	entries := []chunk.Entry{
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Message: "msg"},
	}

	if _, err := s.Ingest(entries); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if s.segments.SegmentCount() != 1 {
		t.Fatalf("before prune: want 1, got %d", s.segments.SegmentCount())
	}

	// Prune with 1-hour retention — current hour segment should NOT be deleted
	deleted, err := s.Prune(time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted: want 0 (segment is current hour), got %d", deleted)
	}
	if s.segments.SegmentCount() != 1 {
		t.Errorf("after prune: want 1 (kept), got %d", s.segments.SegmentCount())
	}
}

func TestStoreIngestWithPipeline(t *testing.T) {
	dir := t.TempDir()
	pii := ingest.DefaultPIIConfig()
	s, err := NewStore(dir, nil, pii)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	body := json.RawMessage(`{
		"exception": {"backtrace": ["app/models/order.rb:42"]},
		"request": {"params": {"email": "user@example.com", "password": "secret"}}
	}`)

	// SDK sends error fields flat
	entries := []chunk.Entry{
		{Ts: now.UnixMilli(), Level: "error", Service: "api",
			Message: "NoMethodError", ErrorClass: "NoMethodError",
			SourceFile: "app/models/order.rb", SourceLine: 42, Body: body},
	}

	result, err := s.Ingest(entries)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("result count: want 1, got %d", len(result))
	}

	// Error fields should be extracted
	if result[0].ErrorClass != "NoMethodError" {
		t.Errorf("error_class: %q", result[0].ErrorClass)
	}
	if result[0].ErrorFingerprint == "" {
		t.Error("fingerprint should be set")
	}
}

func TestStoreSearch_EnvFilterLegacyWildcard(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().UTC()
	entries := []chunk.Entry{
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Env: "production", Message: "prod hit"},
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Env: "staging", Message: "staging hit"},
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Env: "", Message: "legacy hit"}, // pre-multi-env row
	}

	if _, err := s.Ingest(entries); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)

	// A production filter should find the prod row AND the legacy row,
	// but not the staging row.
	res, err := s.Search(SearchParams{Env: "production", Start: &start, End: &end, Limit: 50})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 2 {
		t.Fatalf("env=production matched %d rows, want 2 (production + legacy), got messages: %v",
			res.Total, extractMessages(res.Entries))
	}

	// A staging filter finds staging row + legacy row (same wildcard rule).
	res, err = s.Search(SearchParams{Env: "staging", Start: &start, End: &end, Limit: 50})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 2 {
		t.Fatalf("env=staging matched %d rows, want 2 (staging + legacy), got messages: %v",
			res.Total, extractMessages(res.Entries))
	}

	// No env filter returns all three.
	res, err = s.Search(SearchParams{Start: &start, End: &end, Limit: 50})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 3 {
		t.Errorf("no env filter matched %d rows, want 3", res.Total)
	}
}

func extractMessages(es []chunk.Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Message
	}
	return out
}

func TestStoreCountByLevel_EnvFilter(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().UTC()
	entries := []chunk.Entry{
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Env: "production", Message: "prod1"},
		{Ts: now.UnixMilli(), Level: "error", Service: "api", Env: "production", Message: "prod2"},
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Env: "staging", Message: "staging1"},
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Env: "", Message: "legacy1"}, // legacy wildcard
	}
	if _, err := s.Ingest(entries); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)

	// No env filter: all four entries counted.
	counts, err := s.CountByLevel(start, end, "", "")
	if err != nil {
		t.Fatalf("CountByLevel: %v", err)
	}
	if counts["info"] != 3 || counts["error"] != 1 {
		t.Errorf("unfiltered counts = %+v, want info=3 error=1", counts)
	}

	// Production filter: 2 prod + 1 legacy (wildcard) = 3 total.
	// Breakdown: info from prod + info from legacy = 2 info; error from prod = 1 error.
	counts, err = s.CountByLevel(start, end, "", "production")
	if err != nil {
		t.Fatalf("CountByLevel env=production: %v", err)
	}
	if counts["info"] != 2 || counts["error"] != 1 {
		t.Errorf("env=production counts = %+v, want info=2 error=1", counts)
	}

	// Staging filter: 1 staging + 1 legacy = 2 total.
	counts, err = s.CountByLevel(start, end, "", "staging")
	if err != nil {
		t.Fatalf("CountByLevel env=staging: %v", err)
	}
	if counts["info"] != 2 {
		t.Errorf("env=staging counts = %+v, want info=2", counts)
	}
}

func TestStoreCountByService_EnvFilter(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().UTC()
	entries := []chunk.Entry{
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Env: "production", Message: "p1"},
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Env: "production", Message: "p2"},
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Env: "staging", Message: "s1"},
		{Ts: now.UnixMilli(), Level: "info", Service: "worker", Env: "staging", Message: "ws1"},
	}
	if _, err := s.Ingest(entries); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)

	// Production filter: api=2, worker=0.
	counts, err := s.CountByService(start, end, "production")
	if err != nil {
		t.Fatalf("CountByService: %v", err)
	}
	if counts["api"] != 2 {
		t.Errorf("env=production counts api = %d, want 2", counts["api"])
	}
	if counts["worker"] != 0 {
		t.Errorf("env=production worker unexpectedly counted: %d", counts["worker"])
	}
}
