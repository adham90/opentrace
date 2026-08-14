package engine

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/ingest"
	"github.com/adham90/opentrace/internal/logstore/wal"
)

// writeWAL writes entries to path using the WAL record framing, optionally
// appending extra raw bytes (used to simulate a torn or corrupt record).
func writeWAL(t *testing.T, path string, entries []chunk.Entry, trailer []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var buf []byte
	for i := range entries {
		buf = append(buf, wal.MarshalEntry(&entries[i])...)
	}
	buf = append(buf, trailer...)
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
}

func walEntries(hour int64, n int, msgPrefix string) []chunk.Entry {
	base := TimeFromSegmentHour(hour).Add(time.Minute)
	out := make([]chunk.Entry, n)
	for i := range out {
		out[i] = chunk.Entry{
			ID:         IDForPosition(hour, i),
			Ts:         base.Add(time.Duration(i) * time.Second).UnixMilli(),
			ReceivedAt: base.Add(time.Duration(i) * time.Second).UnixMilli(),
			Level:      "info",
			Service:    "svc",
			Env:        "production",
			Message:    msgPrefix,
		}
	}
	return out
}

// TestRestartAcrossHourBoundaryRecoversOrphanedWAL covers the case where the
// process stops in one hour and starts in a later one: the earlier hour's
// active.wal used to be sealed by nobody, read by nobody, and pruned by nobody,
// so its logs disappeared permanently and the directory leaked.
func TestRestartAcrossHourBoundaryRecoversOrphanedWAL(t *testing.T) {
	dir := t.TempDir()
	prevHour := SegmentHourFromTime(time.Now().UTC()) - 2
	prevDir := filepath.Join(dir, SegmentDirName(prevHour))
	entries := walEntries(prevHour, 3, "orphan")
	writeWAL(t, filepath.Join(prevDir, "active.wal"), entries, nil)

	s, err := NewStore(dir, nil, ingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	start := TimeFromSegmentHour(prevHour)
	end := start.Add(time.Hour)
	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 3 {
		t.Fatalf("orphaned hour: want 3 entries visible after restart, got %d", res.Total)
	}
	if s.segments.SegmentCount() != 1 {
		t.Fatalf("want the orphaned hour registered as a segment, got %d segments", s.segments.SegmentCount())
	}
	if _, err := os.Stat(filepath.Join(prevDir, "active.wal")); !os.IsNotExist(err) {
		t.Fatalf("orphaned active.wal should be consumed by the seal, stat err = %v", err)
	}

	// It must also be reclaimable by retention now that it is a real segment.
	deleted, err := s.Prune(time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("prune: want 1 segment deleted, got %d", deleted)
	}
}

// TestStartupToleratesTornTail: a power loss between Write and Sync leaves a
// partial trailing record. Startup used to hard-fail on it (server down), and
// appending after it would have hidden every later record from ReadEntries.
func TestStartupToleratesTornTail(t *testing.T) {
	dir := t.TempDir()
	hour := SegmentHourFromTime(time.Now().UTC())
	entries := walEntries(hour, 3, "good")
	torn := wal.MarshalEntry(&chunk.Entry{Ts: 1, Level: "info", Service: "svc", Message: "torn"})
	writeWAL(t, filepath.Join(dir, SegmentDirName(hour), "active.wal"), entries, torn[:len(torn)-5])

	s, err := NewStore(dir, nil, ingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore must tolerate a torn tail, got: %v", err)
	}
	defer s.Close()

	if got := s.writer.EntryCount(); got != 3 {
		t.Fatalf("replayed entry count: want 3, got %d", got)
	}

	// The valid prefix must still be searchable...
	start := TimeFromSegmentHour(hour)
	end := start.Add(time.Hour)
	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 3 {
		t.Fatalf("after torn tail: want 3 entries, got %d", res.Total)
	}

	// ...and new appends must land on a clean boundary, not after the tear.
	if _, err := s.Ingest([]chunk.Entry{{Ts: start.Add(time.Minute).UnixMilli(), Level: "info", Service: "svc", Message: "after"}}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	f, err := os.Open(s.writer.WALPath())
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	defer f.Close()
	replayed, err := wal.ReadEntries(f)
	if err != nil {
		t.Fatalf("WAL must be cleanly readable after recovery: %v", err)
	}
	if len(replayed) != 4 {
		t.Fatalf("want 4 records after recovery+append, got %d", len(replayed))
	}
}

// TestSealKeepsPrefixOnCorruptRecord: one unparseable record in the middle of an
// hour used to abort the whole seal, permanently hiding every valid entry
// before it and failing identically on every restart.
func TestSealKeepsPrefixOnCorruptRecord(t *testing.T) {
	dir := t.TempDir()
	hour := SegmentHourFromTime(time.Now().UTC()) - 1
	segDir := filepath.Join(dir, SegmentDirName(hour))
	entries := walEntries(hour, 3, "good")

	// A record whose length prefix is plausible but whose payload is garbage.
	garbage := make([]byte, 4+64)
	garbage[0] = 64
	for i := 4; i < len(garbage); i++ {
		garbage[i] = 0xFF
	}
	walPath := filepath.Join(segDir, "sealing_"+SegmentDirName(hour)+".wal")
	writeWAL(t, walPath, entries, garbage)

	meta, err := Seal(walPath, hour)
	if err != nil {
		t.Fatalf("Seal must not abort on a corrupt record: %v", err)
	}
	if meta == nil || meta.EntryCount != 3 {
		t.Fatalf("want the 3-entry valid prefix sealed, got %+v", meta)
	}
	if !IsSealComplete(segDir) {
		t.Fatal("seal marker missing: the hour would be re-sealed and lost forever")
	}
}

// TestRotateAdvancesHourWhenEmpty: an idle gap used to leave segmentHour parked
// on the last hour with traffic, filing later entries under a stale hour that
// SegmentsInRange excluded once sealed.
func TestRotateAdvancesHourWhenEmpty(t *testing.T) {
	s, base := newClockedStore(t)
	startHour := s.writer.SegmentHour()

	// Three fully idle hours: the seal ticker fires on an empty WAL each time.
	for i := 1; i <= 3; i++ {
		at := base.Add(time.Duration(i) * time.Hour)
		s.writer.now = func() time.Time { return at }
		if err := s.SealCurrentHour(); err != nil {
			t.Fatalf("seal tick %d: %v", i, err)
		}
	}
	if got := s.writer.SegmentHour(); got != startHour+3 {
		t.Fatalf("segment hour after idle gap: want %d, got %d", startHour+3, got)
	}

	// An entry arriving now must stay searchable at its real timestamp.
	active := base.Add(3 * time.Hour).Add(15 * time.Minute)
	if _, err := s.Ingest([]chunk.Entry{{Ts: active.UnixMilli(), Level: "error", Service: "svc", Message: "burst"}}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	sealAt := base.Add(4 * time.Hour)
	s.writer.now = func() time.Time { return sealAt }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}

	start := base.Add(3 * time.Hour)
	end := start.Add(time.Hour)
	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 1 {
		t.Fatalf("post-idle entry must remain searchable in its own hour, got %d", res.Total)
	}
}

// TestFailedSealKeepsHourQueryableAndRetries: between rotation and registration
// the hour used to be in neither place a query looks, and a failed seal was
// never retried while the process lived.
func TestFailedSealKeepsHourQueryableAndRetries(t *testing.T) {
	s, base := newClockedStore(t)
	if _, err := s.Ingest([]chunk.Entry{{Ts: base.UnixMilli(), Level: "info", Service: "svc", Message: "hello"}}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	segDir := filepath.Join(s.dataDir, SegmentDirName(s.writer.SegmentHour()))

	// Make meta.json unwritable so the seal fails after rotation.
	blocker := filepath.Join(segDir, "meta.json")
	if err := os.Mkdir(blocker, 0o755); err != nil {
		t.Fatalf("mkdir blocker: %v", err)
	}

	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err == nil {
		t.Fatal("expected the seal to fail")
	}

	start := base.Add(-time.Minute)
	end := base.Add(time.Minute)
	res, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 1 {
		t.Fatalf("rotated hour must stay queryable during/after a failed seal, got %d", res.Total)
	}

	// Unblock and let the next seal tick retry it.
	if err := os.Remove(blocker); err != nil {
		t.Fatalf("remove blocker: %v", err)
	}
	s.writer.now = func() time.Time { return base.Add(2 * time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("retry seal: %v", err)
	}
	if s.segments.SegmentCount() != 1 {
		t.Fatalf("want the retried seal registered, got %d segments", s.segments.SegmentCount())
	}
	res, err = s.Search(SearchParams{Start: &start, End: &end, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 1 {
		t.Fatalf("after retry the entry must appear exactly once, got %d", res.Total)
	}
}

// TestSegmentFullSealsEarly: past 12.8M entries in one hour the chunk number
// used to carry into the segment-hour bits, so IDs decoded to another hour.
func TestSegmentFullSealsEarly(t *testing.T) {
	s, base := newClockedStore(t)
	if _, err := s.Ingest([]chunk.Entry{{Ts: base.UnixMilli(), Level: "info", Service: "svc", Message: "first"}}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	hour := s.writer.SegmentHour()
	atomic.StoreInt64(&s.writer.entryCount, MaxEntriesPerSegment)

	written, err := s.Ingest([]chunk.Entry{{Ts: base.UnixMilli(), Level: "info", Service: "svc", Message: "overflow"}})
	if err != nil {
		t.Fatalf("Ingest past segment capacity: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("want 1 entry written, got %d", len(written))
	}
	gotHour, _, _ := DecodeID(written[0].ID)
	if gotHour == hour {
		t.Fatal("expected the writer to have moved to a fresh segment hour")
	}
	if gotHour != s.writer.SegmentHour() {
		t.Fatalf("ID hour %d must match the writer's segment hour %d", gotHour, s.writer.SegmentHour())
	}
}

// TestEncodeIDMasksOverflow guards the raw encoder: an out-of-range chunk must
// never bleed into the hour field.
func TestEncodeIDMasksOverflow(t *testing.T) {
	id := EncodeID(1000, maxChunksPerSegment, 0)
	hour, chunkNum, row := DecodeID(id)
	if hour != 1000 || chunkNum != 0 || row != 0 {
		t.Fatalf("overflowing chunk corrupted the ID: hour=%d chunk=%d row=%d", hour, chunkNum, row)
	}
}

// errInjectedWrite is the failure a shortWriteFile reports once its byte budget
// is spent.
var errInjectedWrite = errors.New("injected write failure")

// shortWriteFile accepts a fixed number of bytes, persists them, and then fails
// every write. This is the real fault the rollback guards against: a torn write
// that leaves orphan bytes at the end of the WAL, after which O_APPEND would
// frame the next record behind garbage and mis-frame everything from there on.
//
// The previous version of this test used a read-only fd, which fails with n=0
// and never writes a partial record — so it passed with both rollback calls
// deleted.
type shortWriteFile struct {
	*os.File
	budget  int // bytes still accepted before writes start failing
	written int // bytes actually persisted through this handle
}

func (f *shortWriteFile) Write(p []byte) (int, error) {
	if f.budget <= 0 {
		return 0, errInjectedWrite
	}
	if len(p) <= f.budget {
		n, err := f.File.Write(p)
		f.budget -= n
		f.written += n
		return n, err
	}
	n, err := f.File.Write(p[:f.budget])
	f.budget = 0
	f.written += n
	if err != nil {
		return n, err
	}
	return n, errInjectedWrite // short write, then failure
}

// TestAppendFailureDoesNotCorruptWAL: a failed write must not leave bytes that
// mis-frame every later record.
func TestAppendFailureDoesNotCorruptWAL(t *testing.T) {
	s, base := newClockedStore(t)
	if _, err := s.Ingest([]chunk.Entry{{Ts: base.UnixMilli(), Level: "info", Service: "svc", Message: "one"}}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	walPath := s.writer.WALPath()
	sizeBefore := walFileSize(t, walPath)

	// Inject a handle that tears the next record in half: some bytes land on
	// disk, then the write fails.
	good := s.writer.walFile
	const partialBytes = 12
	faulty := &shortWriteFile{File: good.(*os.File), budget: partialBytes}
	s.writer.walFile = faulty
	if _, err := s.Ingest([]chunk.Entry{{Ts: base.UnixMilli(), Level: "info", Service: "svc", Message: "fails"}}); err == nil {
		t.Fatal("expected the append to fail")
	}
	s.writer.walFile = good
	if faulty.written != partialBytes {
		t.Fatalf("fault injection wrote %d bytes; the torn-write case is not being exercised", faulty.written)
	}

	if got := walFileSize(t, walPath); got != sizeBefore {
		t.Fatalf("failed append changed the WAL size: %d -> %d", sizeBefore, got)
	}
	if _, err := s.Ingest([]chunk.Entry{{Ts: base.UnixMilli(), Level: "info", Service: "svc", Message: "two"}}); err != nil {
		t.Fatalf("Ingest after failure: %v", err)
	}
	f, err := os.Open(walPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	entries, err := wal.ReadEntries(f)
	if err != nil {
		t.Fatalf("WAL must still be cleanly framed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 records, got %d", len(entries))
	}
}

func walFileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return fi.Size()
}

// A graceful restart WITHIN the same wall-clock hour must not destroy what the
// previous run sealed. Shutdown seals mid-hour; Rotate then advances past the
// sealed hour in memory only, so a restart that recomputed the hour from the
// clock reopened the sealed directory with entryCount reset to 0 — handing out
// IDs that collided with the sealed chunk's, and making the next seal overwrite
// chunk_000 in place. One ordinary restart wiped an entire seeded store.
func TestSameHourRestartKeepsSealedSegment(t *testing.T) {
	dir := t.TempDir()

	ingestOne := func(s *Store, msg string) chunk.Entry {
		t.Helper()
		now := time.Now().UTC()
		got, err := s.Ingest([]chunk.Entry{{
			Ts:         now.UnixMilli(),
			ReceivedAt: now.UnixMilli(),
			Level:      "info",
			Service:    "svc",
			Env:        "production",
			Message:    msg,
		}})
		if err != nil {
			t.Fatalf("Ingest(%s): %v", msg, err)
		}
		return got[0]
	}

	// First run: ingest ENTRY-A, then seal on shutdown.
	s1, err := NewStore(dir, nil, ingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore 1: %v", err)
	}
	entryA := ingestOne(s1, "ENTRY-A")
	if err := s1.SealCurrentHour(); err != nil {
		t.Fatalf("seal on shutdown: %v", err)
	}
	s1.Close()

	// Second run, same hour: ingest ENTRY-B and seal again.
	s2, err := NewStore(dir, nil, ingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore 2: %v", err)
	}
	entryB := ingestOne(s2, "ENTRY-B")
	if entryA.ID == entryB.ID {
		t.Fatalf("duplicate id across a same-hour restart: both entries got %d", entryA.ID)
	}
	if err := s2.SealCurrentHour(); err != nil {
		t.Fatalf("second seal: %v", err)
	}
	s2.Close()

	// Third run: both entries must still be readable.
	s3, err := NewStore(dir, nil, ingest.PIIConfig{})
	if err != nil {
		t.Fatalf("NewStore 3: %v", err)
	}
	defer s3.Close()

	start := time.Now().UTC().Add(-2 * time.Hour)
	end := time.Now().UTC().Add(2 * time.Hour)
	res, err := s3.Search(SearchParams{Start: &start, End: &end, Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	seen := map[string]bool{}
	for _, e := range res.Entries {
		seen[e.Message] = true
	}
	if !seen["ENTRY-A"] {
		t.Errorf("ENTRY-A was destroyed by the same-hour restart; visible messages: %v", seen)
	}
	if !seen["ENTRY-B"] {
		t.Errorf("ENTRY-B missing; visible messages: %v", seen)
	}
}
