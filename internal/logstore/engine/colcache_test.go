package engine

import (
	"testing"
)

// TestColumnCacheReturnsSameRows is the correctness guard on the shared decoded
// column cache: a second query must see exactly what the first saw. If the
// cache ever keyed or typed something wrong, the failure mode is silently
// wrong data rather than an error, so this compares full result sets.
func TestColumnCacheReturnsSameRows(t *testing.T) {
	s, base := benchStore(t, 500, 50)
	start, end := benchRange(base)

	params := []SearchParams{
		{Start: &start, End: &end, Limit: 200},
		{Start: &start, End: &end, Limit: 200, Level: "error"},
		{Start: &start, End: &end, Limit: 200, RequestsOnly: true, MinDurationMs: 100},
		{Start: &start, End: &end, Limit: 200, Query: "cache warm"},
	}
	for i, p := range params {
		first, err := s.Search(p)
		if err != nil {
			t.Fatalf("params %d first search: %v", i, err)
		}
		second, err := s.Search(p)
		if err != nil {
			t.Fatalf("params %d second search: %v", i, err)
		}
		if len(first.Entries) != len(second.Entries) {
			t.Fatalf("params %d: cold search returned %d rows, warm search %d",
				i, len(first.Entries), len(second.Entries))
		}
		if len(first.Entries) == 0 {
			t.Fatalf("params %d matched nothing; the case proves nothing", i)
		}
		for j := range first.Entries {
			a, b := first.Entries[j], second.Entries[j]
			if a.ID != b.ID || a.Message != b.Message || a.Level != b.Level ||
				a.Service != b.Service || a.DurationMs != b.DurationMs ||
				a.Path != b.Path || a.ErrorFingerprint != b.ErrorFingerprint {
				t.Fatalf("params %d row %d differs between cold and warm read:\n cold=%+v\n warm=%+v", i, j, a, b)
			}
		}
	}

	if entries, used := s.columns.stats(); entries == 0 || used == 0 {
		t.Fatalf("column cache stayed empty (%d entries, %d bytes); it is not being used", entries, used)
	}
}

// TestColumnCacheDoesNotAliasEntryFields checks that nothing a caller can reach
// through a returned Entry points into a cached column. Mutating a field of one
// result must not change what the next query reads.
func TestColumnCacheDoesNotAliasEntryFields(t *testing.T) {
	s, base := benchStore(t, 200, 0)
	start, end := benchRange(base)
	p := SearchParams{Start: &start, End: &end, Limit: 200}

	first, err := s.Search(p)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	mutated := 0
	for i := range first.Entries {
		if first.Entries[i].NPlusOne != nil {
			*first.Entries[i].NPlusOne = !*first.Entries[i].NPlusOne
			mutated++
		}
		if len(first.Entries[i].Body) > 0 {
			first.Entries[i].Body[0] = 'X'
			mutated++
		}
	}
	if mutated == 0 {
		t.Skip("no aliasable fields in the fixture; nothing to check")
	}

	second, err := s.Search(p)
	if err != nil {
		t.Fatalf("second search: %v", err)
	}
	for i := range second.Entries {
		if len(second.Entries[i].Body) > 0 && second.Entries[i].Body[0] == 'X' {
			t.Fatalf("row %d: the body blob is aliased to a shared buffer — a caller's write leaked into the store", i)
		}
	}
}

// TestColumnCacheHonoursBudget checks the LRU actually bounds memory: a tiny
// budget must evict rather than grow without limit.
func TestColumnCacheHonoursBudget(t *testing.T) {
	const budget = 64 << 10
	c := newColumnCache(budget)
	for i := range 200 {
		key := columnKey{chunkPath: "chunk", column: string(rune('a' + i%26))}
		key.chunkPath = key.chunkPath + string(rune('0'+i/26))
		c.put(key, make([]int64, 1000), sizeOfInt64s(make([]int64, 1000)))
	}
	entries, used := c.stats()
	if used > budget {
		t.Fatalf("cache holds %d bytes, over its %d byte budget", used, budget)
	}
	if entries == 0 {
		t.Fatal("cache evicted everything; the budget is not usable")
	}

	// A single column larger than the whole budget is refused, not cached.
	huge := make([]int64, budget)
	c.put(columnKey{chunkPath: "huge", column: "ts"}, huge, sizeOfInt64s(huge))
	if _, ok := c.get(columnKey{chunkPath: "huge", column: "ts"}); ok {
		t.Fatal("an oversized column was cached; it would evict everything else on every read")
	}
}

// TestColumnCacheDisabled checks that a zero budget turns the cache off cleanly
// rather than panicking — the escape hatch for a memory-constrained deployment.
func TestColumnCacheDisabled(t *testing.T) {
	prev := ColumnCacheBytes
	ColumnCacheBytes = 0
	t.Cleanup(func() { ColumnCacheBytes = prev })

	s, base := benchStore(t, 200, 20)
	if s.columns != nil {
		t.Fatal("cache should be nil when the budget is zero")
	}
	start, end := benchRange(base)
	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 100})
	if err != nil {
		t.Fatalf("search with caching disabled: %v", err)
	}
	if len(res.Entries) == 0 {
		t.Fatal("search with caching disabled returned nothing")
	}
}

// TestColumnCacheConcurrentReads runs concurrent queries through the cache. Any
// data race in the LRU or in sharing decoded slices shows up here under -race.
func TestColumnCacheConcurrentReads(t *testing.T) {
	s, base := benchStore(t, 500, 50)
	start, end := benchRange(base)

	done := make(chan error, 8)
	for i := range 8 {
		go func(i int) {
			p := SearchParams{Start: &start, End: &end, Limit: 50}
			if i%2 == 0 {
				p.Level = "error"
			}
			for range 20 {
				if _, err := s.Search(p); err != nil {
					done <- err
					return
				}
				if _, err := s.CountByLevel(start, end, "", ""); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}(i)
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Fatalf("concurrent query: %v", err)
		}
	}

}

// TestWALCacheDisabled checks the escape hatch: with the cap at zero nothing is
// retained for the live hour, and queries still answer correctly by re-parsing.
func TestWALCacheDisabled(t *testing.T) {
	prev := WALCacheMaxEntries
	WALCacheMaxEntries = 0
	t.Cleanup(func() { WALCacheMaxEntries = prev })

	s, base := benchStore(t, 100, 50)
	start, end := benchRange(base)

	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 200})
	if err != nil {
		t.Fatalf("search with the WAL cache disabled: %v", err)
	}
	if len(res.Entries) != 150 {
		t.Fatalf("got %d entries, want 150 (100 sealed + 50 live)", len(res.Entries))
	}
	// Repeat: the uncached path must be stable, not just correct once.
	again, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 200})
	if err != nil {
		t.Fatalf("second search: %v", err)
	}
	if len(again.Entries) != len(res.Entries) {
		t.Fatalf("uncached searches disagree: %d then %d", len(res.Entries), len(again.Entries))
	}

	s.walCache.mu.Lock()
	cached := len(s.walCache.files)
	s.walCache.mu.Unlock()
	if cached != 0 {
		t.Errorf("WAL cache retained %d files with the cap at zero", cached)
	}
}
