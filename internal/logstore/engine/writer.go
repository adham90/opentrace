package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/wal"
)

// WALWriter handles appending entries to the active WAL file.
// Thread-safe: multiple goroutines can call Append concurrently.
type WALWriter struct {
	mu sync.Mutex

	dataDir     string
	segmentHour int64 // current segment hour
	walFile     *os.File
	entryCount  int64 // entries written this hour (atomic read for ID computation)

	// validBytes is the number of bytes safely fsynced in the WAL.
	// Readers snapshot this value before reading.
	validBytes int64

	ring *RingBuffer
}

// NewWALWriter creates a new WAL writer.
// dataDir is the base directory (e.g., "data/logs").
// It opens or creates the WAL file for the current hour.
func NewWALWriter(dataDir string, ring *RingBuffer) (*WALWriter, error) {
	now := time.Now().UTC()
	segHour := SegmentHourFromTime(now)

	w := &WALWriter{
		dataDir:     dataDir,
		segmentHour: segHour,
		ring:        ring,
	}

	if err := w.ensureWAL(segHour); err != nil {
		return nil, err
	}

	return w, nil
}

// Append writes a batch of entries to the WAL, assigns IDs, and notifies tail subscribers.
// Returns the assigned entries (with IDs populated).
func (w *WALWriter) Append(entries []chunk.Entry) ([]chunk.Entry, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now().UTC()
	currentHour := SegmentHourFromTime(now)

	// Rotate WAL if the hour has changed
	if currentHour != w.segmentHour {
		if err := w.rotate(currentHour); err != nil {
			return nil, fmt.Errorf("rotate WAL: %w", err)
		}
	}

	// Assign IDs and received_at
	receivedAt := now.UnixMilli()
	startIdx := int(atomic.LoadInt64(&w.entryCount))
	for i := range entries {
		entries[i].ID = IDForPosition(w.segmentHour, startIdx+i)
		entries[i].ReceivedAt = receivedAt
	}

	// Serialize and write
	for i := range entries {
		data := wal.MarshalEntry(&entries[i])
		if _, err := w.walFile.Write(data); err != nil {
			return nil, fmt.Errorf("write WAL entry: %w", err)
		}
	}

	// Fsync the batch
	if err := w.walFile.Sync(); err != nil {
		return nil, fmt.Errorf("fsync WAL: %w", err)
	}

	// Update counters
	newCount := int64(startIdx + len(entries))
	atomic.StoreInt64(&w.entryCount, newCount)

	// Update valid bytes for concurrent readers
	fi, err := w.walFile.Stat()
	if err == nil {
		atomic.StoreInt64(&w.validBytes, fi.Size())
	}

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

	if entryCount == 0 {
		return "", sealHour, 0, nil // nothing to seal
	}

	now := time.Now().UTC()
	nextHour := SegmentHourFromTime(now)
	if nextHour == sealHour {
		nextHour = sealHour + 1 // force rotation even within same hour (for testing)
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

func (w *WALWriter) ensureWAL(hour int64) error {
	dir := filepath.Join(w.dataDir, SegmentDirName(hour))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create segment dir: %w", err)
	}

	walPath := w.walPath(hour)

	// Check if WAL already exists (crash recovery)
	if fi, err := os.Stat(walPath); err == nil {
		// Replay to count existing entries
		count, err := w.replayCount(walPath)
		if err != nil {
			return fmt.Errorf("replay WAL: %w", err)
		}
		atomic.StoreInt64(&w.entryCount, int64(count))
		atomic.StoreInt64(&w.validBytes, fi.Size())
	} else {
		atomic.StoreInt64(&w.entryCount, 0)
		atomic.StoreInt64(&w.validBytes, 0)
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

func (w *WALWriter) replayCount(walPath string) (int, error) {
	f, err := os.Open(walPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	entries, err := wal.ReadEntries(f)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}
