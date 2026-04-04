package dump

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/engine"
	"github.com/adham90/opentrace/internal/logstore/index"
)

// Segment prints a human-readable summary of a segment directory.
func Segment(w io.Writer, segDir string) error {
	// Read meta.json
	metaPath := filepath.Join(segDir, "meta.json")
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("read meta.json: %w", err)
	}

	var meta engine.SegmentMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return fmt.Errorf("parse meta.json: %w", err)
	}

	fmt.Fprintf(w, "Segment: %s\n", meta.Segment)
	fmt.Fprintf(w, "Entries: %d\n", meta.EntryCount)
	fmt.Fprintf(w, "Chunks:  %d\n", len(meta.Chunks))
	fmt.Fprintf(w, "Time:    %s → %s\n", meta.TimeRange[0], meta.TimeRange[1])
	fmt.Fprintf(w, "IDs:     %d → %d\n", meta.IDRange[0], meta.IDRange[1])

	fmt.Fprintf(w, "\nCounts by level:\n")
	for level, count := range meta.Counts.ByLevel {
		fmt.Fprintf(w, "  %-8s %d\n", level, count)
	}

	fmt.Fprintf(w, "\nCounts by service:\n")
	for svc, count := range meta.Counts.ByService {
		fmt.Fprintf(w, "  %-20s %d\n", svc, count)
	}

	fmt.Fprintf(w, "\nDictionaries:\n")
	for name, vals := range meta.Dicts {
		fmt.Fprintf(w, "  %s: %v\n", name, vals)
	}

	fmt.Fprintf(w, "\nHistogram (%d minutes):\n", len(meta.Histogram))
	for minute, bucket := range meta.Histogram {
		fmt.Fprintf(w, "  %s: total=%d errors=%d\n", minute, bucket.Total, bucket.Errors)
	}

	// Dump each chunk
	for _, cm := range meta.Chunks {
		fmt.Fprintf(w, "\n--- Chunk %d (%d entries) ---\n", cm.Index, cm.EntryCount)

		chunkPath := filepath.Join(segDir, fmt.Sprintf("chunk_%03d.col", cm.Index))
		if err := dumpChunkSummary(w, chunkPath); err != nil {
			fmt.Fprintf(w, "  Error reading chunk: %v\n", err)
		}

		idxPath := filepath.Join(segDir, fmt.Sprintf("chunk_%03d.idx", cm.Index))
		if err := dumpIndexSummary(w, idxPath); err != nil {
			fmt.Fprintf(w, "  Error reading index: %v\n", err)
		}
	}

	return nil
}

func dumpChunkSummary(w io.Writer, path string) error {
	r, err := chunk.OpenReader(path)
	if err != nil {
		return err
	}
	defer r.Close()

	fi, _ := os.Stat(path)
	fmt.Fprintf(w, "  File: %s (%d bytes)\n", filepath.Base(path), fi.Size())
	fmt.Fprintf(w, "  Entries: %d\n", r.EntryCount)

	// Show first 3 entries
	limit := 3
	if r.EntryCount < limit {
		limit = r.EntryCount
	}

	if limit > 0 {
		fmt.Fprintf(w, "  First %d entries:\n", limit)

		levels, _ := r.ReadDictBitpack("level")
		services, _ := r.ReadDictZstd("service")
		messages, _ := r.ReadZstdBlockStrings("message")
		ts, _ := r.ReadZstdInt64("ts")

		for i := range limit {
			var level, service, message string
			var timestamp int64
			if i < len(levels) {
				level = levels[i]
			}
			if i < len(services) {
				service = services[i]
			}
			if i < len(messages) {
				message = messages[i]
				if len(message) > 80 {
					message = message[:80] + "..."
				}
			}
			if i < len(ts) {
				timestamp = ts[i]
			}
			fmt.Fprintf(w, "    [%d] %s %-8s [%-15s] %s\n", timestamp, level, "", service, message)
		}
	}

	return nil
}

func dumpIndexSummary(w io.Writer, path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return nil // index might not exist
	}

	idx, err := index.OpenReader(path)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "  Index: %s (%d bytes)\n", filepath.Base(path), fi.Size())

	// Test a search to verify it works
	ids, _ := idx.Search("error")
	fmt.Fprintf(w, "  Sample search 'error': %d matches\n", len(ids))

	return nil
}
