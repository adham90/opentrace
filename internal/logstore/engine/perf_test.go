package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/ingest"
)

// perfRowCount is the sealed-row population used by the whole-range scan
// benchmarks. One hour, one chunk (chunkSize is 50000).
const perfRowCount = 20000

// sealedRequestStore builds a store holding n sealed http.request rows inside a
// single chunk of a single hour.
func sealedRequestStore(t testing.TB, n int) (*Store, time.Time) {
	t.Helper()
	s, base := newClockedStoreTB(t)

	batch := make([]chunk.Entry, n)
	for i := range batch {
		dur := int64(10 + i%500)
		db := int64(i % 20)
		batch[i] = chunk.Entry{
			Ts:          base.Add(time.Duration(i) * time.Millisecond).UnixMilli(),
			Level:       "info",
			Service:     "api",
			Env:         "production",
			Kind:        "request",
			EventType:   "http.request",
			Message:     fmt.Sprintf("GET /orders/%d", i),
			Method:      "GET",
			Path:        fmt.Sprintf("/orders/%d", i%50),
			Handler:     "OrdersController#show",
			DurationMs:  int(dur),
			DbCount:     int(db),
			DbMs:        int(db * 2),
			CacheHits:   7,
			CacheMisses: 3,
		}
	}
	if _, err := s.Ingest(batch); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	return s, base
}

// newClockedStoreTB is newClockedStore for both tests and benchmarks.
func newClockedStoreTB(t testing.TB) (*Store, time.Time) {
	t.Helper()
	s, err := NewStore(t.TempDir(), nil, ingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	base := time.Unix(s.writer.SegmentHour()*3600, 0).UTC()
	s.writer.now = func() time.Time { return base }
	return s, base
}

func perfParams(base time.Time) SearchParams {
	start := base.Add(-time.Minute)
	end := base.Add(time.Hour)
	return SearchParams{RequestsOnly: true, Start: &start, End: &end}
}

// TestWholeRangeScanPerformance guards the O(rows² × columns) regression: the
// whole-range scans (slowest endpoints, performance summary, p95 watchers) read
// every matching row, and chunk.Reader re-decodes a whole column per Read call.
// Without a per-scan column cache this took ~90s for 20k rows; with one it is
// well under a second.
func TestWholeRangeScanPerformance(t *testing.T) {
	if testing.Short() {
		// Building and sealing 20k rows is too slow for -short.
		t.Skip("perf test: skipped in short mode")
	}
	s, base := sealedRequestStore(t, perfRowCount)

	aggStart := time.Now()
	agg, err := s.AggregateRequests(perfParams(base))
	if err != nil {
		t.Fatalf("AggregateRequests: %v", err)
	}
	aggElapsed := time.Since(aggStart)

	searchStart := time.Now()
	rows, err := s.SearchRequests(perfParams(base), "duration_ms", 20, 0)
	if err != nil {
		t.Fatalf("SearchRequests: %v", err)
	}
	searchElapsed := time.Since(searchStart)

	t.Logf("AggregateRequests(%d rows): %s", perfRowCount, aggElapsed)
	t.Logf("SearchRequests top-20(%d rows): %s", perfRowCount, searchElapsed)

	if agg.Count != perfRowCount {
		t.Fatalf("aggregate count: want %d, got %d", perfRowCount, agg.Count)
	}
	if len(rows) != 20 {
		t.Fatalf("want 20 rows, got %d", len(rows))
	}
	// Rows are seeded with duration 10+i%500, so the maximum is 509.
	if rows[0].DurationMs != 509 {
		t.Fatalf("slowest row: want 509ms, got %d", rows[0].DurationMs)
	}

	const budget = 5 * time.Second // generous: the fixed path is ~100x under this
	if aggElapsed > budget || searchElapsed > budget {
		t.Fatalf("whole-range scan is quadratic again: aggregate=%s search=%s (budget %s)",
			aggElapsed, searchElapsed, budget)
	}
}

func BenchmarkAggregateRequests20k(b *testing.B) {
	s, base := sealedRequestStore(b, perfRowCount)
	b.ResetTimer()
	for range b.N {
		if _, err := s.AggregateRequests(perfParams(base)); err != nil {
			b.Fatalf("AggregateRequests: %v", err)
		}
	}
}

func BenchmarkSearchRequests20k(b *testing.B) {
	s, base := sealedRequestStore(b, perfRowCount)
	b.ResetTimer()
	for range b.N {
		if _, err := s.SearchRequests(perfParams(base), "duration_ms", 20, 0); err != nil {
			b.Fatalf("SearchRequests: %v", err)
		}
	}
}
