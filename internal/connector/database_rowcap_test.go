package connector

import "testing"

// fakeRows implements the rowScanner subset of pgx.Rows so the row-cap logic
// can be exercised without a live database.
type fakeRows struct {
	total    int
	produced int
}

func (f *fakeRows) Next() bool {
	if f.produced >= f.total {
		return false
	}
	f.produced++
	return true
}

func (f *fakeRows) Values() ([]any, error) { return []any{f.produced}, nil }
func (f *fakeRows) Err() error             { return nil }

// Finding #6: ExecuteReadQuery must hard-cap materialized rows regardless of
// LIMIT detection, so a query that fools HasLimitGeneric can't load a whole
// table into memory.
func TestMaterializeRows_HardCap(t *testing.T) {
	f := &fakeRows{total: 10000}
	rows, err := materializeRows(f, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 500 {
		t.Errorf("materialized %d rows, want hard cap of 500", len(rows))
	}
	// It must stop pulling from the scanner once the cap is hit — proving the
	// full result set is never drained into memory.
	if f.produced > 501 {
		t.Errorf("scanner produced %d rows; cap should stop iteration near 500", f.produced)
	}
}

func TestMaterializeRows_UnderCapReturnsAll(t *testing.T) {
	f := &fakeRows{total: 3}
	rows, err := materializeRows(f, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("materialized %d rows, want 3", len(rows))
	}
}

func TestMaterializeRows_ZeroMeansNoCap(t *testing.T) {
	f := &fakeRows{total: 42}
	rows, err := materializeRows(f, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 42 {
		t.Errorf("materialized %d rows, want 42 (no cap)", len(rows))
	}
}
