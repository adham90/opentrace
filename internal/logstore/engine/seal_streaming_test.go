package engine

import (
	"bufio"
	"os"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/wal"
)

// Seal streams the rotated WAL a chunk at a time instead of loading the hour,
// which meant rewriting the stats pass as an accumulator. meta.json drives every
// count, histogram and distinct-value query that answers from precomputed
// totals, so an accumulator that disagrees with a whole-slice pass makes those
// answers quietly wrong. These tests pin it.
//
// Note on what is *not* tested here: the point of streaming is peak memory, and
// I could not measure that reliably in-process — HeapAlloc counts a seal's
// encoder garbage alongside its live data, and sampling it passed just as
// happily against a seal that buffered the whole hour. What is checked instead
// is the mechanism that bounds the working set (Scanner.NextBatch) and the
// correctness of the accumulator that streaming required.

// TestSealMetaMatchesWholeSlicePass compares the streamed meta against the same
// statistics computed directly from the entries, over a population spanning
// several chunks.
func TestSealMetaMatchesWholeSlicePass(t *testing.T) {
	if testing.Short() {
		t.Skip("seals a multi-chunk segment")
	}

	const n = chunkSize*2 + 1234 // three chunks, last one partial
	dir := t.TempDir()

	w, err := NewWALWriter(dir, NewRingBuffer())
	if err != nil {
		t.Fatalf("NewWALWriter: %v", err)
	}
	base := time.Unix(w.SegmentHour()*3600, 0).UTC()
	w.now = func() time.Time { return base }

	// Spread received_at across minutes so the histogram has real structure.
	var all []chunk.Entry
	const batch = 5000
	for start := 0; start < n; start += batch {
		size := min(batch, n-start)
		w.now = func() time.Time { return base.Add(time.Duration(start/batch) * time.Minute) }
		got, err := w.Append(benchEntries(base.Add(time.Duration(start)*time.Millisecond), size))
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		all = append(all, got...)
	}
	w.now = func() time.Time { return base.Add(time.Hour) }
	sealPath, sealHour, count, err := w.Rotate()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	w.Close()
	if count != n {
		t.Fatalf("rotated %d entries, want %d", count, n)
	}

	meta, err := Seal(sealPath, sealHour)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	want := referenceMeta(all)

	if meta.EntryCount != n {
		t.Errorf("EntryCount = %d, want %d", meta.EntryCount, n)
	}
	if meta.IDRange != [2]int64{all[0].ID, all[n-1].ID} {
		t.Errorf("IDRange = %v, want [%d %d]", meta.IDRange, all[0].ID, all[n-1].ID)
	}
	if got, want := len(meta.Chunks), 3; got != want {
		t.Fatalf("wrote %d chunks, want %d", got, want)
	}
	// Chunks must be contiguous and correctly indexed, or a decoded ID
	// addresses the wrong file.
	seen := 0
	for i, cm := range meta.Chunks {
		if cm.Index != i {
			t.Errorf("chunk %d reports index %d", i, cm.Index)
		}
		if cm.IDRange[0] != all[seen].ID {
			t.Errorf("chunk %d starts at ID %d, want %d", i, cm.IDRange[0], all[seen].ID)
		}
		seen += cm.EntryCount
	}
	if seen != n {
		t.Errorf("chunks cover %d entries, want %d", seen, n)
	}

	assertCountsEqual(t, "ByLevel", meta.Counts.ByLevel, want.ByLevel)
	assertCountsEqual(t, "ByService", meta.Counts.ByService, want.ByService)
	assertCountsEqual(t, "ByServiceError", meta.Counts.ByServiceError, want.ByServiceError)

	if len(meta.Histogram) != len(want.Histogram) {
		t.Errorf("histogram has %d minutes, want %d", len(meta.Histogram), len(want.Histogram))
	}
	histTotal := 0
	for key, got := range meta.Histogram {
		w := want.Histogram[key]
		if got.Total != w.Total || got.Errors != w.Errors {
			t.Errorf("histogram[%s] = %+v, want %+v", key, got, w)
		}
		histTotal += got.Total
	}
	if histTotal != n {
		t.Errorf("histogram totals %d entries, want %d", histTotal, n)
	}

	for _, col := range []string{"level", "service", "env", "event_type"} {
		gotSet := toSet(meta.Dicts[col])
		wantSet := want.Dicts[col]
		if len(gotSet) != len(wantSet) {
			t.Errorf("dict %q has %d values, want %d", col, len(gotSet), len(wantSet))
		}
		for v := range wantSet {
			if _, ok := gotSet[v]; !ok {
				t.Errorf("dict %q is missing %q", col, v)
			}
		}
	}
}

// TestSealedMultiChunkSegmentIsQueryable is the end-to-end check: after a
// streamed seal, every entry is searchable and individually addressable.
func TestSealedMultiChunkSegmentIsQueryable(t *testing.T) {
	if testing.Short() {
		t.Skip("seals a multi-chunk segment")
	}

	const n = chunkSize + 500 // two chunks
	s, base := newClockedStoreTB(t)
	for start := 0; start < n; start += 5000 {
		size := min(5000, n-start)
		if _, err := s.Ingest(benchEntries(base.Add(time.Duration(start)*time.Millisecond), size)); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	}
	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}

	start, end := base.Add(-time.Minute), base.Add(2*time.Hour)
	counts, err := s.CountByLevel(start, end, "", "")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	total := 0
	for _, c := range counts {
		total += c
	}
	if total != n {
		t.Fatalf("CountByLevel totals %d, want %d", total, n)
	}

	// Rows from the second chunk must be addressable too — that is what a
	// mis-indexed chunk would break.
	page, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 5, SortAsc: false})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Entries) != 5 {
		t.Fatalf("got %d entries, want 5", len(page.Entries))
	}
	for _, e := range page.Entries {
		got, err := s.GetByID(e.ID)
		if err != nil {
			t.Fatalf("GetByID(%d): %v", e.ID, err)
		}
		if got.Message != e.Message {
			t.Fatalf("GetByID(%d) = %q, want %q", e.ID, got.Message, e.Message)
		}
	}
}

// TestScannerNextBatchIsBounded pins the mechanism that keeps a seal's working
// set to one chunk: NextBatch must never return more than it was asked for,
// however much is left in the file.
func TestScannerNextBatchIsBounded(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWALWriter(dir, NewRingBuffer())
	if err != nil {
		t.Fatalf("NewWALWriter: %v", err)
	}
	base := time.Unix(w.SegmentHour()*3600, 0).UTC()
	w.now = func() time.Time { return base }
	if _, err := w.Append(benchEntries(base, 250)); err != nil {
		t.Fatalf("append: %v", err)
	}
	w.now = func() time.Time { return base.Add(time.Hour) }
	sealPath, _, _, err := w.Rotate()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	w.Close()

	f, err := os.Open(sealPath)
	if err != nil {
		t.Fatalf("open sealed WAL: %v", err)
	}
	defer f.Close()

	sc := wal.NewScanner(bufio.NewReaderSize(f, walReadBufferBytes))
	buf := make([]chunk.Entry, 0, 100)
	seen, batches := 0, 0
	for {
		buf = sc.NextBatch(buf[:0], 100)
		if len(buf) == 0 {
			break
		}
		if len(buf) > 100 {
			t.Fatalf("NextBatch returned %d entries for a limit of 100", len(buf))
		}
		seen += len(buf)
		batches++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if seen != 250 {
		t.Fatalf("scanned %d entries, want 250", seen)
	}
	if batches != 3 {
		t.Fatalf("took %d batches for 250 entries at 100 per batch, want 3", batches)
	}
}

// --- helpers ---

type refMeta struct {
	ByLevel        map[string]int
	ByService      map[string]int
	ByServiceError map[string]int
	Histogram      map[string]HBucket
	Dicts          map[string]map[string]struct{}
}

// referenceMeta recomputes the segment statistics the plain way — one pass over
// the whole slice — so the streamed accumulator has something to be wrong
// against.
func referenceMeta(entries []chunk.Entry) refMeta {
	r := refMeta{
		ByLevel:        map[string]int{},
		ByService:      map[string]int{},
		ByServiceError: map[string]int{},
		Histogram:      map[string]HBucket{},
		Dicts: map[string]map[string]struct{}{
			"level": {}, "service": {}, "env": {}, "event_type": {},
		},
	}
	for i := range entries {
		e := &entries[i]
		r.ByLevel[e.Level]++
		if e.Service != "" {
			r.ByService[e.Service]++
			if isErrorLevel(e.Level) {
				r.ByServiceError[e.Service]++
			}
		}
		key := time.UnixMilli(e.ReceivedAt).UTC().Format("2006-01-02T15:04")
		b := r.Histogram[key]
		b.Total++
		if isErrorLevel(e.Level) {
			b.Errors++
		}
		r.Histogram[key] = b

		r.Dicts["level"][e.Level] = struct{}{}
		if e.Service != "" {
			r.Dicts["service"][e.Service] = struct{}{}
		}
		if e.Env != "" {
			r.Dicts["env"][e.Env] = struct{}{}
		}
		if e.EventType != "" {
			r.Dicts["event_type"][e.EventType] = struct{}{}
		}
	}
	return r
}

func assertCountsEqual(t *testing.T, name string, got, want map[string]int) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s has %d keys, want %d", name, len(got), len(want))
	}
	for k, wv := range want {
		if got[k] != wv {
			t.Errorf("%s[%q] = %d, want %d", name, k, got[k], wv)
		}
	}
}

func toSet(vals []string) map[string]struct{} {
	out := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		out[v] = struct{}{}
	}
	return out
}
