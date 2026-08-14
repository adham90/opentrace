package engine

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/wal"
)

// ErrSegmentFull is returned by Append when the current segment hour has no
// room left in the composite-ID layout. The Store handles it by sealing early
// and retrying into a fresh segment.
var ErrSegmentFull = errors.New("segment hour is full")

// walFileHandle is the subset of *os.File the WAL writer uses. It is an
// interface so tests can inject a handle that writes some bytes and *then*
// fails: that is the case rollback exists for, and a read-only fd (which fails
// with n=0, leaving nothing behind) never exercises it.
type walFileHandle interface {
	io.Writer
	Sync() error
	Truncate(size int64) error
	Close() error
}

// WALWriter handles appending entries to the active WAL file.
// Thread-safe: multiple goroutines can call Append concurrently.
type WALWriter struct {
	mu sync.Mutex

	dataDir     string
	segmentHour int64 // current segment hour
	walFile     walFileHandle
	entryCount  int64 // entries written this hour (atomic read for ID computation)

	// validBytes is the number of bytes safely fsynced in the WAL.
	// Readers snapshot this value before reading.
	validBytes int64

	// fileSize tracks the last-known-good size of the WAL file (guarded by mu).
	// A failed/partial write is rolled back to this offset so the next append
	// can't be framed after orphan bytes.
	fileSize int64

	// now returns the current UTC time. Overridable in tests to exercise
	// hour-boundary rotation deterministically.
	now func() time.Time

	ring *RingBuffer
}

// NewWALWriter creates a new WAL writer.
// dataDir is the base directory (e.g., "data/logs").
// It opens or creates the WAL file for the current hour.
func NewWALWriter(dataDir string, ring *RingBuffer) (*WALWriter, error) {
	w := &WALWriter{
		dataDir: dataDir,
		ring:    ring,
		now:     func() time.Time { return time.Now().UTC() },
	}
	segHour, err := freeSegmentHour(dataDir, SegmentHourFromTime(w.now()))
	if err != nil {
		return nil, err
	}

	if err := w.ensureWAL(segHour); err != nil {
		return nil, err
	}

	return w, nil
}

// maxSealedHourProbe bounds the search for a writable segment hour. Each step
// is one sealed hour ahead of the clock, which only accumulates from repeated
// forced seals inside a single hour; a full day of them is already pathological.
const maxSealedHourProbe = 24

// freeSegmentHour returns the first segment hour at or after start whose
// directory has not already been sealed.
//
// Rotate advances past a sealed hour in memory (nextHour = sealHour+1) so a
// forced mid-hour seal doesn't reuse the directory it just finished. That
// choice has to survive a restart: recomputing the hour from the wall clock
// reopens the sealed directory with entryCount reset to 0, which hands out IDs
// that collide with the sealed chunk's and makes the next Seal overwrite
// chunk_000 in place — destroying every entry already sealed there. A graceful
// restart inside one hour was enough to lose an entire seeded store.
func freeSegmentHour(dataDir string, start int64) (int64, error) {
	for hour := start; hour < start+maxSealedHourProbe; hour++ {
		if !IsSealComplete(filepath.Join(dataDir, SegmentDirName(hour))) {
			return hour, nil
		}
	}
	return 0, fmt.Errorf("no unsealed segment hour in %d hours from %s",
		maxSealedHourProbe, SegmentDirName(start))
}

// Append writes a batch of entries to the WAL, assigns IDs, and notifies tail subscribers.
// Returns the assigned entries (with IDs populated).
func (w *WALWriter) Append(entries []chunk.Entry) ([]chunk.Entry, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.now()

	// NOTE: Append never rotates. Rotation+seal is driven exclusively by the
	// Store's sealing authority (the hourly seal + the engine's background
	// sealer), which always seals the file it rotates. A previous version
	// auto-rotated here without sealing, which orphaned whole hours and — by
	// desyncing segmentHour from the ticker — caused ID collisions and segment
	// overwrites. Entries that arrive in the first moments of a new hour before
	// the sealer runs land in the still-open previous-hour WAL; they carry the
	// correct Ts and remain searchable, then move into the sealed segment.

	// Assign IDs and received_at
	receivedAt := now.UnixMilli()
	startIdx := int(atomic.LoadInt64(&w.entryCount))

	// Refuse to encode IDs that would overflow the chunk field into the
	// segment-hour bits. The Store turns this into a forced seal + retry.
	if !SegmentHasRoom(startIdx, len(entries)) {
		return nil, ErrSegmentFull
	}

	for i := range entries {
		entries[i].ID = IDForPosition(w.segmentHour, startIdx+i)
		entries[i].ReceivedAt = receivedAt
	}

	// Serialize and write. A short or failed write leaves partial bytes at the
	// end of the file; O_APPEND would then frame the next record after that
	// garbage and mis-frame every record from there on, so roll the file back
	// to the last-known-good offset before returning the error.
	lastGood := w.fileSize
	written := int64(0)
	for i := range entries {
		data := wal.MarshalEntry(&entries[i])
		normalizeForRing(&entries[i], data)
		n, err := w.walFile.Write(data)
		written += int64(n)
		if err != nil {
			w.rollback(lastGood)
			return nil, fmt.Errorf("write WAL entry: %w", err)
		}
	}

	// Fsync the batch
	if err := w.walFile.Sync(); err != nil {
		w.rollback(lastGood)
		return nil, fmt.Errorf("fsync WAL: %w", err)
	}
	w.fileSize = lastGood + written

	// Update counters
	newCount := int64(startIdx + len(entries))
	atomic.StoreInt64(&w.entryCount, newCount)

	// Update valid bytes for concurrent readers. Use the tracked offset rather
	// than Stat(): Stat would report any orphan bytes left by a failed write as
	// valid.
	atomic.StoreInt64(&w.validBytes, w.fileSize)

	// Push to ring buffer (outside lock is fine — ring has its own sync)
	w.ring.Push(entries)

	return entries, nil
}

// SegmentHour returns the current segment hour.
func (w *WALWriter) SegmentHour() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.segmentHour
}

// EntryCount returns the number of entries in the current WAL.
func (w *WALWriter) EntryCount() int {
	return int(atomic.LoadInt64(&w.entryCount))
}

// ValidBytes returns the number of safely fsynced bytes (safe for readers).
func (w *WALWriter) ValidBytes() int64 {
	return atomic.LoadInt64(&w.validBytes)
}

// WALPath returns the path to the current active WAL file.
func (w *WALWriter) WALPath() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.walPath(w.segmentHour)
}

// Rotate forces a WAL rotation (used by seal trigger). Returns the path to the
// old WAL file (now named sealing_T*.wal) and the segment hour it belongs to.
func (w *WALWriter) Rotate() (sealingPath string, sealHour int64, entryCount int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	sealHour = w.segmentHour
	entryCount = int(atomic.LoadInt64(&w.entryCount))

	nextHour := SegmentHourFromTime(w.now())

	if entryCount == 0 {
		// Nothing to seal — but the writer must still follow the clock. Leaving
		// segmentHour parked on the last hour that had traffic meant entries
		// arriving after an idle gap were filed under a stale hour: once sealed,
		// SegmentsInRange (±1h) no longer matched their real timestamps and they
		// vanished from every time-ranged query.
		if nextHour > sealHour {
			if err = w.advanceEmpty(nextHour); err != nil {
				return "", sealHour, 0, err
			}
		}
		return "", sealHour, 0, nil
	}

	if nextHour <= sealHour {
		nextHour = sealHour + 1 // force rotation even within same hour (explicit/forced seals)
	}
	// Skip hours an earlier run already sealed, for the same reason startup
	// does: writing into a sealed directory collides IDs and the next seal
	// overwrites the chunk already there.
	if nextHour, err = freeSegmentHour(w.dataDir, nextHour); err != nil {
		return "", sealHour, 0, err
	}

	if err = w.rotate(nextHour); err != nil {
		return "", sealHour, 0, err
	}

	sealingPath = w.sealingPath(sealHour)
	return sealingPath, sealHour, entryCount, nil
}

// Close closes the WAL file.
func (w *WALWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.walFile != nil {
		return w.walFile.Close()
	}
	return nil
}

// --- internal ---

func (w *WALWriter) walPath(hour int64) string {
	dir := filepath.Join(w.dataDir, SegmentDirName(hour))
	return filepath.Join(dir, "active.wal")
}

func (w *WALWriter) sealingPath(hour int64) string {
	dir := filepath.Join(w.dataDir, SegmentDirName(hour))
	return filepath.Join(dir, fmt.Sprintf("sealing_%s.wal", SegmentDirName(hour)))
}

// normalizeForRing replaces an entry with what the WAL actually stored, so the
// live tail (which serves the in-memory entry) and a post-seal search (which
// serves the stored one) can't disagree. The WAL truncates any field over
// wal.MaxFieldBytes and drops an oversized body, so only records at or above
// that size can differ — the round-trip is skipped for everything else, which
// is the entire hot path.
func normalizeForRing(e *chunk.Entry, record []byte) {
	if len(record) < wal.MaxFieldBytes && len(e.Body) < wal.MaxFieldBytes {
		return
	}
	stored, err := wal.UnmarshalEntry(record[walLenPrefixBytes:])
	if err != nil {
		slog.Warn("wal: cannot normalize oversized entry for tail", "error", err, "id", e.ID)
		return
	}
	*e = *stored
}

// rollback truncates the WAL back to a known-good offset after a failed write.
func (w *WALWriter) rollback(offset int64) {
	if err := w.walFile.Truncate(offset); err != nil {
		slog.Error("wal: cannot roll back partial write", "error", err, "offset", offset)
		return
	}
	if err := w.walFile.Sync(); err != nil {
		slog.Error("wal: cannot fsync after rollback", "error", err)
	}
	w.fileSize = offset
	atomic.StoreInt64(&w.validBytes, offset)
}

// advanceEmpty moves the writer to newHour when the current WAL holds nothing
// worth sealing. The empty WAL (and its directory, if now empty) is removed so
// idle hours don't leak directories.
func (w *WALWriter) advanceEmpty(newHour int64) error {
	oldHour := w.segmentHour
	if w.walFile != nil {
		w.walFile.Close()
	}
	oldPath := w.walPath(oldHour)
	if fi, err := os.Stat(oldPath); err == nil && fi.Size() == 0 {
		os.Remove(oldPath)
		os.Remove(filepath.Dir(oldPath)) // only succeeds when the dir is empty
	}
	return w.ensureWAL(newHour)
}

func (w *WALWriter) ensureWAL(hour int64) error {
	dir := filepath.Join(w.dataDir, SegmentDirName(hour))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create segment dir: %w", err)
	}

	walPath := w.walPath(hour)

	// Check if WAL already exists (crash recovery)
	if _, err := os.Stat(walPath); err == nil {
		// Replay to count existing entries. A power loss between Write and Sync
		// leaves a torn trailing record: the parsed prefix is still good, so use
		// it and truncate the tear away rather than refusing to start (which
		// took the whole server down and lost the hour anyway). Appending past a
		// tear would also hide every later record from ReadEntries.
		count, validBytes, err := scanWAL(walPath)
		if err != nil {
			slog.Warn("wal: torn tail on replay; truncating to last good record",
				"error", err, "path", filepath.Base(walPath), "entries", count, "valid_bytes", validBytes)
			if terr := os.Truncate(walPath, validBytes); terr != nil {
				return fmt.Errorf("truncate torn WAL: %w", terr)
			}
		}
		atomic.StoreInt64(&w.entryCount, int64(count))
		atomic.StoreInt64(&w.validBytes, validBytes)
		w.fileSize = validBytes
	} else {
		atomic.StoreInt64(&w.entryCount, 0)
		atomic.StoreInt64(&w.validBytes, 0)
		w.fileSize = 0
	}

	f, err := os.OpenFile(walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open WAL: %w", err)
	}

	w.walFile = f
	w.segmentHour = hour
	return nil
}

func (w *WALWriter) rotate(newHour int64) error {
	// Close current WAL
	if w.walFile != nil {
		w.walFile.Close()
	}

	// Rename active.wal → sealing_T*.wal
	oldPath := w.walPath(w.segmentHour)
	sealPath := w.sealingPath(w.segmentHour)

	if _, err := os.Stat(oldPath); err == nil {
		if err := os.Rename(oldPath, sealPath); err != nil {
			return fmt.Errorf("rename WAL for sealing: %w", err)
		}
	}

	// Open new WAL for the new hour
	return w.ensureWAL(newHour)
}

// walLenPrefixBytes is the size of the record length prefix that wal.ReadEntries
// frames records with. scanWAL needs it to find the offset of the first bad
// record; the record body itself is validated by wal.UnmarshalEntry.
const walLenPrefixBytes = 4

// scanWAL walks a WAL file record by record and reports how many records parse
// and how many bytes of the file are known good. A non-nil error means the file
// is torn or corrupt from validBytes onwards; count/validBytes still describe
// the usable prefix, which callers keep rather than discard.
func scanWAL(walPath string) (count int, validBytes int64, err error) {
	f, err := os.Open(walPath)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return 0, 0, err
	}
	size := fi.Size()

	r := bufio.NewReader(f)
	lenBuf := make([]byte, walLenPrefixBytes)
	for {
		if _, err := io.ReadFull(r, lenBuf); err != nil {
			if err == io.EOF {
				return count, validBytes, nil
			}
			return count, validBytes, fmt.Errorf("read entry length: %w", err)
		}
		entryLen := int64(binary.LittleEndian.Uint32(lenBuf))
		// A garbage length must never drive a huge allocation: no record can
		// extend past the end of the file.
		if entryLen <= 0 || entryLen > size-validBytes-walLenPrefixBytes {
			return count, validBytes, fmt.Errorf("invalid entry length: %d", entryLen)
		}
		data := make([]byte, entryLen)
		if _, err := io.ReadFull(r, data); err != nil {
			return count, validBytes, fmt.Errorf("read entry data: %w", err)
		}
		if _, err := wal.UnmarshalEntry(data); err != nil {
			return count, validBytes, fmt.Errorf("unmarshal entry: %w", err)
		}
		count++
		validBytes += walLenPrefixBytes + entryLen
	}
}
