package engine

import (
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/ingest"
)

func newClockedStore(t *testing.T) (*Store, time.Time) {
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

// TestCountByLevelSubHourWindow guards the over-count bug: sealed-segment counts
// used whole-hour precomputed totals with no intra-segment time filtering, so a
// 10-minute window returned the entire hour's count.
func TestCountByLevelSubHourWindow(t *testing.T) {
	s, base := newClockedStore(t)

	// 60 entries, one per minute across hour H.
	batch := make([]chunk.Entry, 60)
	for i := 0; i < 60; i++ {
		batch[i] = chunk.Entry{Ts: base.Add(time.Duration(i) * time.Minute).UnixMilli(), Level: "info", Service: "svc", Message: "m"}
	}
	if _, err := s.Ingest(batch); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	// Advance an hour and seal so the data lives in a sealed segment.
	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Count a 10-minute sub-window (minutes 0..10 inclusive = 11 entries).
	counts, err := s.CountByLevel(base, base.Add(10*time.Minute), "", "")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if got := counts["info"]; got != 11 {
		t.Fatalf("sub-hour count: want 11, got %d (whole-hour over-count regression if ~60)", got)
	}
}

// TestHistogramBounded guards the unbounded-histogram DoS: a huge range with a
// tiny interval must not produce ~10^17 buckets.
func TestHistogramBounded(t *testing.T) {
	s, base := newClockedStore(t)
	if _, err := s.Ingest([]chunk.Entry{{Ts: base.UnixMilli(), Level: "info", Service: "svc", Message: "m"}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	end := base.Add(time.Hour)
	buckets, err := s.Histogram(start, end, time.Nanosecond)
	if err != nil {
		t.Fatalf("histogram: %v", err)
	}
	if len(buckets) > maxHistogramBuckets+1 {
		t.Fatalf("histogram not bounded: got %d buckets (cap %d)", len(buckets), maxHistogramBuckets)
	}
}

// TestSearchAppliesStructuredFilters guards the ignored-filter bug: Path/Method
// were declared but never applied, on both the active-WAL and sealed paths.
func TestSearchAppliesStructuredFilters(t *testing.T) {
	s, base := newClockedStore(t)
	entries := []chunk.Entry{
		{Ts: base.UnixMilli(), Level: "info", Service: "svc", Message: "a", Method: "GET", Path: "/checkout"},
		{Ts: base.UnixMilli(), Level: "info", Service: "svc", Message: "b", Method: "POST", Path: "/cart"},
		{Ts: base.UnixMilli(), Level: "info", Service: "svc", Message: "c", Method: "GET", Path: "/checkout/confirm"},
	}
	if _, err := s.Ingest(entries); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	window := func() SearchParams {
		return SearchParams{Start: ptr(base.Add(-time.Minute)), End: ptr(base.Add(time.Minute)), Limit: 100}
	}

	check := func(label string) {
		p := window()
		p.Path = "/checkout"
		res, err := s.Search(p)
		if err != nil {
			t.Fatalf("%s search Path: %v", label, err)
		}
		if res.Total != 2 { // "/checkout" and "/checkout/confirm" (substring)
			t.Fatalf("%s: Path filter want 2, got %d", label, res.Total)
		}

		p = window()
		p.Method = "POST"
		res, err = s.Search(p)
		if err != nil {
			t.Fatalf("%s search Method: %v", label, err)
		}
		if res.Total != 1 {
			t.Fatalf("%s: Method filter want 1, got %d", label, res.Total)
		}
	}

	// Active-WAL path.
	check("active-wal")

	// Sealed path.
	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	check("sealed")
}
