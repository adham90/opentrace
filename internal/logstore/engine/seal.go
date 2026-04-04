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
	Index      int    `json:"index"`
	EntryCount int    `json:"entry_count"`
	IDRange    [2]int64 `json:"id_range"`
}

// MetaCounts holds pre-computed aggregation counts.
type MetaCounts struct {
	ByLevel   map[string]int `json:"by_level"`
	ByService map[string]int `json:"by_service"`
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

	// Read all entries from the WAL
	f, err := os.Open(walPath)
	if err != nil {
		return nil, fmt.Errorf("open WAL: %w", err)
	}

	entries, err := wal.ReadEntries(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("read WAL: %w", err)
	}

	if len(entries) == 0 {
		// Nothing to seal — clean up
		os.Remove(walPath)
		return nil, nil
	}

	slog.Info("seal: starting", "segment", segName, "entries", len(entries))

	// --- Pass 1: Compute stats for meta.json ---
	meta := &SegmentMeta{
		Segment:    segName,
		EntryCount: len(entries),
		IDRange:    [2]int64{entries[0].ID, entries[len(entries)-1].ID},
		Counts: MetaCounts{
			ByLevel:   make(map[string]int),
			ByService: make(map[string]int),
		},
		Histogram: make(map[string]HBucket),
		Dicts:     make(map[string][]string),
	}

	// Time range from received_at (monotonic)
	meta.TimeRange = [2]string{
		time.UnixMilli(entries[0].ReceivedAt).UTC().Format(time.RFC3339),
		time.UnixMilli(entries[len(entries)-1].ReceivedAt).UTC().Format(time.RFC3339),
	}

	// Collect stats
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
		}

		// Per-minute histogram
		minuteKey := time.UnixMilli(e.ReceivedAt).UTC().Format("2006-01-02T15:04")
		bucket := meta.Histogram[minuteKey]
		bucket.Total++
		if e.Level == "error" || e.Level == "fatal" {
			bucket.Errors++
		}
		meta.Histogram[minuteKey] = bucket

		// Dictionary values
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

	// --- Pass 2: Write chunks + inverted index ---
	chunkEntries := splitChunks(entries, chunkSize)
	meta.Chunks = make([]ChunkMeta, len(chunkEntries))

	for ci, ce := range chunkEntries {
		chunkPath := filepath.Join(segDir, fmt.Sprintf("chunk_%03d.col", ci))
		idxPath := filepath.Join(segDir, fmt.Sprintf("chunk_%03d.idx", ci))

		// Write columnar chunk
		w, err := chunk.NewWriter(chunkPath, ce)
		if err != nil {
			return nil, fmt.Errorf("create chunk %d: %w", ci, err)
		}
		if err := w.WriteAllColumns(); err != nil {
			w.Close()
			return nil, fmt.Errorf("write chunk %d columns: %w", ci, err)
		}
		if err := w.Close(); err != nil {
			return nil, fmt.Errorf("close chunk %d: %w", ci, err)
		}

		// Build inverted index (from message column)
		idxBuilder := index.NewBuilder()
		for ri, e := range ce {
			idxBuilder.Add(uint32(ri), e.Message)
		}
		if err := idxBuilder.Write(idxPath, len(ce)); err != nil {
			return nil, fmt.Errorf("write index %d: %w", ci, err)
		}

		meta.Chunks[ci] = ChunkMeta{
			Index:      ci,
			EntryCount: len(ce),
			IDRange:    [2]int64{ce[0].ID, ce[len(ce)-1].ID},
		}
	}

	// Write meta.json
	metaPath := filepath.Join(segDir, "meta.json")
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal meta: %w", err)
	}
	if err := os.WriteFile(metaPath, metaJSON, 0o644); err != nil {
		return nil, fmt.Errorf("write meta: %w", err)
	}

	// Write .seal_complete marker
	markerPath := filepath.Join(segDir, ".seal_complete")
	if err := os.WriteFile(markerPath, []byte("ok"), 0o644); err != nil {
		return nil, fmt.Errorf("write seal marker: %w", err)
	}

	// Delete the WAL
	os.Remove(walPath)

	slog.Info("seal: complete",
		"segment", segName,
		"entries", len(entries),
		"chunks", len(chunkEntries),
	)

	return meta, nil
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
