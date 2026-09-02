package engine

import "testing"

// TestCappedWALSearchStopsAtLimit is the guard for the last unbounded
// allocation a 256MB container hit.
//
// The aggregate scan paths collected every matching row of the live hour into
// one slice and applied their row cap afterwards, so the allocation the cap
// exists to refuse had already happened by the time it was applied. A
// chunk.Entry is ~600 bytes before its body, so the old fixed 500k-row cap was
// a ~300MB ceiling — more heap than the whole deployment had.
func TestCappedWALSearchStopsAtLimit(t *testing.T) {
	const available = 500
	s, base := benchStore(t, 0, available)
	start, end := benchRange(base)
	params := SearchParams{Start: &start, End: &end}

	for _, limit := range []int{0, 1, 37, 200} {
		got := searchWALsCapped(s.walCache, s.walPaths(), params, limit)
		if len(got) != limit {
			t.Errorf("limit %d collected %d entries from a live hour of %d, want exactly %d",
				limit, len(got), available, limit)
		}
	}

	// Above what is there, the cap simply stops binding.
	if got := searchWALsCapped(s.walCache, s.walPaths(), params, available+100); len(got) != available {
		t.Errorf("collected %d entries, want all %d", len(got), available)
	}
}

// TestWALScanHeadroomNeverExceedsTheCap pins the arithmetic the scan paths use
// to size the live-WAL leg: whatever the segments already contributed, the WAL
// may not push the total meaningfully past MaxScanRows.
func TestWALScanHeadroomNeverExceedsTheCap(t *testing.T) {
	prev := MaxScanRows
	MaxScanRows = 1000
	t.Cleanup(func() { MaxScanRows = prev })

	for _, have := range []int{0, 1, 999, 1000, 5000} {
		room := walScanHeadroom(have)
		if room < 0 {
			t.Errorf("have=%d gave negative headroom %d", have, room)
		}
		// +1 is deliberate, so callers can still detect overflow and mark the
		// result truncated; anything beyond that is the cap not binding. It can
		// only bound what the WAL leg adds — rows the segments already
		// contributed are the caller's to truncate.
		if total := have + room; have < MaxScanRows && total > MaxScanRows+1 {
			t.Errorf("have=%d headroom=%d allows %d rows, cap is %d", have, room, total, MaxScanRows)
		}
		if have >= MaxScanRows && room != 0 {
			t.Errorf("have=%d is already at the cap but headroom is %d", have, room)
		}
	}
}
