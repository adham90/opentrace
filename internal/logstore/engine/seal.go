package engine

import (
	"bufio"
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
	FormatVersion int                 `json:"format_version,omitempty"`
	ChunkEntries  int                 `json:"chunk_entries,omitempty"`
	Segment       string              `json:"segment"`
	Chunks        []ChunkMeta         `json:"chunks"`
	EntryCount    int                 `json:"entry_count"`
	IDRange       [2]int64            `json:"id_range"`
	TimeRange     [2]string           `json:"time_range"`
	Counts        MetaCounts          `json:"counts"`
	Histogram     map[string]HBucket  `json:"histogram"`
	Dicts         map[string][]string `json:"dictionaries"`
}

// SealChunkEntries is the physical row-group size for newly sealed segments.
// It may be smaller than the logical ID chunk size; GetByID resolves through
// meta.json ID ranges, so existing 50k chunks and new small row groups coexist.
var SealChunkEntries = chunkSize

func effectiveSealChunkEntries() int {
	if SealChunkEntries < 1 || SealChunkEntries > chunkSize {
		return chunkSize
	}
	return SealChunkEntries
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

	// Stream the WAL a chunk at a time rather than loading the hour.
	//
	// Seal used to read every record into one slice before writing the first
	// chunk, so its peak memory was set by how busy the hour had been — a
	// 600-byte entry plus its body for every log line, all live at once, every
	// hour. Reading exactly one chunk's worth, writing it, and reusing the same
	// slice bounds that to the chunk size no matter how much arrived.
	f, err := os.Open(walPath)
	if err != nil {
		return nil, fmt.Errorf("open WAL: %w", err)
	}

	acc := newMetaAccumulator(segName)
	sc := wal.NewScanner(bufio.NewReaderSize(f, walReadBufferBytes))
	physicalChunkSize := effectiveSealChunkEntries()
	batch := make([]chunk.Entry, 0, physicalChunkSize)
	total := 0
	for {
		// Never let a physical row group cross a logical ID-chunk boundary.
		// Within that boundary IDs are contiguous, so meta.IDRange can map an ID
		// to its physical row without changing the composite ID format.
		limit := min(physicalChunkSize, chunkSize-(total%chunkSize))
		batch = sc.NextBatch(batch[:0], limit)
		if len(batch) == 0 {
			break
		}
		acc.add(batch)
		if err := writeChunk(segDir, len(acc.meta.Chunks), batch, acc.meta); err != nil {
			f.Close()
			return nil, err
		}
		total += len(batch)
	}
	err = sc.Err()
	f.Close()
	if err != nil {
		// The scan yields everything parsed before the bad record. Aborting here
		// used to discard that prefix permanently: the WAL had already been
		// rotated out of the query path, the re-seal failed identically at every
		// startup, and a whole hour of valid entries became unsearchable because
		// of one torn record. Seal what parsed.
		slog.Warn("seal: WAL truncated at a bad record; sealing the valid prefix",
			"error", err, "segment", segName, "entries", total)
	}

	if total == 0 {
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

	slog.Info("seal: starting", "segment", segName, "entries", total)

	meta := acc.finish()

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
		"entries", total,
		"chunks", len(meta.Chunks),
	)

	return meta, nil
}

// metaAccumulator builds a segment's meta.json incrementally, so the stats pass
// can run over one chunk at a time instead of over a slice holding the whole
// hour. Everything it tracks is either a running total or a first/last value,
// which is what makes streaming possible at all.
type metaAccumulator struct {
	meta     *SegmentMeta
	dictSets map[string]map[string]bool
	first    *chunk.Entry
	last     chunk.Entry
	seen     int
}

func newMetaAccumulator(segName string) *metaAccumulator {
	return &metaAccumulator{
		meta: &SegmentMeta{
			FormatVersion: 2,
			ChunkEntries:  effectiveSealChunkEntries(),
			Segment:       segName,
			Counts: MetaCounts{
				ByLevel:        make(map[string]int),
				ByService:      make(map[string]int),
				ByServiceError: make(map[string]int),
			},
			Histogram: make(map[string]HBucket),
			Dicts:     make(map[string][]string),
		},
		dictSets: map[string]map[string]bool{
			"service":    {},
			"level":      {},
			"env":        {},
			"event_type": {},
		},
	}
}

// add folds one batch of entries into the running totals. The batch's backing
// array is reused by the caller for the next batch, so anything retained here
// is copied.
func (a *metaAccumulator) add(entries []chunk.Entry) {
	if len(entries) == 0 {
		return
	}
	if a.first == nil {
		first := entries[0]
		a.first = &first
	}
	a.last = entries[len(entries)-1]
	a.seen += len(entries)

	// Consecutive entries almost always share a minute (received_at is assigned
	// at append time), so the key is formatted once per minute, not once per row.
	var lastMinute int64 = -1
	var minuteKey string

	for i := range entries {
		e := &entries[i]

		a.meta.Counts.ByLevel[e.Level]++
		if e.Service != "" {
			a.meta.Counts.ByService[e.Service]++
			if isErrorLevel(e.Level) {
				a.meta.Counts.ByServiceError[e.Service]++
			}
		}

		if minute := e.ReceivedAt / 60000; minute != lastMinute {
			lastMinute = minute
			minuteKey = time.UnixMilli(minute * 60000).UTC().Format("2006-01-02T15:04")
		}
		bucket := a.meta.Histogram[minuteKey]
		bucket.Total++
		if isErrorLevel(e.Level) {
			bucket.Errors++
		}
		a.meta.Histogram[minuteKey] = bucket

		a.dictSets["level"][e.Level] = true
		if e.Service != "" {
			a.dictSets["service"][e.Service] = true
		}
		if e.Env != "" {
			a.dictSets["env"][e.Env] = true
		}
		if e.EventType != "" {
			a.dictSets["event_type"][e.EventType] = true
		}
	}
}

// finish resolves the fields that need the whole segment: the entry count, the
// ID and time ranges, and the column dictionaries.
func (a *metaAccumulator) finish() *SegmentMeta {
	a.meta.EntryCount = a.seen
	if a.first != nil {
		a.meta.IDRange = [2]int64{a.first.ID, a.last.ID}
		a.meta.TimeRange = [2]string{
			time.UnixMilli(a.first.ReceivedAt).UTC().Format(time.RFC3339),
			time.UnixMilli(a.last.ReceivedAt).UTC().Format(time.RFC3339),
		}
	}
	for name, set := range a.dictSets {
		vals := make([]string, 0, len(set))
		for v := range set {
			vals = append(vals, v)
		}
		a.meta.Dicts[name] = vals
	}
	return a.meta
}

// writeChunk runs the write pass for one chunk: the columnar file plus its
// inverted index, recorded in meta.
func writeChunk(segDir string, ci int, entries []chunk.Entry, meta *SegmentMeta) error {
	chunkPath := filepath.Join(segDir, fmt.Sprintf("chunk_%03d.col", ci))
	idxPath := filepath.Join(segDir, fmt.Sprintf("chunk_%03d.idx", ci))

	w, err := chunk.NewWriter(chunkPath, entries)
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
	for ri := range entries {
		idxBuilder.Add(uint32(ri), entries[ri].Message)
	}
	if err := idxBuilder.Write(idxPath, len(entries)); err != nil {
		return fmt.Errorf("write index %d: %w", ci, err)
	}

	meta.Chunks = append(meta.Chunks, ChunkMeta{
		Index:      ci,
		EntryCount: len(entries),
		IDRange:    [2]int64{entries[0].ID, entries[len(entries)-1].ID},
	})
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
