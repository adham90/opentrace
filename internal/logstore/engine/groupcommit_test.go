package engine

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/wal"
)

// Append fsyncs outside the writer lock so concurrent appends can share one
// sync (group commit). That moves a durability boundary, so these tests pin the
// contract it must still honour:
//
//   - Append does not return until the caller's own bytes are on disk.
//   - Every record is intact and framed correctly, whatever the interleaving.
//   - IDs stay unique and dense, so the composite id -> (hour, chunk, row)
//     decoding still addresses the right row after a seal.

// TestConcurrentAppendsAreAllDurable is the core one: many writers append at
// once, and every entry each of them was told was written must be readable back
// out of the WAL file afterwards.
func TestConcurrentAppendsAreAllDurable(t *testing.T) {
	const (
		writers    = 16
		perWriter  = 8
		batchSize  = 10
		wantTotal  = writers * perWriter * batchSize
		messageFmt = "w%d-b%d-e%d"
	)

	s, base := newClockedStoreTB(t)

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for b := range perWriter {
				batch := make([]chunk.Entry, batchSize)
				for e := range batch {
					batch[e] = chunk.Entry{
						Ts:      base.UnixMilli(),
						Level:   "info",
						Service: "api",
						Message: fmt.Sprintf(messageFmt, w, b, e),
					}
				}
				if _, err := s.Ingest(batch); err != nil {
					errs <- fmt.Errorf("writer %d batch %d: %w", w, b, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ingest: %v", err)
	}

	// Read the WAL file straight off disk — not through the store's caches —
	// so this measures what actually survived an fsync, not what is in memory.
	f, err := os.Open(s.writer.WALPath())
	if err != nil {
		t.Fatalf("open WAL: %v", err)
	}
	defer f.Close()
	entries, err := wal.ReadEntries(f)
	if err != nil {
		t.Fatalf("WAL is not cleanly readable after concurrent appends: %v (parsed %d)", err, len(entries))
	}
	if len(entries) != wantTotal {
		t.Fatalf("WAL holds %d entries, but Append reported %d written", len(entries), wantTotal)
	}

	// Every message must appear exactly once: a lost record means a sync was
	// skipped, a duplicate means an offset was reused.
	seen := make(map[string]int, wantTotal)
	for i := range entries {
		seen[entries[i].Message]++
	}
	for w := range writers {
		for b := range perWriter {
			for e := range batchSize {
				msg := fmt.Sprintf(messageFmt, w, b, e)
				if n := seen[msg]; n != 1 {
					t.Fatalf("entry %q appears %d times in the WAL, want exactly 1", msg, n)
				}
			}
		}
	}

	// IDs must be unique and cover a dense range: the composite ID encodes the
	// row number a seal will write the entry to, so a gap or a collision
	// misaddresses rows in the sealed chunk.
	ids := make(map[int64]struct{}, len(entries))
	for i := range entries {
		if _, dup := ids[entries[i].ID]; dup {
			t.Fatalf("duplicate entry ID %d: two appends were assigned the same row", entries[i].ID)
		}
		ids[entries[i].ID] = struct{}{}
	}
	for row := range wantTotal {
		if _, ok := ids[IDForPosition(s.writer.SegmentHour(), row)]; !ok {
			t.Fatalf("row %d has no entry: the ID range is not dense", row)
		}
	}
}

// TestConcurrentAppendsSurviveSeal checks the same population end to end: after
// sealing, every concurrently appended entry is still searchable. This is what
// catches an ID that was unique in the WAL but decodes to the wrong chunk row.
func TestConcurrentAppendsSurviveSeal(t *testing.T) {
	const writers, perWriter = 8, 8

	s, base := newClockedStoreTB(t)

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for b := range perWriter {
				if _, err := s.Ingest([]chunk.Entry{{
					Ts: base.UnixMilli(), Level: "info", Service: "api",
					Message: fmt.Sprintf("w%d-b%d", w, b),
				}}); err != nil {
					t.Errorf("ingest: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}

	start, end := base.Add(-time.Minute), base.Add(2*time.Hour)
	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 500})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Entries) != writers*perWriter {
		t.Fatalf("after seal, search returns %d entries, want %d", len(res.Entries), writers*perWriter)
	}

	// Each entry must be individually addressable by its ID.
	for _, e := range res.Entries {
		got, err := s.GetByID(e.ID)
		if err != nil {
			t.Fatalf("GetByID(%d): %v", e.ID, err)
		}
		if got.Message != e.Message {
			t.Fatalf("GetByID(%d) returned %q, want %q — the ID addresses the wrong row",
				e.ID, got.Message, e.Message)
		}
	}
}

// TestAppendReturnsOnlyWhenDurable checks the ordering guarantee that makes
// group commit safe to use at all: by the time Append returns, the bytes are
// past the sync offset. A group commit that returned early would report data as
// written that a crash could still lose.
func TestAppendReturnsOnlyWhenDurable(t *testing.T) {
	s, base := newClockedStoreTB(t)

	for i := range 20 {
		batch := []chunk.Entry{{
			Ts: base.UnixMilli(), Level: "info", Service: "api",
			Message: fmt.Sprintf("entry-%d", i),
		}}
		if _, err := s.Ingest(batch); err != nil {
			t.Fatalf("ingest: %v", err)
		}
		written := s.writer.writtenBytes
		s.writer.syncMu.Lock()
		synced := s.writer.syncedBytes
		s.writer.syncMu.Unlock()
		if synced < written {
			t.Fatalf("Append returned with %d bytes written but only %d synced", written, synced)
		}
	}
}

// TestRotateFlushesUnsyncedTail covers the handoff: rotation renames the file
// out from under the writer, so anything a group commit has not reached yet has
// to be flushed first or it is lost before the seal ever reads it.
func TestRotateFlushesUnsyncedTail(t *testing.T) {
	s, base := newClockedStoreTB(t)
	mustIngest(t, s, chunk.Entry{Ts: base.UnixMilli(), Level: "info", Service: "api", Message: "before rotate"})

	// Simulate a group commit that has written but not yet synced.
	s.writer.mu.Lock()
	s.writer.syncMu.Lock()
	s.writer.syncedBytes = 0
	s.writer.syncMu.Unlock()
	s.writer.mu.Unlock()

	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}

	start, end := base.Add(-time.Minute), base.Add(2*time.Hour)
	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Message != "before rotate" {
		t.Fatalf("entry did not survive rotation: got %d entries", len(res.Entries))
	}
}

// BenchmarkIngestConcurrent is the load-ceiling guard. Append used to fsync
// while holding the writer lock, which pinned throughput flat regardless of how
// many writers were pushing — adding cores or SDK instances bought nothing.
// This benchmark exists so that shows up as a number: entries/s must rise with
// the writer count.
func BenchmarkIngestConcurrent(b *testing.B) {
	for _, writers := range []int{1, 4, 16, 64} {
		b.Run(fmt.Sprintf("writers%d", writers), func(b *testing.B) {
			s, base := newClockedStoreTB(b)
			template := benchEntries(base, 50)

			b.ResetTimer()
			began := time.Now()
			var wg sync.WaitGroup
			for range writers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range b.N {
						batch := make([]chunk.Entry, len(template))
						copy(batch, template)
						if _, err := s.Ingest(batch); err != nil {
							b.Error(err)
							return
						}
					}
				}()
			}
			wg.Wait()

			elapsed := time.Since(began)
			entries := float64(b.N * writers * len(template))
			b.ReportMetric(entries/elapsed.Seconds(), "entries/s")
		})
	}
}
