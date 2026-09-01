package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/ingest"
)

// TestManySealsInOneHourStillOpens covers a restart loop turning itself into a
// permanent outage.
//
// Every seal that has data parks the next segment one hour slot further ahead
// of the clock, and the search for a writable slot gave up after 24. A service
// that restarted two dozen times in a day — a crash loop, a bad deploy being
// rolled forward — then failed to open its own store at every subsequent boot,
// exiting non-zero with all of its data intact and unreachable.
func TestManySealsInOneHourStillOpens(t *testing.T) {
	if testing.Short() {
		t.Skip("seals past the old 24-slot probe limit")
	}

	dir := t.TempDir()
	now := time.Now().UTC()

	// Past the old bound, so this fails outright on the previous behaviour.
	const seals = 30
	for i := range seals {
		s, err := NewStore(dir, nil, ingest.PIIConfig{})
		if err != nil {
			t.Fatalf("open store on iteration %d (a restart at this point is fatal): %v", i, err)
		}
		if _, err := s.Ingest([]chunk.Entry{{
			Ts:      now.UnixMilli(),
			Level:   "info",
			Service: "api",
			Message: fmt.Sprintf("slotmarker-%d", i),
		}}); err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
		// A graceful shutdown seals, which is what consumes the slot.
		if err := s.SealCurrentHour(); err != nil {
			t.Fatalf("seal %d: %v", i, err)
		}
		s.Close()
	}

	// The store must still open, and everything written across those seals must
	// still be reachable.
	s, err := NewStore(dir, nil, ingest.PIIConfig{})
	if err != nil {
		t.Fatalf("store will not open after %d seals: %v", seals, err)
	}
	t.Cleanup(func() { s.Close() })

	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	for i := range seals {
		marker := fmt.Sprintf("slotmarker-%d", i)
		res, err := s.Search(SearchParams{Query: marker, Start: &start, End: &end, Limit: 5})
		if err != nil {
			t.Fatalf("search %s: %v", marker, err)
		}
		if len(res.Entries) == 0 {
			t.Errorf("%s is unreachable after %d seals in one hour", marker, seals)
		}
	}
}
