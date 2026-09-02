package engine

import (
	"runtime"
	"testing"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

// TestOversizedWALSearchDoesNotMaterializeTheHour is the guard for the failure
// that OOM-killed a 256MB container: a live WAL far larger than the cache
// budget was read into one slice on every query, so a search returning 50 rows
// allocated the whole hour. The budget bounded what was *kept*, not what was
// *read*.
//
// The assertion is on allocation rather than wall clock: it is deterministic and
// does not depend on the machine. A streaming scan is flat in the population; a
// materializing one is linear, which is the regression this catches.
// TestStreamedWALScanDoesNotRetainTheHour is the guard for the failure that
// OOM-killed a 256MB container: a live WAL far larger than the cache budget was
// read into one slice on every query, so a search returning 50 rows held the
// whole hour live while it ran. The budget bounded what was *kept* between
// queries, not what was *live during* one.
//
// Total allocation is linear either way — every record still has to be decoded —
// so the assertion is on heap still live (after a forced GC) at the end of the
// scan, which is the quantity a container memory limit is applied to.
func TestStreamedWALScanDoesNotRetainTheHour(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two live-WAL populations")
	}

	// A budget of one byte is never satisfiable, so the scan takes the
	// uncacheable path — the same one a real 90MB hour takes against a 16MB
	// budget, without building 90MB here.
	prevBytes := WALCacheBytes
	WALCacheBytes = 1
	t.Cleanup(func() { WALCacheBytes = prevBytes })

	// liveHeapAtEndOfScan walks every record and reports the heap still live when
	// the last one is handed over, holding nothing itself.
	liveHeapAtEndOfScan := func(live int) uint64 {
		s, _ := benchStore(t, 0, live)
		paths := s.walPaths()

		seen, peak := 0, uint64(0)
		var ms runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&ms)
		base := ms.HeapAlloc

		forEachWALEntry(s.walCache, paths, func(e *chunk.Entry) bool {
			seen++
			if seen == live { // the last record: a materializing scan holds them all
				// GC first: HeapAlloc otherwise counts the per-record garbage a
				// streaming scan produces and drops, which grows with the
				// population either way. What matters is what is still *live*.
				runtime.GC()
				runtime.ReadMemStats(&ms)
				if ms.HeapAlloc > base {
					peak = ms.HeapAlloc - base
				}
			}
			return true
		})
		if seen != live {
			t.Fatalf("scanned %d entries, wrote %d", seen, live)
		}
		return peak
	}

	small := liveHeapAtEndOfScan(2000)
	large := liveHeapAtEndOfScan(8000)

	// 4x the records in the live hour. A streaming scan holds one entry at a
	// time, so its live heap is flat; a materializing one holds every entry, so
	// it grows with the population. 2.5 sits clear of both.
	if small == 0 {
		small = 1
	}
	ratio := float64(large) / float64(small)
	if ratio > 2.5 {
		t.Errorf("live heap during a WAL scan grew %.1fx for 4x the records (%d -> %d bytes); "+
			"the WAL is being materialized instead of streamed", ratio, small, large)
	}
}

// TestOversizedWALSearchStillCorrect pins the behaviour the streaming path must
// preserve: the same answer as the cached path, and no entry bleeding into the
// next through the reused scan buffer.
func TestOversizedWALSearchStillCorrect(t *testing.T) {
	s, base := benchStore(t, 0, 300)
	start, end := benchRange(base)
	params := SearchParams{Start: &start, End: &end, Limit: 300}

	cached, err := s.Search(params)
	if err != nil {
		t.Fatalf("cached search: %v", err)
	}

	prevBytes := WALCacheBytes
	WALCacheBytes = 1
	t.Cleanup(func() { WALCacheBytes = prevBytes })

	streamed, err := s.Search(params)
	if err != nil {
		t.Fatalf("streamed search: %v", err)
	}

	if len(streamed.Entries) != len(cached.Entries) {
		t.Fatalf("streamed search returned %d entries, cached returned %d",
			len(streamed.Entries), len(cached.Entries))
	}
	for i := range cached.Entries {
		c, st := cached.Entries[i], streamed.Entries[i]
		if c.ID != st.ID || c.Ts != st.Ts || c.Message != st.Message ||
			c.Service != st.Service || c.Level != st.Level {
			t.Fatalf("row %d differs between cached and streamed scans:\n cached=%+v\n streamed=%+v", i, c, st)
		}
		if string(c.Body) != string(st.Body) {
			t.Fatalf("row %d body differs: cached=%q streamed=%q", i, c.Body, st.Body)
		}
	}
}
