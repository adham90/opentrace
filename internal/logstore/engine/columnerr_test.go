package engine

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	chunkpkg "github.com/adham90/opentrace/internal/logstore/chunk"
)

// sealedChunkColumns seals a handful of rows and returns a column cache over the
// resulting chunk, with the ts column poisoned as if its decode had failed.
// Poisoning the cache is the cheapest faithful stand-in for a corrupt ts column:
// memoColumn serves the recorded error to every reader of that column, exactly
// as a failing zstd/varint decode would.
func sealedChunkColumns(t *testing.T, poison error) (*chunkColumns, []int, time.Time) {
	t.Helper()
	s, base := newClockedStore(t)

	batch := make([]chunk.Entry, 5)
	for i := range batch {
		batch[i] = chunk.Entry{
			Ts:      base.Add(time.Duration(i) * time.Minute).UnixMilli(),
			Level:   "info",
			Service: "svc",
			Message: "m",
		}
	}
	if _, err := s.Ingest(batch); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}

	segs := s.segments.AllSegments()
	if len(segs) != 1 {
		t.Fatalf("want 1 sealed segment, got %d", len(segs))
	}
	r, err := chunkpkg.OpenReader(filepath.Join(segs[0].DirPath, "chunk_000.col"))
	if err != nil {
		t.Fatalf("open chunk: %v", err)
	}
	t.Cleanup(func() { r.Close() })

	cols := newChunkColumns(r)
	if poison != nil {
		cols.errs["ts"] = poison
	}
	rows := make([]int, r.EntryCount)
	for i := range rows {
		rows[i] = i
	}
	return cols, rows, base
}

// TestTimeFilterFailsLoudOnUnreadableTs: filterByTimeRange used to answer an
// unreadable ts column with "every row, unfiltered", so a corrupt chunk returned
// out-of-range rows presented as an in-range answer.
func TestTimeFilterFailsLoudOnUnreadableTs(t *testing.T) {
	boom := errors.New("corrupt ts column")
	cols, rows, base := sealedChunkColumns(t, boom)

	start := base.Add(-time.Hour)
	end := base.Add(-time.Minute) // excludes every row
	got, err := filterByTimeRange(cols, &start, &end, rows)
	if err == nil {
		t.Fatalf("want an error for an unreadable ts column, got %d rows", len(got))
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error must wrap the read failure, got %v", err)
	}
	if got != nil {
		t.Fatalf("no rows may be returned alongside the error, got %d", len(got))
	}

	// The same failure must reach the caller through applyColumnFilters.
	if _, err := applyColumnFilters(cols, SearchParams{Start: &start, End: &end}, rows); !errors.Is(err, boom) {
		t.Fatalf("applyColumnFilters swallowed the ts failure: %v", err)
	}
}

// TestTopRowsFailsLoudOnUnreadableTs: topRowsByTs used to return the row set
// unordered, so the caller presented an arbitrary slice as "the newest N".
func TestTopRowsFailsLoudOnUnreadableTs(t *testing.T) {
	boom := errors.New("corrupt ts column")
	cols, rows, _ := sealedChunkColumns(t, boom)

	got, err := topRowsByTs(cols, rows, false, 2)
	if err == nil {
		t.Fatalf("want an error for an unreadable ts column, got %d rows", len(got))
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error must wrap the read failure, got %v", err)
	}
}

// TestTimeFilterHealthyChunk keeps the happy path honest: a readable ts column
// still filters (and doesn't error).
func TestTimeFilterHealthyChunk(t *testing.T) {
	cols, rows, base := sealedChunkColumns(t, nil)
	start := base.Add(-time.Second)
	end := base.Add(2*time.Minute + time.Second)
	got, err := filterByTimeRange(cols, &start, &end, rows)
	if err != nil {
		t.Fatalf("filterByTimeRange: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want rows 0..2 in range, got %d", len(got))
	}
}

// TestChunkColumnsDecodeOnce is the guard for the quadratic-scan regression: the
// per-scan cache must decode each column exactly once no matter how many rows
// are read through it.
func TestChunkColumnsDecodeOnce(t *testing.T) {
	cols, rows, _ := sealedChunkColumns(t, nil)

	decodes := 0
	counting := func(name string) ([]int64, error) {
		decodes++
		return cols.r.ReadZstdInt64(name)
	}
	for range rows {
		if _, err := memoColumn(cols, cols.ints, "ts", counting, sizeOfInt64s); err != nil {
			t.Fatalf("memoColumn: %v", err)
		}
	}
	if decodes != 1 {
		t.Fatalf("column decoded %d times for %d rows; want exactly 1", decodes, len(rows))
	}
}
