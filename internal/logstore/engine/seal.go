package engine

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/index"
	"github.com/adham90/opentrace/internal/logstore/wal"
)

// SegmentMeta holds pre-computed statistics for a sealed segment.
type SegmentMeta struct {
	Segment    string              `json:"segment"`
	Chunks     []ChunkMeta         `json:"chunks"`
	EntryCount int                 `json:"entry_count"`
	IDRange    [2]int64            `json:"id_range"`
	TimeRange  [2]string           `json:"time_range"`
	Counts     MetaCounts          `json:"counts"`
	Histogram  map[string]HBucket  `json:"histogram"`
	Dicts      map[string][]string `json:"dictionaries"`
}

// ChunkMeta holds info about a single chunk within a segment.
type ChunkMeta struct {
	Index      int      `json:"index"`
	EntryCount int      `json:"entry_count"`
	IDRange    [2]int64 `json:"id_range"`
}

// MetaCounts holds pre-computed aggregation counts.
type MetaCounts struct {
	ByLevel   map[string]int `json:"by_level"`
	ByService map[string]int `json:"by_service"`
	// ByServiceError counts error/fatal rows per service. Without it the
	// per-service error count had to be reported as zero (an incident read as
	// "0 errors, unchanged") or recomputed with a full scan.
	ByServiceError map[string]int `json:"by_service_error,omitempty"`
}

// isErrorLevel reports whether a level counts as an error for aggregate stats.
func isErrorLevel(level string) bool {
	return level == "error" || level == "fatal"
}

// HBucket holds histogram counts for a single minute.
type HBucket struct {
	Total  int `json:"total"`
	Errors int `json:"errors"`
}

// Seal reads a WAL file, writes columnar chunks + inverted index + meta.json.
// This is the 2-pass streaming seal process.
func Seal(walPath string, segmentHour int64) (*SegmentMeta, error) {
	segDir := filepath.Dir(walPath)
	segName := SegmentDirName(segmentHour)

	// Refuse to seal into a directory that already holds a finished segment.
	// Seal rewrites chunk_000 in place, so doing this destroys every entry
	// already sealed there — a graceful same-hour restart once wiped a whole
	// store this way. Writers avoid sealed hours (freeSegmentHour), and this is
	// the backstop at the point of damage: fail loudly, keep the data.
	if IsSealComplete(segDir) {
		return nil, fmt.Errorf("refusing to reseal %s: segment already sealed", segName)
	}

	// Read all entries from the WAL
	f, err := os.Open(walPath)
	if err != nil {
		return nil, fmt.Errorf("open WAL: %w", err)
	}

	entries, err := wal.ReadEntries(f)
	f.Close()
	if err != nil {
		// ReadEntries returns everything it parsed before the bad record.
		// Aborting here used to discard that prefix permanently: the WAL had
		// already been rotated out of the query path, the re-seal failed
		// identically at every startup, and a whole hour of valid entries
		// became unsearchable because of one torn record. Seal what parsed.
		slog.Warn("seal: WAL truncated at a bad record; sealing the valid prefix",
			"error", err, "segment", segName, "entries", len(entries))
	}

	if len(entries) == 0 {
		if err != nil {
			// Nothing parseable at all. Deleting would destroy evidence and
			// leaving it in place would make every startup retry the same
			// failure, so park it under a quarantine name instead.
			quarantine := walPath + ".corrupt"
			if rerr := os.Rename(walPath, quarantine); rerr != nil {
				slog.Error("seal: cannot quarantine unreadable WAL", "error", rerr, "segment", segName)
			} else {
				slog.Error("seal: WAL unreadable, quarantined", "error", err, "segment", segName)
			}
			return nil, nil
		}
		// Nothing to seal — clean up
		os.Remove(walPath)
		return nil, nil
	}

	slog.Info("seal: starting", "segment", segName, "entries", len(entries))

	meta := buildSegmentMeta(entries, segName)
	if err := writeChunks(segDir, entries, meta); err != nil {
		return nil, err
	}

	// Write meta.json (durably). Chunks and index were already fsynced by their
	// writers.
	metaPath := filepath.Join(segDir, "meta.json")
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal meta: %w", err)
	}
	if err := writeFileSync(metaPath, metaJSON); err != nil {
		return nil, fmt.Errorf("write meta: %w", err)
	}

	// fsync the directory so the chunk/index/meta directory entries are durable
	// before the completion marker is written.
	if err := syncDir(segDir); err != nil {
		return nil, fmt.Errorf("fsync segment dir: %w", err)
	}

	// Write .seal_complete marker (durably), then fsync the dir again so the
	// marker's presence is durable. Only after this is the segment safe.
	markerPath := filepath.Join(segDir, ".seal_complete")
	if err := writeFileSync(markerPath, []byte("ok")); err != nil {
		return nil, fmt.Errorf("write seal marker: %w", err)
	}
	if err := syncDir(segDir); err != nil {
		return nil, fmt.Errorf("fsync segment dir (marker): %w", err)
	}

	// Only now delete the WAL — the segment is fully durable, so a crash can
	// never lose both the WAL and the sealed segment.
	os.Remove(walPath)

	slog.Info("seal: complete",
		"segment", segName,
		"entries", len(entries),
		"chunks", len(meta.Chunks),
	)

	return meta, nil
}

// buildSegmentMeta runs the stats pass: per-level/service counts, the per-minute
// histogram and the column dictionaries.
func buildSegmentMeta(entries []chunk.Entry, segName string) *SegmentMeta {
	meta := &SegmentMeta{
		Segment:    segName,
		EntryCount: len(entries),
		IDRange:    [2]int64{entries[0].ID, entries[len(entries)-1].ID},
		Counts: MetaCounts{
			ByLevel:        make(map[string]int),
			ByService:      make(map[string]int),
			ByServiceError: make(map[string]int),
		},
		Histogram: make(map[string]HBucket),
		Dicts:     make(map[string][]string),
	}

	// Time range from received_at (monotonic)
	meta.TimeRange = [2]string{
		time.UnixMilli(entries[0].ReceivedAt).UTC().Format(time.RFC3339),
		time.UnixMilli(entries[len(entries)-1].ReceivedAt).UTC().Format(time.RFC3339),
	}

	dictSets := map[string]map[string]bool{
		"service":    {},
		"level":      {},
		"env":        {},
		"event_type": {},
	}

	for i := range entries {
		e := &entries[i]

		meta.Counts.ByLevel[e.Level]++
		if e.Service != "" {
			meta.Counts.ByService[e.Service]++
			if isErrorLevel(e.Level) {
				meta.Counts.ByServiceError[e.Service]++
			}
		}

		minuteKey := time.UnixMilli(e.ReceivedAt).UTC().Format("2006-01-02T15:04")
		bucket := meta.Histogram[minuteKey]
		bucket.Total++
		if isErrorLevel(e.Level) {
			bucket.Errors++
		}
		meta.Histogram[minuteKey] = bucket

		dictSets["level"][e.Level] = true
		if e.Service != "" {
			dictSets["service"][e.Service] = true
		}
		if e.Env != "" {
			dictSets["env"][e.Env] = true
		}
		if e.EventType != "" {
			dictSets["event_type"][e.EventType] = true
		}
	}

	for name, set := range dictSets {
		vals := make([]string, 0, len(set))
		for v := range set {
			vals = append(vals, v)
		}
		meta.Dicts[name] = vals
	}
	return meta
}

// writeChunks runs the write pass: columnar chunks plus their inverted indexes,
// recording each chunk in meta.
func writeChunks(segDir string, entries []chunk.Entry, meta *SegmentMeta) error {
	chunkEntries := splitChunks(entries, chunkSize)
	meta.Chunks = make([]ChunkMeta, len(chunkEntries))

	for ci, ce := range chunkEntries {
		chunkPath := filepath.Join(segDir, fmt.Sprintf("chunk_%03d.col", ci))
		idxPath := filepath.Join(segDir, fmt.Sprintf("chunk_%03d.idx", ci))

		w, err := chunk.NewWriter(chunkPath, ce)
		if err != nil {
			return fmt.Errorf("create chunk %d: %w", ci, err)
		}
		if err := w.WriteAllColumns(); err != nil {
			w.Close()
			return fmt.Errorf("write chunk %d columns: %w", ci, err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("close chunk %d: %w", ci, err)
		}

		idxBuilder := index.NewBuilder()
		for ri, e := range ce {
			idxBuilder.Add(uint32(ri), e.Message)
		}
		if err := idxBuilder.Write(idxPath, len(ce)); err != nil {
			return fmt.Errorf("write index %d: %w", ci, err)
		}

		meta.Chunks[ci] = ChunkMeta{
			Index:      ci,
			EntryCount: len(ce),
			IDRange:    [2]int64{ce[0].ID, ce[len(ce)-1].ID},
		}
	}
	return nil
}

// writeFileSync writes data to path and fsyncs it before returning, so the
// content is durable and not merely in the page cache.
func writeFileSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// syncDir fsyncs a directory so newly created files' directory entries are
// durable (required before treating the files as committed).
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}

// splitChunks divides entries into chunks of at most maxSize.
func splitChunks(entries []chunk.Entry, maxSize int) [][]chunk.Entry {
	if len(entries) == 0 {
		return nil
	}
	var chunks [][]chunk.Entry
	for i := 0; i < len(entries); i += maxSize {
		end := i + maxSize
		if end > len(entries) {
			end = len(entries)
		}
		chunks = append(chunks, entries[i:end])
	}
	return chunks
}

// IsSealComplete checks if a segment directory has a .seal_complete marker.
func IsSealComplete(segDir string) bool {
	_, err := os.Stat(filepath.Join(segDir, ".seal_complete"))
	return err == nil
}

// CleanIncompleteSeal removes partial chunk/index files from a failed seal.
func CleanIncompleteSeal(segDir string) error {
	matches, _ := filepath.Glob(filepath.Join(segDir, "chunk_*.col"))
	for _, m := range matches {
		os.Remove(m)
	}
	matches, _ = filepath.Glob(filepath.Join(segDir, "chunk_*.idx"))
	for _, m := range matches {
		os.Remove(m)
	}
	os.Remove(filepath.Join(segDir, "meta.json"))
	os.Remove(filepath.Join(segDir, ".seal_complete"))
	return nil
}
