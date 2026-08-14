package engine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

func mustIngest(t *testing.T, s *Store, entries ...chunk.Entry) {
	t.Helper()
	if _, err := s.Ingest(entries); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
}

// sealNow seals the current hour by advancing the injected clock one hour.
func sealNow(t *testing.T, s *Store, base time.Time) {
	t.Helper()
	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}
}

// TestDistinctValuesSeesActiveWALAndScopes: dict columns were answered purely
// from sealed segments' dictionaries, so a value that first appeared in the
// current hour was invisible for up to an hour; service/level/env were dropped
// entirely, so a watcher's COUNT DISTINCT spanned everything.
func TestDistinctValuesSeesActiveWALAndScopes(t *testing.T) {
	s, base := newClockedStore(t)
	ts := base.UnixMilli()
	mustIngest(t, s,
		chunk.Entry{Ts: ts, Level: "error", Service: "billing-v2", Env: "production", Message: "a", ErrorFingerprint: "fp-prod"},
		chunk.Entry{Ts: ts, Level: "info", Service: "billing-v2", Env: "production", Message: "b", ErrorFingerprint: "fp-info"},
		chunk.Entry{Ts: ts, Level: "error", Service: "legacy", Env: "staging", Message: "c", ErrorFingerprint: "fp-staging"},
	)
	start, end := base.Add(-time.Minute), base.Add(time.Minute)

	all, err := s.DistinctValues("service", start, end, "", "", "")
	if err != nil {
		t.Fatalf("DistinctValues: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("unsealed services must be visible: want 2, got %v", all)
	}

	prod, err := s.DistinctValues("service", start, end, "", "", "production")
	if err != nil {
		t.Fatalf("DistinctValues env: %v", err)
	}
	if len(prod) != 1 || prod[0] != "billing-v2" {
		t.Fatalf("env scope not applied: %v", prod)
	}

	// Level scope (the watcher's `level:error` distinct condition).
	fps, err := s.DistinctValues("error_fingerprint", start, end, "", "error", "")
	if err != nil {
		t.Fatalf("DistinctValues level: %v", err)
	}
	if len(fps) != 2 {
		t.Fatalf("level scope not applied: want 2 error fingerprints, got %v", fps)
	}

	svcFps, err := s.DistinctValues("error_fingerprint", start, end, "billing-v2", "error", "")
	if err != nil {
		t.Fatalf("DistinctValues service+level: %v", err)
	}
	if len(svcFps) != 1 || svcFps[0] != "fp-prod" {
		t.Fatalf("service+level scope not applied: %v", svcFps)
	}
}

// TestCountByServiceDetailedErrors: per-service error counts must be real, both
// live and after sealing (they were always reported as zero).
func TestCountByServiceDetailedErrors(t *testing.T) {
	s, base := newClockedStore(t)
	ts := base.UnixMilli()
	mustIngest(t, s,
		chunk.Entry{Ts: ts, Level: "info", Service: "api", Message: "ok"},
		chunk.Entry{Ts: ts, Level: "error", Service: "api", Message: "boom"},
		chunk.Entry{Ts: ts, Level: "fatal", Service: "api", Message: "worse"},
		chunk.Entry{Ts: ts, Level: "info", Service: "worker", Message: "ok"},
	)
	start, end := base.Add(-time.Minute), base.Add(time.Minute)

	check := func(stage string) {
		counts, err := s.CountByServiceDetailed(start, end, "")
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		if counts["api"].Total != 3 || counts["api"].Errors != 2 {
			t.Fatalf("%s: api counts = %+v, want total 3 errors 2", stage, counts["api"])
		}
		if counts["worker"].Errors != 0 {
			t.Fatalf("%s: worker errors = %d, want 0", stage, counts["worker"].Errors)
		}
	}
	check("live")
	sealNow(t, s, base)
	check("sealed")
}

// TestCountsUseEventTime: the unsealed count paths filtered on arrival time
// while everything else filtered on event time, so counts changed retroactively
// once an hour sealed.
func TestCountsUseEventTime(t *testing.T) {
	s, base := newClockedStore(t)
	// Buffered batch: arrives now, but the events happened five minutes later
	// in event-time terms (an SDK with a skewed clock).
	eventTs := base.Add(5 * time.Minute)
	mustIngest(t, s, chunk.Entry{Ts: eventTs.UnixMilli(), Level: "error", Service: "api", Message: "late"})

	start, end := base.Add(4*time.Minute), base.Add(6*time.Minute)
	counts, err := s.CountByLevel(start, end, "", "")
	if err != nil {
		t.Fatalf("CountByLevel: %v", err)
	}
	if counts["error"] != 1 {
		t.Fatalf("live count by event time: want 1, got %d", counts["error"])
	}

	sealNow(t, s, base)
	counts, err = s.CountByLevel(start, end, "", "")
	if err != nil {
		t.Fatalf("CountByLevel sealed: %v", err)
	}
	if counts["error"] != 1 {
		t.Fatalf("count must not change once the hour seals: want 1, got %d", counts["error"])
	}
}

// TestKindSurvivesSeal: kind was decoded with the sparse-string reader although
// it is a dict column, so it came back empty from every sealed chunk.
func TestKindSurvivesSeal(t *testing.T) {
	s, base := newClockedStore(t)
	mustIngest(t, s, chunk.Entry{Ts: base.UnixMilli(), Level: "info", Service: "api", Kind: "job", Message: "worked"})
	sealNow(t, s, base)

	start, end := base.Add(-time.Minute), base.Add(time.Minute)
	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(res.Entries))
	}
	if res.Entries[0].Kind != "job" {
		t.Fatalf("kind lost through seal: %q", res.Entries[0].Kind)
	}
}

// TestQuerySemanticsMatchAcrossSeal: the unsealed path used a raw substring
// match while sealed chunks used token-AND, so identical queries returned
// different rows either side of the seal boundary.
func TestQuerySemanticsMatchAcrossSeal(t *testing.T) {
	s, base := newClockedStore(t)
	mustIngest(t, s, chunk.Entry{Ts: base.UnixMilli(), Level: "error", Service: "api", Message: "timeout connecting to redis"})
	start, end := base.Add(-time.Minute), base.Add(time.Minute)

	count := func(q string) int {
		res, err := s.Search(SearchParams{Query: q, Start: &start, End: &end, Limit: 10})
		if err != nil {
			t.Fatalf("Search %q: %v", q, err)
		}
		return len(res.Entries)
	}

	live := count("redis timeout")
	if live != 1 {
		t.Fatalf("out-of-order tokens must match live: got %d", live)
	}
	sealNow(t, s, base)
	if sealed := count("redis timeout"); sealed != live {
		t.Fatalf("query result changed at the seal boundary: live %d, sealed %d", live, sealed)
	}
}

// TestMetadataFilterFilters: the metadata filter was accepted and dropped, so a
// narrowed search returned every row.
func TestMetadataFilterFilters(t *testing.T) {
	s, base := newClockedStore(t)
	body := func(host string) []byte {
		b, _ := json.Marshal(map[string]any{"metadata": map[string]any{"host": host}})
		return b
	}
	ts := base.UnixMilli()
	mustIngest(t, s,
		chunk.Entry{Ts: ts, Level: "info", Service: "api", Message: "one", Body: body("server-01")},
		chunk.Entry{Ts: ts, Level: "info", Service: "api", Message: "two", Body: body("server-02")},
		chunk.Entry{Ts: ts, Level: "info", Service: "api", Message: "three"},
	)
	start, end := base.Add(-time.Minute), base.Add(time.Minute)

	check := func(stage string) {
		res, err := s.Search(SearchParams{
			Start: &start, End: &end, Limit: 10,
			MetadataFilter: map[string]string{"host": "server-01"},
		})
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		if len(res.Entries) != 1 || res.Entries[0].Message != "one" {
			t.Fatalf("%s: metadata filter not applied, got %d entries", stage, len(res.Entries))
		}
	}
	check("live")
	sealNow(t, s, base)
	check("sealed")
}

// TestHistogramFilteredByService: the histogram ignored the service filter and
// returned every service's volume under the caller's label.
func TestHistogramFilteredByService(t *testing.T) {
	s, base := newClockedStore(t)
	ts := base.UnixMilli()
	mustIngest(t, s,
		chunk.Entry{Ts: ts, Level: "info", Service: "checkout", Message: "a"},
		chunk.Entry{Ts: ts, Level: "error", Service: "checkout", Message: "b"},
		chunk.Entry{Ts: ts, Level: "info", Service: "other", Message: "c"},
	)
	start, end := base.Add(-time.Minute), base.Add(time.Minute)

	total := func(f HistogramFilter) (int, int) {
		buckets, err := s.HistogramFiltered(start, end, time.Minute, f)
		if err != nil {
			t.Fatalf("HistogramFiltered: %v", err)
		}
		var n, errs int
		for _, b := range buckets {
			n += b.Total
			errs += b.Errors
		}
		return n, errs
	}

	if n, _ := total(HistogramFilter{}); n != 3 {
		t.Fatalf("unfiltered histogram: want 3, got %d", n)
	}
	n, errs := total(HistogramFilter{Service: "checkout"})
	if n != 2 || errs != 1 {
		t.Fatalf("service-filtered histogram: want 2 total / 1 error, got %d / %d", n, errs)
	}

	sealNow(t, s, base)
	if n, _ := total(HistogramFilter{Service: "checkout"}); n != 2 {
		t.Fatalf("sealed service-filtered histogram: want 2, got %d", n)
	}
}

// TestTailSnapshotAndSubscribeIsAtomic: a batch pushed between Snapshot and
// Subscribe used to land in neither and was lost from the tail forever.
func TestTailSnapshotAndSubscribeIsAtomic(t *testing.T) {
	for i := 0; i < 200; i++ {
		rb := NewRingBuffer()
		done := make(chan struct{})
		go func() {
			rb.Push([]chunk.Entry{{ID: 42, Message: "racy"}})
			close(done)
		}()
		snapshot, ch, unsub := rb.SnapshotAndSubscribe()
		<-done

		found := false
		for _, e := range snapshot {
			if e.ID == 42 {
				found = true
			}
		}
		if !found {
			select {
			case batch := <-ch:
				for _, e := range batch {
					if e.ID == 42 {
						found = true
					}
				}
			case <-time.After(time.Second):
			}
		}
		unsub()
		if !found {
			t.Fatalf("iteration %d: batch lost between snapshot and subscribe", i)
		}
	}
}
