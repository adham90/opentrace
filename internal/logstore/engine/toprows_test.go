package engine

import (
	"testing"
)

// needsPostReadFilter decides whether the top-N shortcut may run. Getting it
// wrong drops rows silently: the shortcut picks winners by timestamp before the
// filter that would have rejected them has been applied, so a query with such a
// filter would return fewer rows than exist.
func TestNeedsPostReadFilter(t *testing.T) {
	safe := []struct {
		name string
		p    SearchParams
	}{
		{"empty", SearchParams{}},
		{"column-indexed filters only", SearchParams{
			Level: "error", Service: "api", Env: "production",
			TraceID: "t1", RequestID: "r1", EventType: "http",
			ErrorClass: "Boom", ErrorFingerprint: "abc",
		}},
		// These are decided by applyScalarColumnFilters, so the shortcut is
		// safe. TestScalarFiltersMatchAcrossSeal is what proves they agree with
		// matchesParams; this only records that they are column-decided.
		{"scalar column filters", SearchParams{
			Method: "GET", Path: "/api", Handler: "OrdersController",
			TenantID: "t", SourceFile: "a.go", CommitHash: "abc",
			MinDurationMs: 100, PositiveDurationOnly: true, MinSQLCount: 2,
			NPlusOneOnly: true, RequestsOnly: true, SinceID: 5,
		}},
	}
	for _, tc := range safe {
		if needsPostReadFilter(tc.p) {
			t.Errorf("%s: reported as needing a post-read filter, which disables the shortcut unnecessarily", tc.name)
		}
	}

	// Only the body-dependent filters are left: they cannot be decided before
	// the row is materialized.
	unsafe := []struct {
		name string
		p    SearchParams
	}{
		{"metadata", SearchParams{MetadataFilter: map[string]string{"region": "eu"}}},
		{"exclude", SearchParams{Exclude: map[string]string{"path": "/health"}}},
	}
	for _, tc := range unsafe {
		if !needsPostReadFilter(tc.p) {
			t.Errorf("%s: not reported as a post-read filter — the shortcut would drop rows it should have kept", tc.name)
		}
	}
}

// topRowsByTs must return exactly the n newest (or oldest) rows, and return them
// in ascending row order so the caller's sequential read stays in order.
func TestTopRowsByTs_SelectsAndOrders(t *testing.T) {
	// Row i has timestamp ts[i]; deliberately not monotonic in row order, since
	// entries are written in arrival order and can be slightly out of sequence.
	ts := []int64{50, 10, 90, 30, 70}
	rows := []int{0, 1, 2, 3, 4}

	got := selectTopRows(ts, rows, false, 2) // newest 2 → ts 90 (row 2), 70 (row 4)
	want := []int{2, 4}
	if !equalInts(got, want) {
		t.Errorf("newest 2 = %v, want %v", got, want)
	}

	got = selectTopRows(ts, rows, true, 2) // oldest 2 → ts 10 (row 1), 30 (row 3)
	want = []int{1, 3}
	if !equalInts(got, want) {
		t.Errorf("oldest 2 = %v, want %v", got, want)
	}

	// Asking for more than exists returns everything, still row-ordered.
	got = selectTopRows(ts, rows, false, 99)
	if !equalInts(got, []int{0, 1, 2, 3, 4}) {
		t.Errorf("n > len = %v, want every row", got)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
