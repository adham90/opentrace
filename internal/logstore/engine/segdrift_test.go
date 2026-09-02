package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

// TestRepeatedSealsInOneHourStayQueryable is the guard for a silent data-loss
// bug: sealing more than twice inside one wall-clock hour parked each new
// segment in the next free hour slot, and segment selection matched on that
// slot with only a ±1 hour buffer. From the third seal on, entries were written
// durably, acknowledged to the sender, and then matched no query at any window
// — including "last 7 days".
//
// A restart seals, so three restarts in an hour was enough to trigger it.
func TestRepeatedSealsInOneHourStayQueryable(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	const rounds = 5
	for i := range rounds {
		marker := fmt.Sprintf("segdrift-marker-%d", i)
		if _, err := s.Ingest([]chunk.Entry{{
			Ts:      now.UnixMilli(),
			Level:   "error",
			Service: "marker",
			Message: marker,
		}}); err != nil {
			t.Fatalf("round %d ingest: %v", i, err)
		}
		// Every seal lands in the next free hour slot, drifting further from the
		// timestamps it holds — exactly what a restart loop does.
		if err := s.SealCurrentHour(); err != nil {
			t.Fatalf("round %d seal: %v", i, err)
		}
	}

	start, end := now.Add(-15*time.Minute), now.Add(15*time.Minute)
	for i := range rounds {
		marker := fmt.Sprintf("segdrift-marker-%d", i)
		res, err := s.Search(SearchParams{
			Query: marker, Start: &start, End: &end, Limit: 10,
		})
		if err != nil {
			t.Fatalf("search for %s: %v", marker, err)
		}
		if len(res.Entries) == 0 {
			t.Errorf("%s was ingested and sealed but no time-ranged query can reach it "+
				"(seal #%d of %d in the same hour)", marker, i+1, rounds)
		}
	}
}
