package engine

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

// benchRows is the sealed population used by the query benchmarks: one hour,
// one chunk (chunkSize is 50000), enough rows that per-row costs dominate.
const benchRows = 20000

// benchStore builds a store holding n sealed mixed rows (requests, app logs and
// errors) inside a single hour, plus m live rows in the unsealed WAL. It is the
// shape a real hour has: most queries straddle sealed chunks and the live WAL.
func benchStore(tb testing.TB, sealed, live int) (*Store, time.Time) {
	tb.Helper()
	s, base := newClockedStoreTB(tb)

	if _, err := s.Ingest(benchEntries(base, sealed)); err != nil {
		tb.Fatalf("ingest sealed: %v", err)
	}
	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		tb.Fatalf("seal: %v", err)
	}
	if live > 0 {
		if _, err := s.Ingest(benchEntries(base.Add(time.Hour), live)); err != nil {
			tb.Fatalf("ingest live: %v", err)
		}
	}
	return s, base
}

// benchEntries generates n entries spread over an hour with a realistic mix:
// two thirds HTTP requests, one sixth app logs, one sixth errors.
func benchEntries(base time.Time, n int) []chunk.Entry {
	out := make([]chunk.Entry, n)
	services := []string{"api", "web", "worker"}
	for i := range out {
		e := chunk.Entry{
			Ts:        base.Add(time.Duration(i) * time.Millisecond).UnixMilli(),
			Level:     "info",
			Service:   services[i%len(services)],
			Env:       "production",
			Message:   fmt.Sprintf("request %d completed for account %d", i, i%500),
			TraceID:   fmt.Sprintf("trace-%d", i%1000),
			RequestID: fmt.Sprintf("req-%d", i),
			UserID:    fmt.Sprintf("user-%d", i%200),
			Body:      []byte(`{"metadata":{"region":"eu-west-1"}}`),
		}
		switch i % 6 {
		case 0, 1, 2, 3:
			e.Kind = "request"
			e.EventType = "http.request"
			e.Method = "GET"
			e.Path = fmt.Sprintf("/orders/%d", i%50)
			e.Handler = "OrdersController#show"
			e.Status = 200
			e.DurationMs = 10 + i%500
			e.DbCount = i % 20
			e.DbMs = (i % 20) * 2
			e.CacheHits = 7
			e.CacheMisses = 3
		case 4:
			e.Kind = "log"
			e.EventType = "app.log"
			e.Message = fmt.Sprintf("cache warm for shard %d", i%16)
		case 5:
			e.Kind = "error"
			e.Level = "error"
			e.EventType = "app.error"
			e.ErrorClass = "NoMethodError"
			e.ErrorMessage = "undefined method `total' for nil"
			e.ErrorFingerprint = fmt.Sprintf("fp-%d", i%25)
			e.SourceFile = "app/models/order.rb"
			e.SourceLine = 87
		}
		out[i] = e
	}
	return out
}

func benchRange(base time.Time) (time.Time, time.Time) {
	return base.Add(-time.Minute), base.Add(2 * time.Hour)
}

func benchParams(base time.Time) SearchParams {
	start, end := benchRange(base)
	return SearchParams{Start: &start, End: &end}
}

// --- Query benchmarks ---

// BenchmarkSearchRecent is the most common query: the newest page of logs over
// a range far wider than the page.
func BenchmarkSearchRecent(b *testing.B) {
	s, base := benchStore(b, benchRows, 2000)
	p := benchParams(base)
	p.Limit = 50
	b.ResetTimer()
	for range b.N {
		if _, err := s.Search(p); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSearchFiltered narrows by the column-indexed filters, which should
// discard rows before anything is materialized.
func BenchmarkSearchFiltered(b *testing.B) {
	s, base := benchStore(b, benchRows, 2000)
	p := benchParams(base)
	p.Level = "error"
	p.Service = "api"
	p.Limit = 50
	b.ResetTimer()
	for range b.N {
		if _, err := s.Search(p); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSearchFullText exercises the inverted index path.
func BenchmarkSearchFullText(b *testing.B) {
	s, base := benchStore(b, benchRows, 2000)
	p := benchParams(base)
	p.Query = "cache warm"
	p.Limit = 50
	b.ResetTimer()
	for range b.N {
		if _, err := s.Search(p); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCountByLevel is the overview/status hot path — it needs one column.
func BenchmarkCountByLevel(b *testing.B) {
	s, base := benchStore(b, benchRows, 2000)
	start, end := benchRange(base)
	b.ResetTimer()
	for range b.N {
		if _, err := s.CountByLevel(start, end, "api", ""); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCountByServiceDetailed backs the per-service traffic summary.
func BenchmarkCountByServiceDetailed(b *testing.B) {
	s, base := benchStore(b, benchRows, 2000)
	start, end := benchRange(base)
	b.ResetTimer()
	for range b.N {
		if _, err := s.CountByServiceDetailed(start, end, "production"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHistogramFiltered backs the log-volume chart with a service filter,
// which cannot use the precomputed per-minute buckets.
func BenchmarkHistogramFiltered(b *testing.B) {
	s, base := benchStore(b, benchRows, 2000)
	start, end := benchRange(base)
	b.ResetTimer()
	for range b.N {
		if _, err := s.HistogramFiltered(start, end, time.Minute, HistogramFilter{Service: "api"}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDistinctValues backs watch COUNT DISTINCT over a non-dict column.
func BenchmarkDistinctValues(b *testing.B) {
	s, base := benchStore(b, benchRows, 2000)
	start, end := benchRange(base)
	b.ResetTimer()
	for range b.N {
		if _, err := s.DistinctValues("error_fingerprint", start, end, "", "", ""); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAggregateRequests backs logs(action:"performance") — a whole-range
// scan that reads a handful of numeric columns.
func BenchmarkAggregateRequests(b *testing.B) {
	s, base := benchStore(b, benchRows, 2000)
	p := benchParams(base)
	p.RequestsOnly = true
	b.ResetTimer()
	for range b.N {
		if _, err := s.AggregateRequests(p); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSearchRequestsSlowest backs "the 20 slowest requests", which must
// order the whole range before taking a page.
func BenchmarkSearchRequestsSlowest(b *testing.B) {
	s, base := benchStore(b, benchRows, 2000)
	p := benchParams(base)
	p.RequestsOnly = true
	b.ResetTimer()
	for range b.N {
		if _, err := s.SearchRequests(p, "duration_ms", 20, 0); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetByID is the single-entry lookup behind logs(action:"context").
func BenchmarkGetByID(b *testing.B) {
	s, base := benchStore(b, benchRows, 0)
	p := benchParams(base)
	p.Limit = 1
	res, err := s.Search(p)
	if err != nil || len(res.Entries) == 0 {
		b.Fatalf("seed search: %v (%d entries)", err, len(res.Entries))
	}
	id := res.Entries[0].ID
	b.ResetTimer()
	for range b.N {
		if _, err := s.GetByID(id); err != nil {
			b.Fatal(err)
		}
	}
}

// --- Write benchmarks ---

// BenchmarkIngest measures the SDK-facing write path (sampling, PII scrub,
// expansion, WAL append) for a typical 200-entry batch.
func BenchmarkIngest(b *testing.B) {
	s, base := newClockedStoreTB(b)
	batch := benchEntries(base, 200)
	b.ResetTimer()
	for range b.N {
		if _, err := s.Ingest(batch); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSeal measures the hourly rotation: read the WAL back, encode every
// column, build the inverted index.
func BenchmarkSeal(b *testing.B) {
	for range b.N {
		b.StopTimer()
		s, base := newClockedStoreTB(b)
		if _, err := s.Ingest(benchEntries(base, benchRows)); err != nil {
			b.Fatal(err)
		}
		s.writer.now = func() time.Time { return base.Add(time.Hour) }
		b.StartTimer()
		if err := s.SealCurrentHour(); err != nil {
			b.Fatal(err)
		}
	}
}

// TestMain silences the store's operational logging during tests and
// benchmarks. Seal/segment INFO lines are written per benchmark iteration and
// interleave with the benchmark output, and the log write itself shows up in
// the timings.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// BenchmarkSearchMetadataFiltered exercises the one filter that still cannot be
// decided from a column: it needs the entry body. That path materializes rows to
// judge them, so it is the memory-sensitive one — it must still return a page
// without holding the whole range in memory.
func BenchmarkSearchMetadataFiltered(b *testing.B) {
	s, base := benchStore(b, benchRows, 2000)
	p := benchParams(base)
	p.MetadataFilter = map[string]string{"region": "eu-west-1"}
	p.Limit = 50
	b.ResetTimer()
	for range b.N {
		if _, err := s.Search(p); err != nil {
			b.Fatal(err)
		}
	}
}
