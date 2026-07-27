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

	"github.com/adham90/opentrace/internal/logstore/chunk"
	chunkpkg "github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/index"
	"github.com/adham90/opentrace/internal/logstore/ingest"
	"github.com/adham90/opentrace/internal/logstore/wal"
)

// Store is the main log store engine. It implements the core query operations
// on top of the WAL writer, segment manager, and ingest pipeline.
type Store struct {
	dataDir  string
	writer   *WALWriter
	segments *SegmentManager
	ring     *RingBuffer
	pipeline *ingest.Pipeline

	sealMu sync.Mutex // prevent concurrent seals
}

// NewStore creates and initializes the segmented log store.
func NewStore(dataDir string, samplingRules []ingest.SamplingRule, piiConfig ingest.PIIConfig) (*Store, error) {
	ring := NewRingBuffer()

	writer, err := NewWALWriter(dataDir, ring)
	if err != nil {
		return nil, fmt.Errorf("init WAL writer: %w", err)
	}

	segments, err := NewSegmentManager(dataDir)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("init segment manager: %w", err)
	}

	pipeline := ingest.NewPipeline(samplingRules, piiConfig)

	return &Store{
		dataDir:  dataDir,
		writer:   writer,
		segments: segments,
		ring:     ring,
		pipeline: pipeline,
	}, nil
}

// Close shuts down the store gracefully.
func (s *Store) Close() error {
	return s.writer.Close()
}

// --- Write Operations ---

// Ingest processes and stores a batch of log entries.
// Runs the full pipeline: sampling → PII scrub → error extraction → log expansion → WAL append.
func (s *Store) Ingest(entries []chunk.Entry) ([]chunk.Entry, error) {
	processed := s.pipeline.Process(entries)
	if len(processed) == 0 {
		return nil, nil
	}
	return s.writer.Append(processed)
}

// SealCurrentHour triggers sealing of the current hour's WAL.
func (s *Store) SealCurrentHour() error {
	s.sealMu.Lock()
	defer s.sealMu.Unlock()

	sealPath, sealHour, count, err := s.writer.Rotate()
	if err != nil {
		return fmt.Errorf("rotate WAL: %w", err)
	}
	if count == 0 {
		return nil // nothing to seal
	}

	meta, err := Seal(sealPath, sealHour)
	if err != nil {
		return fmt.Errorf("seal: %w", err)
	}
	if meta != nil {
		s.segments.Register(sealHour, meta)
	}
	return nil
}

// Prune deletes segments older than the given retention duration.
func (s *Store) Prune(retention time.Duration) (int, error) {
	return s.segments.Prune(retention)
}

// --- Read Operations ---

// SearchParams defines filters for log search.
type SearchParams struct {
	Query            string     // full-text search on message
	Service          string     // exact match
	Level            string     // exact match
	Env              string     // exact match
	TraceID          string     // exact match
	RequestID        string     // exact match
	UserID           string     // exact match
	TenantID         string     // exact match
	EventType        string     // exact match
	ErrorClass   string     // exact match
	ErrorFingerprint string     // exact match
	Method           string     // exact match
	Path             string     // substring match
	MinDurationMs    int        // minimum duration_ms
	NPlusOneOnly    bool       // only n_plus_one entries
	Start            *time.Time // event time range start
	End              *time.Time // event time range end
	Limit            int
	Offset           int
	SortAsc          bool // true = oldest first
}

// SearchResult holds search results.
type SearchResult struct {
	Entries []chunk.Entry
	Total   int // total matching (may be > len(Entries) if limited)
}

// Search finds log entries matching the given parameters.
func (s *Store) Search(params SearchParams) (*SearchResult, error) {
	if params.Limit <= 0 {
		params.Limit = 50
	}
	if params.Limit > 500 {
		params.Limit = 500
	}

	// Default time range: last 1 hour
	now := time.Now().UTC()
	if params.Start == nil {
		start := now.Add(-time.Hour)
		params.Start = &start
	}
	if params.End == nil {
		params.End = &now
	}

	// Collect results from sealed segments + active WAL
	var allMatches []chunk.Entry
	total := 0

	// The final page needs at most offset+limit rows, so each segment can
	// contribute at most that many candidates to the global top-N. Capping per
	// segment bounds the merge set instead of materializing every matching row.
	perSegLimit := params.Offset + params.Limit

	// Query sealed segments with bounded concurrency. A wide time range can match
	// hundreds of hourly segments; spawning one decompressing goroutine each was
	// an OOM/CPU vector. Each goroutine is panic-isolated so one corrupt segment
	// can't crash the server.
	segs := s.segments.SegmentsInRange(*params.Start, *params.End)
	if len(segs) > 0 {
		results := make([][]chunk.Entry, len(segs))
		counts := make([]int, len(segs))
		sem := make(chan struct{}, searchConcurrency)
		var wg sync.WaitGroup
		for i, seg := range segs {
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int, seg *LoadedSegment) {
				defer wg.Done()
				defer func() { <-sem }()
				defer func() {
					if r := recover(); r != nil {
						slog.Error("search: recovered from segment panic", "segment", seg.DirName, "panic", r)
					}
				}()
				entries, err := s.searchSegment(seg, params)
				if err != nil {
					slog.Warn("search: segment error", "segment", seg.DirName, "error", err)
					return
				}
				counts[idx] = len(entries)
				results[idx] = capEntriesByTs(entries, params.SortAsc, perSegLimit)
			}(i, seg)
		}
		wg.Wait()

		for i := range results {
			total += counts[i]
			allMatches = append(allMatches, results[i]...)
		}
	}

	// Query active WAL (linear scan of at most the current hour).
	activeMatches := s.searchActiveWAL(params)
	total += len(activeMatches)
	allMatches = append(allMatches, activeMatches...)

	// Sort by timestamp with a stable ID tiebreak so pagination is deterministic.
	sortEntriesByTs(allMatches, params.SortAsc)

	// Apply offset and limit.
	if params.Offset > 0 && params.Offset < len(allMatches) {
		allMatches = allMatches[params.Offset:]
	} else if params.Offset >= len(allMatches) {
		allMatches = nil
	}
	if len(allMatches) > params.Limit {
		allMatches = allMatches[:params.Limit]
	}

	return &SearchResult{Entries: allMatches, Total: total}, nil
}

// searchConcurrency bounds how many sealed segments are scanned in parallel.
const searchConcurrency = 8

// sortEntriesByTs sorts by timestamp (asc/desc per asc), breaking ties by ID so
// results are deterministic across pages (equal-timestamp rows don't reorder).
func sortEntriesByTs(entries []chunk.Entry, asc bool) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Ts != entries[j].Ts {
			if asc {
				return entries[i].Ts < entries[j].Ts
			}
			return entries[i].Ts > entries[j].Ts
		}
		return entries[i].ID < entries[j].ID
	})
}

// capEntriesByTs returns at most max entries — the most relevant by timestamp
// order — so a single segment can't contribute an unbounded slice to the merge.
func capEntriesByTs(entries []chunk.Entry, asc bool, max int) []chunk.Entry {
	if max <= 0 || len(entries) <= max {
		return entries
	}
	sortEntriesByTs(entries, asc)
	return entries[:max]
}

// GetByID retrieves a single entry by composite ID.
func (s *Store) GetByID(id int64) (*chunk.Entry, error) {
	hour, chunkNum, row := DecodeID(id)

	seg := s.segments.FindSegmentByID(id)
	if seg == nil {
		// Try active WAL
		return s.findInActiveWAL(id)
	}

	chunkPath := filepath.Join(seg.DirPath, fmt.Sprintf("chunk_%03d.col", chunkNum))
	r, err := chunkpkg.OpenReader(chunkPath)
	if err != nil {
		return nil, fmt.Errorf("open chunk for ID %d (hour=%d chunk=%d): %w", id, hour, chunkNum, err)
	}
	defer r.Close()

	if row >= r.EntryCount {
		return nil, fmt.Errorf("row %d out of range (chunk has %d entries)", row, r.EntryCount)
	}

	return readEntryFromChunk(r, row)
}

// GetBody retrieves just the body blob for an entry.
func (s *Store) GetBody(id int64) (json.RawMessage, error) {
	entry, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}
	return entry.Body, nil
}

// segmentFullyInRange reports whether an entire hourly segment lies within
// [start, end], so its precomputed whole-segment totals are exact for the range.
func segmentFullyInRange(hour int64, start, end time.Time) bool {
	segStart := hour * 3600
	segEnd := (hour + 1) * 3600
	return start.Unix() <= segStart && segEnd <= end.Unix()
}

// CountByLevel returns log counts grouped by level for the given time range.
//
// For segments fully inside the range (and with no service/env filter) the
// precomputed per-level totals are exact. Segments that only partially overlap
// the range — or when a service/env filter is set — are scanned and counted
// with per-row time filtering, so sub-hour and non-hour-aligned ranges are no
// longer over-counted by including whole neighbouring hours.
func (s *Store) CountByLevel(start, end time.Time, service, environment string) (map[string]int, error) {
	counts := make(map[string]int)

	segs := s.segments.SegmentsInRange(start, end)
	for _, seg := range segs {
		if service == "" && environment == "" && segmentFullyInRange(seg.Hour, start, end) {
			for level, count := range seg.Meta.Counts.ByLevel {
				counts[level] += count
			}
			continue
		}
		entries, err := s.searchSegment(seg, SearchParams{Start: &start, End: &end, Service: service, Env: environment})
		if err != nil {
			slog.Warn("count: segment scan error", "segment", seg.DirName, "error", err)
			continue
		}
		for i := range entries {
			counts[entries[i].Level]++
		}
	}

	// Active WAL counts (service + env filters applied).
	walCounts := s.countActiveWALByLevel(start, end, service, environment)
	for level, count := range walCounts {
		counts[level] += count
	}

	return counts, nil
}

// CountByService returns log counts grouped by service for the given time range.
// See CountByLevel for the fully-in-range vs boundary-scan strategy.
func (s *Store) CountByService(start, end time.Time, environment string) (map[string]int, error) {
	counts := make(map[string]int)

	segs := s.segments.SegmentsInRange(start, end)
	for _, seg := range segs {
		if environment == "" && segmentFullyInRange(seg.Hour, start, end) {
			for svc, count := range seg.Meta.Counts.ByService {
				counts[svc] += count
			}
			continue
		}
		entries, err := s.searchSegment(seg, SearchParams{Start: &start, End: &end, Env: environment})
		if err != nil {
			slog.Warn("count: segment scan error", "segment", seg.DirName, "error", err)
			continue
		}
		for i := range entries {
			counts[entries[i].Service]++
		}
	}

	walCounts := s.countActiveWALByService(start, end, environment)
	for svc, count := range walCounts {
		counts[svc] += count
	}

	return counts, nil
}

// HistogramBucket represents a time-bucketed count.
type HistogramBucket struct {
	Timestamp time.Time
	Total     int
	Errors    int
}

// Histogram bucketing bounds. A caller-supplied interval down to 1ns over a
// huge range previously produced ~10^17 buckets (CPU pin + OOM from one query).
const (
	maxHistogramBuckets = 10000
	minHistogramInterval = time.Second
)

// Histogram returns per-interval counts for the given time range. Times are
// normalized to UTC (stored minute keys are UTC), the interval is floored at
// minHistogramInterval, and the bucket count is capped by widening the interval
// so a hostile range/interval can't exhaust CPU or memory.
func (s *Store) Histogram(start, end time.Time, interval time.Duration) ([]HistogramBucket, error) {
	start = start.UTC()
	end = end.UTC()
	if !end.After(start) {
		return nil, nil
	}
	if interval < minHistogramInterval {
		interval = minHistogramInterval
	}
	span := end.Sub(start)
	if span/interval > maxHistogramBuckets {
		interval = span / maxHistogramBuckets
		if interval < minHistogramInterval {
			interval = minHistogramInterval
		}
	}

	// Build minute-level counts from meta.json (UTC-keyed).
	minuteCounts := make(map[string]HBucket)
	segs := s.segments.SegmentsInRange(start, end)
	for _, seg := range segs {
		for key, bucket := range seg.Meta.Histogram {
			existing := minuteCounts[key]
			existing.Total += bucket.Total
			existing.Errors += bucket.Errors
			minuteCounts[key] = existing
		}
	}
	s.addActiveWALHistogram(minuteCounts, start, end)

	// Pre-size the bucket slice, then assign each data point to its bucket by
	// index — O(data points), never iterating empty minutes of the span.
	nBuckets := int(span/interval) + 1
	buckets := make([]HistogramBucket, nBuckets)
	for i := range buckets {
		buckets[i].Timestamp = start.Add(time.Duration(i) * interval)
	}
	for key, mb := range minuteCounts {
		minute, err := time.ParseInLocation("2006-01-02T15:04", key, time.UTC)
		if err != nil || minute.Before(start) || !minute.Before(end) {
			continue
		}
		idx := int(minute.Sub(start) / interval)
		if idx >= 0 && idx < nBuckets {
			buckets[idx].Total += mb.Total
			buckets[idx].Errors += mb.Errors
		}
	}

	return buckets, nil
}

// DistinctValues returns unique values for a given column name.
func (s *Store) DistinctValues(column string, start, end time.Time) ([]string, error) {
	// For dict-encoded columns, read from meta.json dictionaries
	dictColumns := map[string]bool{
		"service": true, "level": true, "env": true, "event_type": true,
	}

	if dictColumns[column] {
		seen := make(map[string]bool)
		segs := s.segments.SegmentsInRange(start, end)
		for _, seg := range segs {
			if vals, ok := seg.Meta.Dicts[column]; ok {
				for _, v := range vals {
					if v != "" {
						seen[v] = true
					}
				}
			}
		}
		result := make([]string, 0, len(seen))
		for v := range seen {
			result = append(result, v)
		}
		sort.Strings(result)
		return result, nil
	}

	// Non-dict columns (e.g. error_fingerprint, user_id — used by watch rules)
	// require a scan. Bounded by the hourly segment size and a distinct cap so a
	// watch evaluating a wide window can't exhaust memory.
	if _, ok := scanColumnValue(&chunk.Entry{}, column); !ok {
		return nil, fmt.Errorf("distinct values not supported for column %q", column)
	}
	seen := make(map[string]struct{})
	params := SearchParams{Start: &start, End: &end}
	collect := func(entries []chunk.Entry) bool {
		for i := range entries {
			if v, _ := scanColumnValue(&entries[i], column); v != "" {
				seen[v] = struct{}{}
				if len(seen) >= maxDistinctValues {
					return true
				}
			}
		}
		return false
	}
	for _, seg := range s.segments.SegmentsInRange(start, end) {
		entries, err := s.searchSegment(seg, params)
		if err != nil {
			slog.Warn("distinct: segment scan error", "segment", seg.DirName, "error", err)
			continue
		}
		if collect(entries) {
			break
		}
	}
	if len(seen) < maxDistinctValues {
		collect(s.searchActiveWAL(params))
	}
	result := make([]string, 0, len(seen))
	for v := range seen {
		result = append(result, v)
	}
	sort.Strings(result)
	return result, nil
}

// maxDistinctValues bounds the distinct set collected during a column scan.
const maxDistinctValues = 100000

// scanColumnValue returns the string value of a scannable (non-dict) column for
// an entry, and whether the column is supported for distinct scans.
func scanColumnValue(e *chunk.Entry, column string) (string, bool) {
	switch column {
	case "error_fingerprint":
		return e.ErrorFingerprint, true
	case "error_class":
		return e.ErrorClass, true
	case "user_id":
		return e.UserID, true
	case "tenant_id":
		return e.TenantID, true
	case "trace_id":
		return e.TraceID, true
	case "request_id":
		return e.RequestID, true
	case "path":
		return e.Path, true
	case "handler":
		return e.Handler, true
	case "host":
		return e.Host, true
	default:
		return "", false
	}
}

// Tail subscribes to live log entries.
func (s *Store) Tail() (snapshot []chunk.Entry, ch <-chan []chunk.Entry, unsubscribe func()) {
	snapshot = s.ring.Snapshot()
	ch, unsubscribe = s.ring.Subscribe()
	return
}

// --- Internal query helpers ---

func (s *Store) searchSegment(seg *LoadedSegment, params SearchParams) ([]chunk.Entry, error) {
	var allResults []chunk.Entry

	for _, cm := range seg.Meta.Chunks {
		chunkPath := filepath.Join(seg.DirPath, fmt.Sprintf("chunk_%03d.col", cm.Index))
		idxPath := filepath.Join(seg.DirPath, fmt.Sprintf("chunk_%03d.idx", cm.Index))

		entries, err := s.searchChunk(chunkPath, idxPath, cm.EntryCount, params)
		if err != nil {
			return nil, fmt.Errorf("search chunk %d: %w", cm.Index, err)
		}
		allResults = append(allResults, entries...)
	}

	return allResults, nil
}

func (s *Store) searchChunk(chunkPath, idxPath string, entryCount int, params SearchParams) ([]chunk.Entry, error) {
	// Step 1: If full-text query, use inverted index to get candidate rows
	var candidates map[uint32]bool
	if params.Query != "" {
		idx, err := index.OpenReader(idxPath)
		if err != nil {
			return nil, fmt.Errorf("open index: %w", err)
		}
		rowIDs, err := idx.Search(params.Query)
		if err != nil {
			return nil, fmt.Errorf("index search: %w", err)
		}
		if len(rowIDs) == 0 {
			return nil, nil // no FTS matches
		}
		candidates = make(map[uint32]bool, len(rowIDs))
		for _, id := range rowIDs {
			candidates[id] = true
		}
	}

	// Step 2: Open chunk and filter by column values
	r, err := chunkpkg.OpenReader(chunkPath)
	if err != nil {
		return nil, fmt.Errorf("open chunk: %w", err)
	}
	defer r.Close()

	// Start with all rows (or FTS candidates)
	matchingRows := make([]int, 0)
	if candidates != nil {
		for row := range candidates {
			matchingRows = append(matchingRows, int(row))
		}
		sort.Ints(matchingRows)
	} else {
		matchingRows = make([]int, r.EntryCount)
		for i := range matchingRows {
			matchingRows[i] = i
		}
	}

	// Filter by structured columns
	matchingRows, err = filterByColumn(r, "level", params.Level, matchingRows)
	if err != nil {
		return nil, err
	}
	matchingRows, err = filterByColumn(r, "service", params.Service, matchingRows)
	if err != nil {
		return nil, err
	}
	matchingRows, err = filterByColumn(r, "env", params.Env, matchingRows)
	if err != nil {
		return nil, err
	}
	matchingRows, err = filterBySparseColumn(r, "trace_id", params.TraceID, matchingRows)
	if err != nil {
		return nil, err
	}
	matchingRows, err = filterBySparseColumn(r, "request_id", params.RequestID, matchingRows)
	if err != nil {
		return nil, err
	}
	matchingRows, err = filterBySparseColumn(r, "user_id", params.UserID, matchingRows)
	if err != nil {
		return nil, err
	}
	matchingRows, err = filterBySparseColumn(r, "event_type", params.EventType, matchingRows)
	if err != nil {
		return nil, err
	}
	matchingRows, err = filterBySparseColumn(r, "error_class", params.ErrorClass, matchingRows)
	if err != nil {
		return nil, err
	}
	matchingRows, err = filterBySparseColumn(r, "error_fingerprint", params.ErrorFingerprint, matchingRows)
	if err != nil {
		return nil, err
	}

	// Filter by time range
	if params.Start != nil || params.End != nil {
		matchingRows, err = filterByTimeRange(r, params.Start, params.End, matchingRows)
		if err != nil {
			return nil, err
		}
	}

	if len(matchingRows) == 0 {
		return nil, nil
	}

	// Read full entries for matching rows, then apply the structured filters that
	// aren't column-indexed (method/path/tenant_id/min_duration/n_plus_one). The
	// FTS Query was already resolved via the inverted index above, so clear it.
	filterParams := params
	filterParams.Query = ""
	entries := make([]chunk.Entry, 0, len(matchingRows))
	for _, row := range matchingRows {
		entry, err := readEntryFromChunk(r, row)
		if err != nil {
			continue // skip corrupt entries
		}
		if !matchesParams(entry, filterParams) {
			continue
		}
		entries = append(entries, *entry)
	}

	return entries, nil
}

func (s *Store) searchActiveWAL(params SearchParams) []chunk.Entry {
	walPath := s.writer.WALPath()
	f, err := os.Open(walPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	// A concurrent in-flight append can leave a torn trailing record; ReadEntries
	// returns the fully-parsed entries plus an error. Use the good entries and
	// log rather than discarding the whole hour (which returned zero live logs).
	entries, err := wal.ReadEntries(f)
	if err != nil {
		slog.Warn("search: active WAL had a torn tail; using parsed entries", "error", err, "parsed", len(entries))
	}

	var matches []chunk.Entry
	for _, e := range entries {
		if matchesParams(&e, params) {
			matches = append(matches, e)
		}
	}
	return matches
}

func (s *Store) findInActiveWAL(id int64) (*chunk.Entry, error) {
	walPath := s.writer.WALPath()
	f, err := os.Open(walPath)
	if err != nil {
		return nil, fmt.Errorf("entry not found (no active WAL)")
	}
	defer f.Close()

	// Tolerate a torn trailing record (see searchActiveWAL): scan the parsed
	// entries even if the tail was incomplete.
	entries, err := wal.ReadEntries(f)
	if err != nil {
		slog.Warn("lookup: active WAL had a torn tail; using parsed entries", "error", err, "parsed", len(entries))
	}

	for i := range entries {
		if entries[i].ID == id {
			return &entries[i], nil
		}
	}
	return nil, fmt.Errorf("entry ID %d not found", id)
}

func (s *Store) countActiveWALByLevel(start, end time.Time, service, environment string) map[string]int {
	walPath := s.writer.WALPath()
	f, err := os.Open(walPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	entries, _ := wal.ReadEntries(f)
	counts := make(map[string]int)
	startMs := start.UnixMilli()
	endMs := end.UnixMilli()
	for _, e := range entries {
		if e.ReceivedAt < startMs || e.ReceivedAt > endMs {
			continue
		}
		if service != "" && !strings.EqualFold(e.Service, service) {
			continue
		}
		if !envMatches(environment, e.Env) {
			continue
		}
		counts[e.Level]++
	}
	return counts
}

func (s *Store) countActiveWALByService(start, end time.Time, environment string) map[string]int {
	walPath := s.writer.WALPath()
	f, err := os.Open(walPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	entries, _ := wal.ReadEntries(f)
	counts := make(map[string]int)
	startMs := start.UnixMilli()
	endMs := end.UnixMilli()
	for _, e := range entries {
		if e.ReceivedAt < startMs || e.ReceivedAt > endMs {
			continue
		}
		if !envMatches(environment, e.Env) {
			continue
		}
		counts[e.Service]++
	}
	return counts
}

// envMatches applies the PR 2 legacy-wildcard rule: a filter is satisfied
// when it's empty, when the row's env exactly matches, or when the row's
// env is empty (pre-multi-env row, treated as wildcard).
func envMatches(filter, entryEnv string) bool {
	if filter == "" || entryEnv == "" {
		return true
	}
	return strings.EqualFold(filter, entryEnv)
}

func (s *Store) addActiveWALHistogram(minuteCounts map[string]HBucket, start, end time.Time) {
	walPath := s.writer.WALPath()
	f, err := os.Open(walPath)
	if err != nil {
		return
	}
	defer f.Close()

	entries, _ := wal.ReadEntries(f)
	startMs := start.UnixMilli()
	endMs := end.UnixMilli()
	for _, e := range entries {
		if e.ReceivedAt >= startMs && e.ReceivedAt <= endMs {
			key := time.UnixMilli(e.ReceivedAt).UTC().Format("2006-01-02T15:04")
			bucket := minuteCounts[key]
			bucket.Total++
			if e.Level == "error" || e.Level == "fatal" {
				bucket.Errors++
			}
			minuteCounts[key] = bucket
		}
	}
}

// --- Column filtering helpers ---

func filterByColumn(r *chunkpkg.Reader, colName, value string, rows []int) ([]int, error) {
	if value == "" {
		return rows, nil
	}

	colType := getColumnType(colName)
	var values []string
	var err error

	switch colType {
	case chunkpkg.ColDictBitpack:
		values, err = r.ReadDictBitpack(colName)
	case chunkpkg.ColDictZstd:
		values, err = r.ReadDictZstd(colName)
	default:
		return rows, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read column %s: %w", colName, err)
	}

	// For the env column, rows stored with an empty value predate multi-env
	// enforcement and should match any env filter (see matchesParams comment).
	// No other dict-encoded column has this semantics today.
	legacyWildcard := colName == "env"

	filtered := make([]int, 0, len(rows))
	for _, row := range rows {
		if row >= len(values) {
			continue
		}
		if legacyWildcard && values[row] == "" {
			filtered = append(filtered, row)
			continue
		}
		if strings.EqualFold(values[row], value) {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

func filterBySparseColumn(r *chunkpkg.Reader, colName, value string, rows []int) ([]int, error) {
	if value == "" {
		return rows, nil
	}

	if !r.HasColumn(colName) {
		return nil, nil // column doesn't exist → no matches
	}

	values, err := r.ReadSparseStrings(colName)
	if err != nil {
		return nil, fmt.Errorf("read sparse column %s: %w", colName, err)
	}

	filtered := make([]int, 0, len(rows))
	for _, row := range rows {
		if row < len(values) && strings.EqualFold(values[row], value) {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

func filterByTimeRange(r *chunkpkg.Reader, start, end *time.Time, rows []int) ([]int, error) {
	tsValues, err := r.ReadZstdInt64("ts")
	if err != nil {
		return rows, nil // can't filter, return all
	}

	var startMs, endMs int64
	if start != nil {
		startMs = start.UnixMilli()
	}
	if end != nil {
		endMs = end.UnixMilli()
	} else {
		endMs = time.Now().UnixMilli()
	}

	filtered := make([]int, 0, len(rows))
	for _, row := range rows {
		if row < len(tsValues) {
			ts := tsValues[row]
			if ts >= startMs && ts <= endMs {
				filtered = append(filtered, row)
			}
		}
	}
	return filtered, nil
}

func getColumnType(name string) chunkpkg.ColumnType {
	if idx, ok := chunkpkg.ColumnIndex[name]; ok {
		return chunkpkg.Schema[idx].Type
	}
	return 0
}

// readEntryFromChunk reads all columns for a single row and assembles an Entry.
func readEntryFromChunk(r *chunkpkg.Reader, row int) (*chunk.Entry, error) {
	e := &chunk.Entry{}

	// Read each column type and populate the entry
	if ids, err := r.ReadDeltaInt64("id"); err == nil && row < len(ids) {
		e.ID = ids[row]
	}
	if ts, err := r.ReadZstdInt64("ts"); err == nil && row < len(ts) {
		e.Ts = ts[row]
	}
	if ra, err := r.ReadDeltaInt64("received_at"); err == nil && row < len(ra) {
		e.ReceivedAt = ra[row]
	}
	if levels, err := r.ReadDictBitpack("level"); err == nil && row < len(levels) {
		e.Level = levels[row]
	}
	if services, err := r.ReadDictZstd("service"); err == nil && row < len(services) {
		e.Service = services[row]
	}
	if envs, err := r.ReadDictZstd("env"); err == nil && row < len(envs) {
		e.Env = envs[row]
	}
	if msgs, err := r.ReadZstdBlockStrings("message"); err == nil && row < len(msgs) {
		e.Message = msgs[row]
	}

	// Sparse string columns
	sparseStrings := map[string]*string{
		"version": &e.Version, "host": &e.Host, "kind": &e.Kind,
		"event_type": &e.EventType,
		"trace_id": &e.TraceID, "span_id": &e.SpanID,
		"parent_span_id": &e.ParentSpanID, "request_id": &e.RequestID,
		"user_id": &e.UserID, "tenant_id": &e.TenantID,
		"session_id": &e.SessionID, "method": &e.Method,
		"path": &e.Path, "route": &e.Route, "handler": &e.Handler,
		"error_class": &e.ErrorClass, "error_message": &e.ErrorMessage,
		"source_file": &e.SourceFile, "error_fingerprint": &e.ErrorFingerprint,
		"job_class": &e.JobClass, "job_queue": &e.JobQueue, "job_id": &e.JobID,
		"status": nil, // handled separately
	}
	for col, dest := range sparseStrings {
		if dest == nil {
			continue
		}
		if vals, err := r.ReadSparseStrings(col); err == nil && row < len(vals) {
			*dest = vals[row]
		}
	}

	// Status (stored as sparse dict string, needs int conversion)
	if vals, err := r.ReadSparseStrings("status"); err == nil && row < len(vals) && vals[row] != "" {
		fmt.Sscanf(vals[row], "%d", &e.Status)
	}

	// Sparse int64 columns
	sparseInts := map[string]*int{
		"duration_ms": &e.DurationMs, "db_ms": &e.DbMs,
		"db_count": &e.DbCount, "cache_ms": &e.CacheMs,
		"cache_hits": &e.CacheHits, "cache_misses": &e.CacheMisses,
		"ext_ms": &e.ExtMs, "ext_count": &e.ExtCount,
		"render_ms": &e.RenderMs, "alloc_count": &e.AllocCount,
		"mem_delta_mb": &e.MemDeltaMb,
		"slow_queries": &e.SlowQueries, "dup_queries": &e.DupQueries,
		"source_line": &e.SourceLine, "queue_ms": &e.QueueMs,
	}
	for col, dest := range sparseInts {
		if vals, err := r.ReadSparseInt64(col); err == nil && row < len(vals) && vals[row] != nil {
			*dest = int(*vals[row])
		}
	}

	// Sparse bool
	if vals, err := r.ReadSparseBool("n_plus_one"); err == nil && row < len(vals) {
		e.NPlusOne = vals[row]
	}

	// Body
	if vals, err := r.ReadSparseBytes("body"); err == nil && row < len(vals) && vals[row] != nil {
		e.Body = vals[row]
	}

	return e, nil
}

// matchesParams checks if an entry matches search parameters (used for WAL scan).
func matchesParams(e *chunk.Entry, p SearchParams) bool {
	if p.Service != "" && !strings.EqualFold(e.Service, p.Service) {
		return false
	}
	if p.Level != "" && !strings.EqualFold(e.Level, p.Level) {
		return false
	}
	// Legacy fallback: treat entries with env="" as matching any env filter.
	// Rows ingested before multi-env enforcement don't carry a scope; the
	// SQL backfill rewrites them in bulk, but chunks on disk aren't
	// rewritten until the admin runs the rebuild-logs tool, so we leak
	// them through for now to preserve visibility.
	if p.Env != "" && e.Env != "" && !strings.EqualFold(e.Env, p.Env) {
		return false
	}
	if p.TraceID != "" && e.TraceID != p.TraceID {
		return false
	}
	if p.RequestID != "" && e.RequestID != p.RequestID {
		return false
	}
	if p.UserID != "" && e.UserID != p.UserID {
		return false
	}
	if p.EventType != "" && !strings.EqualFold(e.EventType, p.EventType) {
		return false
	}
	if p.ErrorClass != "" && !strings.EqualFold(e.ErrorClass, p.ErrorClass) {
		return false
	}
	if p.ErrorFingerprint != "" && e.ErrorFingerprint != p.ErrorFingerprint {
		return false
	}
	if p.TenantID != "" && e.TenantID != p.TenantID {
		return false
	}
	if p.Method != "" && !strings.EqualFold(e.Method, p.Method) {
		return false
	}
	if p.Path != "" && !strings.Contains(strings.ToLower(e.Path), strings.ToLower(p.Path)) {
		return false
	}
	if p.MinDurationMs > 0 && e.DurationMs < p.MinDurationMs {
		return false
	}
	if p.NPlusOneOnly && (e.NPlusOne == nil || !*e.NPlusOne) {
		return false
	}
	if p.Query != "" && !strings.Contains(strings.ToLower(e.Message), strings.ToLower(p.Query)) {
		return false
	}
	if p.Start != nil && e.Ts < p.Start.UnixMilli() {
		return false
	}
	if p.End != nil && e.Ts > p.End.UnixMilli() {
		return false
	}
	return true
}
