package engine

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/ingest"
)

// bytes is used by PII scrub check below
var _ = bytes.Contains

// TestIntegrationEndToEnd exercises the full lifecycle:
// ingest → seal → search → GetByID → histogram → prune
func TestIntegrationEndToEnd(t *testing.T) {
	dir := t.TempDir()
	pii := ingest.DefaultPIIConfig()
	s, err := NewStore(dir, nil, pii)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()

	// --- Ingest mix of entry types ---
	body := json.RawMessage(`{
		"exception": {"backtrace": ["app/services/billing.rb:99"]},
		"request": {"params": {"email": "user@example.com", "card": "4111111111111111"}},
		"queries": [{"sql": "SELECT * FROM users WHERE id = 42", "duration_ms": 1.2}],
		"timeline": [{"t": "db", "n": "User Load", "ms": 1.2, "at": 0}],
		"logs": [{"level": "debug", "message": "Charging card", "at": 5}]
	}`)

	entries := []chunk.Entry{
		// Simple logs
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Env: "production", Message: "Cache hit for user 42"},
		{Ts: now.UnixMilli(), Level: "debug", Service: "worker", Message: "Job started: ProcessOrderJob"},
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Env: "production", Message: "GET /api/users 200 45ms"},

		// Error with body (triggers PII scrub + fingerprint computation + log expansion)
		{Ts: now.UnixMilli(), Level: "error", Service: "api", Env: "production",
			Message:    "PaymentError: card declined",
			TraceID:    "trace-payment-123",
			RequestID:  "req-abc",
			UserID:     "42",
			ErrorClass: "PaymentError",
			SourceFile: "app/services/billing.rb",
			SourceLine: 99,
			Body:       body},

		// Structured log
		{Ts: now.UnixMilli(), Level: "warn", Service: "api", Env: "production",
			Message:   "Slow query detected",
			TraceID:   "trace-payment-123",
			EventType: "db.slow_query"},
	}

	result, err := s.Ingest(entries)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// Should have 5 original + 1 expanded in-request log = 6
	if len(result) != 6 {
		t.Fatalf("ingest result: want 6 (5 + 1 expanded log), got %d", len(result))
	}

	// Verify PII was scrubbed
	errorEntry := result[3]
	if errorEntry.ErrorClass != "PaymentError" {
		t.Errorf("error_class: %q", errorEntry.ErrorClass)
	}
	if errorEntry.ErrorFingerprint == "" {
		t.Error("fingerprint should be set")
	}
	bodyStr := string(errorEntry.Body)
	if bytes.Contains(errorEntry.Body, []byte("user@example.com")) {
		t.Error("email should be scrubbed from body")
	}
	if bytes.Contains(errorEntry.Body, []byte("4111111111111111")) {
		t.Error("credit card should be scrubbed from body")
	}
	t.Logf("scrubbed body: %s", bodyStr[:min(200, len(bodyStr))])

	// Verify expanded in-request log (inserted after parent error entry)
	// Order: [0:info, 1:debug, 2:info, 3:error, 4:expanded_from_error, 5:warn]
	expandedLog := result[4]
	if expandedLog.EventType != "in_request_log" {
		t.Errorf("expanded event_type: want in_request_log, got %q", expandedLog.EventType)
	}
	if expandedLog.TraceID != "trace-payment-123" {
		t.Errorf("expanded trace_id: %q", expandedLog.TraceID)
	}

	// --- Seal ---
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if s.segments.SegmentCount() != 1 {
		t.Fatalf("segments after seal: want 1, got %d", s.segments.SegmentCount())
	}

	// --- Search sealed data ---
	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)

	// All entries
	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 100})
	if err != nil {
		t.Fatalf("Search all: %v", err)
	}
	if res.Total != 6 {
		t.Errorf("search all: want 6, got %d", res.Total)
	}

	// By level
	res, err = s.Search(SearchParams{Level: "error", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("search error: want 1, got %d", res.Total)
	}

	// By service
	res, err = s.Search(SearchParams{Service: "worker", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search worker: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("search worker: want 1, got %d", res.Total)
	}

	// By trace_id
	res, err = s.Search(SearchParams{TraceID: "trace-payment-123", Start: &start, End: &end, Limit: 100})
	if err != nil {
		t.Fatalf("Search trace: %v", err)
	}
	// Error entry + warn entry + expanded log = 3
	if res.Total != 3 {
		t.Errorf("search trace: want 3, got %d", res.Total)
	}

	// FTS (inverted index)
	res, err = s.Search(SearchParams{Query: "cache hit", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search FTS: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("search FTS 'cache hit': want 1, got %d", res.Total)
	}

	// --- GetByID ---
	entry, err := s.GetByID(result[3].ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if entry.ErrorClass != "PaymentError" {
		t.Errorf("GetByID error_class: %q", entry.ErrorClass)
	}
	if len(entry.Body) == 0 {
		t.Error("GetByID should include body")
	}

	// --- GetBody ---
	bodyResult, err := s.GetBody(result[3].ID)
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	if len(bodyResult) == 0 {
		t.Error("GetBody should return body")
	}

	// --- CountByLevel ---
	counts, err := s.CountByLevel(start, end, "", "")
	if err != nil {
		t.Fatalf("CountByLevel: %v", err)
	}
	totalFromCounts := 0
	for _, c := range counts {
		totalFromCounts += c
	}
	if totalFromCounts != 6 {
		t.Errorf("total from counts: want 6, got %d (counts: %v)", totalFromCounts, counts)
	}

	// --- CountByService ---
	svcCounts, err := s.CountByService(start, end, "")
	if err != nil {
		t.Fatalf("CountByService: %v", err)
	}
	if svcCounts["api"] != 5 { // 3 original api + 1 error api + 1 expanded (inherits api)
		t.Errorf("api count: want 5, got %d (counts: %v)", svcCounts["api"], svcCounts)
	}

	// --- Histogram ---
	buckets, err := s.Histogram(start, end, time.Minute)
	if err != nil {
		t.Fatalf("Histogram: %v", err)
	}
	totalFromHist := 0
	for _, b := range buckets {
		totalFromHist += b.Total
	}
	if totalFromHist != 6 {
		t.Errorf("histogram total: want 6, got %d", totalFromHist)
	}

	// --- DistinctValues ---
	services, err := s.DistinctValues("service", start, end, "", "", "")
	if err != nil {
		t.Fatalf("DistinctValues: %v", err)
	}
	if len(services) != 2 { // api, worker
		t.Errorf("distinct services: want 2, got %d (%v)", len(services), services)
	}

	t.Log("=== End-to-end integration test passed ===")
}

// TestIntegrationConcurrentIngestAndSearch tests concurrent writes and reads.
func TestIntegrationConcurrentIngestAndSearch(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, nil, ingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)

	// Ingest 500 entries across 5 goroutines while searching concurrently
	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	// Writers
	for g := range 5 {
		wg.Add(1)
		go func(goroutine int) {
			defer wg.Done()
			for i := range 100 {
				level := "info"
				if i%10 == 0 {
					level = "error"
				}
				_, err := s.Ingest([]chunk.Entry{{
					Ts: now.UnixMilli(), Level: level, Service: "api",
					Message: "concurrent test entry",
				}})
				if err != nil {
					errCh <- err
					return
				}
			}
		}(g)
	}

	// Readers
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 10})
				if err != nil {
					errCh <- err
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent error: %v", err)
	}

	// Verify all entries were written
	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 1000})
	if err != nil {
		t.Fatalf("final search: %v", err)
	}
	if res.Total != 500 {
		t.Errorf("total entries: want 500, got %d", res.Total)
	}
}

// TestIntegrationIngestSealSearchCycle tests the full write → seal → read cycle.
func TestIntegrationIngestSealSearchCycle(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, nil, ingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	start := now.Add(-time.Minute)
	end := now.Add(time.Minute)

	// Ingest batch 1
	_, err = s.Ingest([]chunk.Entry{
		{Ts: now.UnixMilli(), Level: "info", Service: "api", Message: "batch 1 entry 1"},
		{Ts: now.UnixMilli(), Level: "error", Service: "api", Message: "batch 1 error"},
	})
	if err != nil {
		t.Fatalf("Ingest batch 1: %v", err)
	}

	// Seal
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("Seal 1: %v", err)
	}

	// Ingest batch 2 (goes to new WAL)
	_, err = s.Ingest([]chunk.Entry{
		{Ts: now.UnixMilli(), Level: "info", Service: "worker", Message: "batch 2 entry 1"},
		{Ts: now.UnixMilli(), Level: "warn", Service: "worker", Message: "batch 2 warning"},
	})
	if err != nil {
		t.Fatalf("Ingest batch 2: %v", err)
	}

	// Search should find entries from both sealed segment AND active WAL
	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 4 {
		t.Errorf("search total: want 4 (2 sealed + 2 active), got %d", res.Total)
	}

	// Search by service should span both stores
	res, err = s.Search(SearchParams{Service: "api", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search api: %v", err)
	}
	if res.Total != 2 {
		t.Errorf("search api: want 2, got %d", res.Total)
	}

	res, err = s.Search(SearchParams{Service: "worker", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("Search worker: %v", err)
	}
	if res.Total != 2 {
		t.Errorf("search worker: want 2, got %d", res.Total)
	}
}
