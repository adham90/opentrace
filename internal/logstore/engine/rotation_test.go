package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/ingest"
)

// TestHourBoundaryNoDataLossOrIDCollision reproduces the critical rotation bug:
// entries that arrive in a new hour BEFORE the hourly seal fires used to make
// Append auto-rotate the previous hour's WAL without sealing it (orphaning the
// whole hour) and desync segmentHour, causing ID collisions and segment
// overwrites on the next seal. With Append no longer rotating, all entries must
// survive, stay searchable across the boundary, and keep unique IDs.
func TestHourBoundaryNoDataLossOrIDCollision(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, nil, ingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	// Injectable clock pinned to the writer's actual starting hour boundary so
	// segment hours and Ts-based search windows stay aligned.
	base := time.Unix(s.writer.SegmentHour()*3600, 0).UTC()
	var mu sync.Mutex
	cur := base
	s.writer.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return cur }
	advance := func(d time.Duration) { mu.Lock(); cur = cur.Add(d); mu.Unlock() }

	mk := func(ts time.Time, msg string) chunk.Entry {
		return chunk.Entry{Ts: ts.UnixMilli(), Level: "info", Service: "svc", Env: "production", Message: msg}
	}

	// Hour H (10:00): three entries.
	if _, err := s.Ingest([]chunk.Entry{mk(base, "h0-a"), mk(base, "h0-b"), mk(base, "h0-c")}); err != nil {
		t.Fatalf("ingest H: %v", err)
	}

	// Cross into hour H+1 (11:00) and ingest two more BEFORE any seal — the
	// exact trigger for the old bug.
	advance(time.Hour)
	nextHour := base.Add(time.Hour)
	if _, err := s.Ingest([]chunk.Entry{mk(nextHour, "h1-a"), mk(nextHour, "h1-b")}); err != nil {
		t.Fatalf("ingest H+1 pre-seal: %v", err)
	}

	window := SearchParams{Start: ptr(base.Add(-time.Minute)), End: ptr(nextHour.Add(time.Minute)), Limit: 100}

	// Before sealing, all five must already be visible (no invisibility window).
	res, err := s.Search(window)
	if err != nil {
		t.Fatalf("search pre-seal: %v", err)
	}
	if res.Total != 5 {
		t.Fatalf("pre-seal: want 5 visible, got %d", res.Total)
	}

	// Now the hourly seal fires (we are in hour H+1, so H is complete).
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}

	// One more arrives in H+1 after the seal.
	if _, err := s.Ingest([]chunk.Entry{mk(nextHour, "h1-c")}); err != nil {
		t.Fatalf("ingest H+1 post-seal: %v", err)
	}

	// All six must be present, none lost or hidden, IDs unique.
	res, err = s.Search(window)
	if err != nil {
		t.Fatalf("search post-seal: %v", err)
	}
	if res.Total != 6 {
		t.Fatalf("post-seal: want 6 entries, got %d", res.Total)
	}
	seen := map[int64]string{}
	for _, e := range res.Entries {
		if prev, dup := seen[e.ID]; dup {
			t.Fatalf("duplicate ID %d for %q and %q", e.ID, prev, e.Message)
		}
		seen[e.ID] = e.Message
	}
	// The completed hour H must be a registered sealed segment.
	if s.segments.SegmentCount() != 1 {
		t.Fatalf("want 1 sealed segment for hour H, got %d", s.segments.SegmentCount())
	}
}

func ptr(t time.Time) *time.Time { return &t }
