package engine

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SegmentManager manages the lifecycle of sealed segments and the active WAL.
type SegmentManager struct {
	mu       sync.RWMutex
	dataDir  string
	segments []*LoadedSegment // sorted by segment hour, ascending
}

// LoadedSegment is a sealed segment loaded into memory (metadata only).
type LoadedSegment struct {
	Hour    int64
	DirName string
	DirPath string
	Meta    *SegmentMeta
}

// NewSegmentManager scans the data directory and loads all sealed segments.
func NewSegmentManager(dataDir string) (*SegmentManager, error) {
	sm := &SegmentManager{dataDir: dataDir}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	if err := sm.scan(); err != nil {
		return nil, fmt.Errorf("scan segments: %w", err)
	}

	return sm, nil
}

// scan discovers and loads all sealed segments from disk.
func (sm *SegmentManager) scan() error {
	entries, err := os.ReadDir(sm.dataDir)
	if err != nil {
		return fmt.Errorf("read data dir: %w", err)
	}

	// The WAL writer owns the current hour's active.wal; every earlier hour's is
	// an orphan left by a restart and must be recovered here.
	currentHour := SegmentHourFromTime(time.Now().UTC())

	var segments []*LoadedSegment
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirPath := filepath.Join(sm.dataDir, entry.Name())
		sm.recoverDir(dirPath, entry.Name(), currentHour)

		// Skip if not a sealed segment
		if !IsSealComplete(dirPath) {
			continue
		}

		// Load meta.json
		metaPath := filepath.Join(dirPath, "meta.json")
		metaData, err := os.ReadFile(metaPath)
		if err != nil {
			slog.Warn("segment: cannot read meta.json", "dir", entry.Name(), "error", err)
			continue
		}

		var meta SegmentMeta
		if err := json.Unmarshal(metaData, &meta); err != nil {
			slog.Warn("segment: invalid meta.json", "dir", entry.Name(), "error", err)
			continue
		}

		hour := parseSegmentHour(entry.Name())
		segments = append(segments, &LoadedSegment{
			Hour:    hour,
			DirName: entry.Name(),
			DirPath: dirPath,
			Meta:    &meta,
		})
	}

	// Sort by hour ascending
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].Hour < segments[j].Hour
	})

	sm.mu.Lock()
	sm.segments = segments
	sm.mu.Unlock()

	slog.Info("segment: loaded segments", "count", len(segments))
	return nil
}

// recoverDir brings one segment directory into a loadable state at startup:
// it finishes an interrupted seal, and it seals a previous hour's orphaned
// active.wal. An orphan is left behind by any restart that crosses an hour
// boundary — the writer only ever opens the current hour, nothing else reads
// old active.wal files, and Prune only touches registered segments, so those
// entries used to be invisible to every query forever and the directory leaked.
func (sm *SegmentManager) recoverDir(dirPath, dirName string, currentHour int64) {
	if IsSealComplete(dirPath) {
		return
	}
	hour := parseSegmentHour(dirName)
	if hour <= 0 {
		return
	}

	// Finish an interrupted seal first (its sealing_*.wal is authoritative).
	sealingFiles, _ := filepath.Glob(filepath.Join(dirPath, "sealing_*.wal"))
	if len(sealingFiles) > 0 {
		slog.Warn("segment: incomplete seal detected, cleaning up", "dir", dirName)
		CleanIncompleteSeal(dirPath)
		for _, walFile := range sealingFiles {
			slog.Info("segment: re-sealing from WAL", "wal", walFile)
			if _, err := Seal(walFile, hour); err != nil {
				slog.Error("segment: re-seal failed", "error", err, "wal", walFile)
			}
		}
		return
	}

	// Orphaned active.wal from an earlier hour: rename it into the normal
	// sealing path (so a crash mid-seal is recoverable by the branch above),
	// then seal it so the hour becomes searchable and prunable again.
	if hour >= currentHour {
		return // current hour belongs to the live writer
	}
	activePath := filepath.Join(dirPath, "active.wal")
	if _, err := os.Stat(activePath); err != nil {
		return
	}
	sealingPath := filepath.Join(dirPath, fmt.Sprintf("sealing_%s.wal", SegmentDirName(hour)))
	if err := os.Rename(activePath, sealingPath); err != nil {
		slog.Error("segment: cannot rename orphaned WAL for sealing", "error", err, "dir", dirName)
		return
	}
	slog.Warn("segment: sealing orphaned active WAL from a previous hour", "dir", dirName)
	if _, err := Seal(sealingPath, hour); err != nil {
		slog.Error("segment: orphan seal failed", "error", err, "dir", dirName)
	}
}

// Register adds a newly sealed segment to the manager.
func (sm *SegmentManager) Register(hour int64, meta *SegmentMeta) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	dirName := SegmentDirName(hour)
	dirPath := filepath.Join(sm.dataDir, dirName)

	seg := &LoadedSegment{
		Hour:    hour,
		DirName: dirName,
		DirPath: dirPath,
		Meta:    meta,
	}

	// Insert in sorted order
	idx := sort.Search(len(sm.segments), func(i int) bool {
		return sm.segments[i].Hour >= hour
	})

	// Check for duplicate
	if idx < len(sm.segments) && sm.segments[idx].Hour == hour {
		sm.segments[idx] = seg // replace
		return
	}

	// Insert
	sm.segments = append(sm.segments, nil)
	copy(sm.segments[idx+1:], sm.segments[idx:])
	sm.segments[idx] = seg
}

// SegmentsInRange returns sealed segments that may contain data for the given time range.
// Includes ±1 hour buffer for clock skew (received_at vs ts).
func (sm *SegmentManager) SegmentsInRange(start, end time.Time) []*LoadedSegment {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	startHour := SegmentHourFromTime(start) - 1 // -1 hour buffer
	endHour := SegmentHourFromTime(end) + 1     // +1 hour buffer

	var result []*LoadedSegment
	for _, seg := range sm.segments {
		if seg.Hour >= startHour && seg.Hour <= endHour {
			result = append(result, seg)
		}
	}
	return result
}

// AllSegments returns all loaded segments (for operations that need all data).
func (sm *SegmentManager) AllSegments() []*LoadedSegment {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	result := make([]*LoadedSegment, len(sm.segments))
	copy(result, sm.segments)
	return result
}

// SegmentCount returns the number of sealed segments.
func (sm *SegmentManager) SegmentCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.segments)
}

// Prune deletes segments older than the given duration.
// Returns the number of segments deleted.
func (sm *SegmentManager) Prune(retention time.Duration) (int, error) {
	cutoffHour := SegmentHourFromTime(time.Now().UTC().Add(-retention))

	sm.mu.Lock()
	defer sm.mu.Unlock()

	deleted := 0
	var remaining []*LoadedSegment
	for _, seg := range sm.segments {
		if seg.Hour < cutoffHour {
			if err := os.RemoveAll(seg.DirPath); err != nil {
				slog.Error("segment: prune failed", "dir", seg.DirName, "error", err)
				remaining = append(remaining, seg) // keep in list if delete failed
				continue
			}
			slog.Info("segment: pruned", "dir", seg.DirName)
			deleted++
		} else {
			remaining = append(remaining, seg)
		}
	}
	sm.segments = remaining
	return deleted, nil
}

// FindSegmentByID locates which segment contains a given composite ID.
func (sm *SegmentManager) FindSegmentByID(id int64) *LoadedSegment {
	hour, _, _ := DecodeID(id)

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	idx := sort.Search(len(sm.segments), func(i int) bool {
		return sm.segments[i].Hour >= hour
	})
	if idx < len(sm.segments) && sm.segments[idx].Hour == hour {
		return sm.segments[idx]
	}
	return nil
}

// parseSegmentHour parses a directory name like "2026-04-04T12" into a segment hour.
func parseSegmentHour(dirName string) int64 {
	// Format: 2006-01-02T15
	t, err := time.Parse("2006-01-02T15", dirName)
	if err != nil {
		// Try to handle names that might have different formats
		if strings.Contains(dirName, "T") {
			t, err = time.Parse("2006-01-02T15", dirName)
			if err != nil {
				return 0
			}
		} else {
			return 0
		}
	}
	return SegmentHourFromTime(t)
}
