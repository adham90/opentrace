package engine

import (
	"runtime"
	"testing"
	"time"
)

// The performance guards in this file exist because every expensive regression
// this engine has had looked the same from the outside: a query that returns a
// small answer quietly started reading, decoding or materializing every row in
// range. Wall-clock budgets catch that only on a machine as fast as the one
// they were tuned on, so the primary assertion here is on allocation, which is
// deterministic and machine-independent.

// perfSmall and perfLarge are the two populations the scaling guard compares.
// The ratio between them is what the budgets are written against.
const (
	perfSmall = 5000
	perfLarge = 20000
)

// allocScaleTolerance is how much a fixed-size answer's cost may grow when the
// scanned population grows 4x.
//
// Some growth is real and correct: the scan has to consider every candidate row,
// so the row-number bookkeeping is proportional to the population. That puts a
// healthy query around 1.7-1.9x here. A query that materializes an entry per
// matching row lands near 4.0x — full linear growth — which is the regression
// this is written to catch. 2.5 sits well clear of both.
const allocScaleTolerance = 2.5

// TestQueryCostDoesNotScaleWithRowsScanned is the core regression guard.
//
// Each of these queries returns an answer whose size does not depend on how
// many rows were scanned — a count, a histogram, a distinct set, an aggregate,
// one page of results. Quadrupling the rows in range must therefore not
// quadruple what the query allocates. It used to: every one of these paths
// built a 600-byte entry with ~45 decoded columns for every matching row.
func TestQueryCostDoesNotScaleWithRowsScanned(t *testing.T) {
	if testing.Short() {
		t.Skip("perf guard: builds and seals 25k rows")
	}

	small, smallBase := benchStore(t, perfSmall, 0)
	large, largeBase := benchStore(t, perfLarge, 0)

	cases := []struct {
		name string
		run  func(s *Store, base time.Time) error
	}{
		{"count by level", func(s *Store, base time.Time) error {
			start, end := benchRange(base)
			_, err := s.CountByLevel(start, end, "api", "")
			return err
		}},
		{"count by service", func(s *Store, base time.Time) error {
			start, end := benchRange(base)
			_, err := s.CountByServiceDetailed(start, end, "production")
			return err
		}},
		{"histogram", func(s *Store, base time.Time) error {
			start, end := benchRange(base)
			_, err := s.HistogramFiltered(start, end, time.Minute, HistogramFilter{Service: "api"})
			return err
		}},
		{"distinct values", func(s *Store, base time.Time) error {
			start, end := benchRange(base)
			_, err := s.DistinctValues("error_fingerprint", start, end, "", "", "")
			return err
		}},
		{"aggregate requests", func(s *Store, base time.Time) error {
			p := benchParams(base)
			p.RequestsOnly = true
			_, err := s.AggregateRequests(p)
			return err
		}},
		{"slowest requests", func(s *Store, base time.Time) error {
			p := benchParams(base)
			p.RequestsOnly = true
			_, err := s.SearchRequests(p, "duration_ms", 20, 0)
			return err
		}},
		{"recent page", func(s *Store, base time.Time) error {
			p := benchParams(base)
			p.Limit = 50
			_, err := s.Search(p)
			return err
		}},
		{"filtered page", func(s *Store, base time.Time) error {
			p := benchParams(base)
			p.Level, p.Service, p.Limit = "error", "api", 50
			_, err := s.Search(p)
			return err
		}},
		{"single entry by id", func(s *Store, base time.Time) error {
			p := benchParams(base)
			p.Limit = 1
			res, err := s.Search(p)
			if err != nil || len(res.Entries) == 0 {
				return err
			}
			_, err = s.GetByID(res.Entries[0].ID)
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			smallBytes := allocBytesPerRun(t, func() { mustRun(t, tc.run(small, smallBase)) })
			largeBytes := allocBytesPerRun(t, func() { mustRun(t, tc.run(large, largeBase)) })

			if smallBytes == 0 {
				t.Fatalf("measured 0 bytes on the small store; the measurement is broken")
			}
			ratio := float64(largeBytes) / float64(smallBytes)
			t.Logf("%d rows: %d B/op | %d rows: %d B/op | ratio %.2fx",
				perfSmall, smallBytes, perfLarge, largeBytes, ratio)
			if ratio > allocScaleTolerance {
				t.Errorf("cost grew %.2fx when the scanned rows grew %.0fx (limit %.1fx): "+
					"this query is reading or materializing per scanned row again",
					ratio, float64(perfLarge)/float64(perfSmall), allocScaleTolerance)
			}
		})
	}
}

// TestInteractiveQueryLatency is the coarse backstop: an absolute ceiling that
// catches a blowup no allocation ratio would (a lock convoy, a re-added
// per-query fsync, an accidental O(n^2) sort). The budget is deliberately far
// above the real numbers — around 100x on the machine this was written on — so
// it stays quiet on a slow or loaded CI runner and only fires on a genuine
// pathology.
func TestInteractiveQueryLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("perf guard: builds and seals 20k rows")
	}

	s, base := benchStore(t, perfLarge, 2000)
	start, end := benchRange(base)

	const budget = 2 * time.Second

	ops := []struct {
		name string
		run  func() error
	}{
		{"recent page", func() error {
			p := benchParams(base)
			p.Limit = 50
			_, err := s.Search(p)
			return err
		}},
		{"full text search", func() error {
			p := benchParams(base)
			p.Query, p.Limit = "cache warm", 50
			_, err := s.Search(p)
			return err
		}},
		{"status counts", func() error {
			_, err := s.CountByLevel(start, end, "", "")
			return err
		}},
		{"performance summary", func() error {
			p := benchParams(base)
			p.RequestsOnly = true
			_, err := s.AggregateRequests(p)
			return err
		}},
		{"slowest requests", func() error {
			p := benchParams(base)
			p.RequestsOnly = true
			_, err := s.SearchRequests(p, "duration_ms", 20, 0)
			return err
		}},
	}

	for _, op := range ops {
		// Warm once: the first call pays the decode that later calls reuse, and
		// what this guards is the steady state a running server is in.
		mustRun(t, op.run())
		began := time.Now()
		mustRun(t, op.run())
		elapsed := time.Since(began)
		t.Logf("%s: %s", op.name, elapsed)
		if elapsed > budget {
			t.Errorf("%s took %s, over the %s budget", op.name, elapsed, budget)
		}
	}
}

// TestIngestAllocationsPerEntryStayFlat guards the WAL batch buffer:
// serializing a batch must reuse one buffer, not allocate per entry.
func TestIngestAllocationsPerEntryStayFlat(t *testing.T) {
	if testing.Short() {
		t.Skip("perf guard: ingests two differently sized batches")
	}

	measure := func(batchSize int) int64 {
		s, base := newClockedStoreTB(t)
		batch := benchEntries(base, batchSize)
		// Warm the writer's buffer so the measurement is steady-state.
		mustIngest(t, s, cloneEntries(batch)...)
		return allocBytesPerRun(t, func() { mustIngest(t, s, cloneEntries(batch)...) }) / int64(batchSize)
	}

	small := measure(50)
	large := measure(500)
	t.Logf("per-entry allocation: 50-entry batch %d B, 500-entry batch %d B", small, large)
	if large > small {
		t.Errorf("a 10x larger batch costs %d B/entry vs %d B/entry: ingest is not amortizing per batch",
			large, small)
	}
}

// allocBytesPerRun reports the bytes allocated by one call to fn, averaged over
// a few runs with the garbage collector settled between them.
func allocBytesPerRun(t *testing.T, fn func()) int64 {
	t.Helper()
	const runs = 5

	fn() // warm: first call fills the caches later calls reuse

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		fn()
	}
	runtime.ReadMemStats(&after)
	return int64(after.TotalAlloc-before.TotalAlloc) / runs
}

func mustRun(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
}
