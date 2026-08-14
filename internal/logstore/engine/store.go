package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	chunkpkg "github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/index"
	"github.com/adham90/opentrace/internal/logstore/ingest"
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

	// pendingMu guards pendingSeals and serializes it with segment
	// registration, so a query never observes a rotated hour as absent from
	// both places (nor present in both).
	pendingMu sync.Mutex
	// pendingSeals maps a segment hour to its rotated sealing_*.wal that has
	// not been registered as a segment yet. Queries scan these files, so a long
	// or failed seal no longer blanks out the hour it is sealing.
	pendingSeals map[int64]string
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
		dataDir:      dataDir,
		writer:       writer,
		segments:     segments,
		ring:         ring,
		pipeline:     pipeline,
		pendingSeals: make(map[int64]string),
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
	written, err := s.writer.Append(processed)
	if !errors.Is(err, ErrSegmentFull) {
		return written, err
	}
	// The segment hour ran out of addressable IDs. Seal it early and retry once
	// into the fresh segment rather than encoding an ID that would decode to a
	// different hour.
	slog.Warn("ingest: segment hour full, sealing early", "max_entries", MaxEntriesPerSegment)
	if serr := s.SealCurrentHour(); serr != nil {
		return nil, fmt.Errorf("seal full segment: %w", serr)
	}
	return s.writer.Append(processed)
}

// SealCurrentHour triggers sealing of the current hour's WAL.
func (s *Store) SealCurrentHour() error {
	s.sealMu.Lock()
	defer s.sealMu.Unlock()

	// Retry anything a previous seal failed on: those files stay queryable but
	// are not segments yet, and nothing else would ever pick them up again
	// while the process lives.
	s.retryPendingSeals()

	sealPath, sealHour, count, err := s.writer.Rotate()
	if err != nil {
		return fmt.Errorf("rotate WAL: %w", err)
	}
	if count == 0 {
		return nil // nothing to seal
	}

	// Publish the rotated file to queries before sealing: it is no longer the
	// active WAL and not yet a segment, so without this the hour is invisible
	// for the whole (potentially long) duration of the seal.
	s.pendingMu.Lock()
	s.pendingSeals[sealHour] = sealPath
	s.pendingMu.Unlock()

	return s.sealPending(sealHour, sealPath)
}

// sealPending seals one rotated WAL and swaps it from the pending set to the
// registered segments atomically, so queries see it in exactly one place.
func (s *Store) sealPending(hour int64, path string) error {
	meta, err := Seal(path, hour)
	if err != nil {
		// Keep it pending: still queryable, and retried on the next seal tick.
		return fmt.Errorf("seal: %w", err)
	}
	s.pendingMu.Lock()
	if meta != nil {
		s.segments.Register(hour, meta)
	}
	delete(s.pendingSeals, hour)
	s.pendingMu.Unlock()
	return nil
}

// retryPendingSeals re-attempts seals that previously failed. Caller holds sealMu.
func (s *Store) retryPendingSeals() {
	s.pendingMu.Lock()
	pending := make(map[int64]string, len(s.pendingSeals))
	for hour, path := range s.pendingSeals {
		pending[hour] = path
	}
	s.pendingMu.Unlock()

	for hour, path := range pending {
		if err := s.sealPending(hour, path); err != nil {
			slog.Error("seal: retry failed", "error", err, "hour", hour)
		}
	}
}

// Prune deletes segments older than the given retention duration.
func (s *Store) Prune(retention time.Duration) (int, error) {
	return s.segments.Prune(retention)
}

// --- Read Operations ---

// SearchParams defines filters for log search.
type SearchParams struct {
	Query            string // full-text search on message
	Service          string // exact match
	Level            string // exact match
	Env              string // exact match
	TraceID          string // exact match
	RequestID        string // exact match
	UserID           string // exact match
	TenantID         string // exact match
	EventType        string // exact match
	ErrorClass       string // exact match
	ErrorFingerprint string // exact match
	SourceFile       string // exact match
	CommitHash       string // exact match
	Method           string // exact match
	Path             string // substring match
	Handler          string // controller/handler; matches "X" against a stored "X#action" too
	MinDurationMs    int    // minimum duration_ms
	// PositiveDurationOnly keeps only rows that carry a real duration
	// (duration_ms > 0). Callers that render request timings used to drop these
	// rows after the engine had already applied the limit, so a full page could
	// come back short while more qualifying rows existed.
	PositiveDurationOnly bool
	MinSQLCount          int  // minimum db_count
	NPlusOneOnly         bool // only n_plus_one entries
	// RequestsOnly restricts results to HTTP request rows. Matching on the
	// event_type string alone dropped request rows whose SDK sent its own
	// event_type, so identity is taken from kind/event_type/request shape.
	RequestsOnly bool
	// MetadataFilter requires every key to be present in the entry body's
	// metadata object with the given (stringified) value.
	MetadataFilter map[string]string
	// Exclude maps a field name to a comma-separated list of values to reject.
	Exclude map[string]string
	Start   *time.Time // event time range start
	End     *time.Time // event time range end
	SinceID int64      // cursor: only entries with a strictly greater id
	Limit   int
	Offset  int
	SortAsc bool // true = oldest first
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
	segs, walPaths := s.queryView(*params.Start, *params.End)
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

	// Query unsealed WALs (live hour + any seal in progress).
	activeMatches := searchWALs(walPaths, params)
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

// ErrEntryNotFound reports that no entry with the requested ID exists. Callers
// (and the store adapter, which maps it to store.ErrNotFound) must be able to
// tell a genuine 404 apart from an I/O failure, which used to be impossible
// because every failure came back as an ad-hoc error string.
var ErrEntryNotFound = errors.New("log entry not found")

// GetByID retrieves a single entry by composite ID.
func (s *Store) GetByID(id int64) (*chunk.Entry, error) {
	hour, chunkNum, row := DecodeID(id)

	seg := s.segments.FindSegmentByID(id)
	if seg == nil {
		// Not in a sealed segment: try the unsealed WALs.
		return findInWALs(s.walPaths(), id)
	}

	chunkPath := filepath.Join(seg.DirPath, fmt.Sprintf("chunk_%03d.col", chunkNum))
	r, err := chunkpkg.OpenReader(chunkPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Retention can delete the segment between the lookup and the open.
			return nil, fmt.Errorf("%w: entry ID %d", ErrEntryNotFound, id)
		}
		return nil, fmt.Errorf("open chunk for ID %d (hour=%d chunk=%d): %w", id, hour, chunkNum, err)
	}
	defer r.Close()

	if row >= r.EntryCount {
		return nil, fmt.Errorf("%w: entry ID %d (row %d of %d)", ErrEntryNotFound, id, row, r.EntryCount)
	}

	return readEntryFromChunk(newChunkColumns(r), row)
}

// GetBody retrieves just the body blob for an entry.
func (s *Store) GetBody(id int64) (json.RawMessage, error) {
	entry, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}
	return entry.Body, nil
}

// segmentFullyInRange reports whether an hourly segment's precomputed
// whole-segment totals are exact for [start, end].
//
// Segment membership is by arrival hour but every other count/search path
// filters on event time (ts), and the two differ for buffered SDK batches and
// skewed clocks. SegmentsInRange already assumes that skew stays within ±1
// hour, so the precomputed totals are only used when the range covers the
// segment's hour with that same one-hour margin on each side; otherwise the
// segment is scanned and counted row by row on ts, matching Search exactly.
func segmentFullyInRange(hour int64, start, end time.Time) bool {
	segStart := hour*3600 - skewMarginSeconds
	segEnd := (hour+1)*3600 + skewMarginSeconds
	return start.Unix() <= segStart && segEnd <= end.Unix()
}

// skewMarginSeconds mirrors the ±1 hour buffer SegmentsInRange applies.
const skewMarginSeconds = 3600

// ServiceCount holds per-service totals for a time range.
type ServiceCount struct {
	Total  int
	Errors int
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

	segs, walPaths := s.queryView(start, end)
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

	// Unsealed counts (service + env filters applied).
	walCounts := countWALsByLevel(walPaths, start, end, service, environment)
	for level, count := range walCounts {
		counts[level] += count
	}

	return counts, nil
}

// CountByService returns total log counts grouped by service.
func (s *Store) CountByService(start, end time.Time, environment string) (map[string]int, error) {
	detailed, err := s.CountByServiceDetailed(start, end, environment)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(detailed))
	for svc, c := range detailed {
		counts[svc] = c.Total
	}
	return counts, nil
}

// CountByServiceDetailed returns per-service totals and error counts for the
// range. The error counts come from the sealed segments' precomputed
// per-service error totals (or a scan for segments sealed before that field
// existed / when a filter applies), so callers no longer have to report every
// service as having zero errors.
// See CountByLevel for the fully-in-range vs boundary-scan strategy.
func (s *Store) CountByServiceDetailed(start, end time.Time, environment string) (map[string]ServiceCount, error) {
	counts := make(map[string]ServiceCount)
	add := func(svc string, total, errs int) {
		c := counts[svc]
		c.Total += total
		c.Errors += errs
		counts[svc] = c
	}

	segs, walPaths := s.queryView(start, end)
	for _, seg := range segs {
		if environment == "" && segmentFullyInRange(seg.Hour, start, end) && seg.Meta.Counts.ByServiceError != nil {
			for svc, count := range seg.Meta.Counts.ByService {
				add(svc, count, seg.Meta.Counts.ByServiceError[svc])
			}
			continue
		}
		entries, err := s.searchSegment(seg, SearchParams{Start: &start, End: &end, Env: environment})
		if err != nil {
			slog.Warn("count: segment scan error", "segment", seg.DirName, "error", err)
			continue
		}
		for i := range entries {
			errs := 0
			if isErrorLevel(entries[i].Level) {
				errs = 1
			}
			add(entries[i].Service, 1, errs)
		}
	}

	for svc, c := range countWALsByService(walPaths, start, end, environment) {
		add(svc, c.Total, c.Errors)
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
	maxHistogramBuckets  = 10000
	minHistogramInterval = time.Second
)

// Histogram returns per-interval counts for the given time range. Times are
// normalized to UTC (stored minute keys are UTC), the interval is floored at
// minHistogramInterval, and the bucket count is capped by widening the interval
// so a hostile range/interval can't exhaust CPU or memory.
func (s *Store) Histogram(start, end time.Time, interval time.Duration) ([]HistogramBucket, error) {
	return s.HistogramFiltered(start, end, interval, HistogramFilter{})
}

// HistogramFilter narrows a histogram to a service, level and/or environment.
// An empty field means "all".
type HistogramFilter struct {
	Service     string
	Level       string
	Environment string
}

func (f HistogramFilter) isZero() bool {
	return f.Service == "" && f.Level == "" && f.Environment == ""
}

func (f HistogramFilter) matches(e *chunk.Entry) bool {
	if f.Service != "" && !strings.EqualFold(e.Service, f.Service) {
		return false
	}
	if f.Level != "" && !strings.EqualFold(e.Level, f.Level) {
		return false
	}
	return envMatches(f.Environment, e.Env)
}

// HistogramFiltered returns per-interval counts, restricted to the given
// service/level/environment. The precomputed per-minute buckets in meta.json
// carry no such breakdown, so a filtered histogram scans the segments instead
// of silently returning every service's counts under the caller's label.
func (s *Store) HistogramFiltered(start, end time.Time, interval time.Duration, filter HistogramFilter) ([]HistogramBucket, error) {
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

	// Build minute-level counts from meta.json (UTC-keyed), or by scanning when
	// a filter is set (meta has no per-service/level breakdown).
	minuteCounts := make(map[string]HBucket)
	segs, walPaths := s.queryView(start, end)
	for _, seg := range segs {
		if filter.isZero() {
			for key, bucket := range seg.Meta.Histogram {
				existing := minuteCounts[key]
				existing.Total += bucket.Total
				existing.Errors += bucket.Errors
				minuteCounts[key] = existing
			}
			continue
		}
		s.addSegmentHistogram(minuteCounts, seg, start, end, filter)
	}
	addWALHistogram(minuteCounts, walPaths, start, end, filter)

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

// addSegmentHistogram folds a sealed segment's matching rows into the minute
// buckets, keyed by received_at like the precomputed buckets are.
func (s *Store) addSegmentHistogram(minuteCounts map[string]HBucket, seg *LoadedSegment, start, end time.Time, filter HistogramFilter) {
	entries, err := s.searchSegment(seg, SearchParams{
		Start:   &start,
		End:     &end,
		Service: filter.Service,
		Level:   filter.Level,
		Env:     filter.Environment,
	})
	if err != nil {
		slog.Warn("histogram: segment scan error", "segment", seg.DirName, "error", err)
		return
	}
	for i := range entries {
		key := time.UnixMilli(entries[i].ReceivedAt).UTC().Format("2006-01-02T15:04")
		bucket := minuteCounts[key]
		bucket.Total++
		if isErrorLevel(entries[i].Level) {
			bucket.Errors++
		}
		minuteCounts[key] = bucket
	}
}

// DistinctValues returns unique values for a given column name.
// The service/level/env filters scope the result the same way Search does — a
// watcher's COUNT DISTINCT for one service, level and environment must not be
// computed across every service, level and environment.
func (s *Store) DistinctValues(column string, start, end time.Time, service, level, environment string) ([]string, error) {
	segs, walPaths := s.queryView(start, end)

	// For dict-encoded columns, read from meta.json dictionaries
	dictColumns := map[string]bool{
		"service": true, "level": true, "env": true, "event_type": true,
	}

	if dictColumns[column] {
		return s.distinctDictValues(column, segs, walPaths, start, end, service, level, environment)
	}

	// Non-dict columns (e.g. error_fingerprint, user_id — used by watch rules)
	// require a scan. Bounded by the hourly segment size and a distinct cap so a
	// watch evaluating a wide window can't exhaust memory.
	if _, ok := scanColumnValue(&chunk.Entry{}, column); !ok {
		return nil, fmt.Errorf("distinct values not supported for column %q", column)
	}
	seen := make(map[string]struct{})
	params := SearchParams{Start: &start, End: &end, Service: service, Level: level, Env: environment}
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
	for _, seg := range segs {
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
		collect(searchWALs(walPaths, params))
	}
	return sortedKeys(seen)
}

// distinctDictValues resolves dict-encoded columns. Sealed segments answer from
// their meta.json dictionaries when no service/env filter applies; otherwise
// (and always for the unsealed hour, whose values are in no dictionary yet) the
// rows are scanned. Skipping the unsealed hour hid every value that first
// appeared in it — a service deployed at 10:05 was missing from the service
// list until the 11:00 seal.
func (s *Store) distinctDictValues(column string, segs []*LoadedSegment, walPaths []string, start, end time.Time, service, level, environment string) ([]string, error) {
	seen := make(map[string]struct{})
	params := SearchParams{Start: &start, End: &end, Service: service, Level: level, Env: environment}
	filtered := service != "" || level != "" || environment != ""

	for _, seg := range segs {
		if !filtered {
			for _, v := range seg.Meta.Dicts[column] {
				if v != "" {
					seen[v] = struct{}{}
				}
			}
			continue
		}
		entries, err := s.searchSegment(seg, params)
		if err != nil {
			slog.Warn("distinct: segment scan error", "segment", seg.DirName, "error", err)
			continue
		}
		for i := range entries {
			if v := dictColumnValue(&entries[i], column); v != "" {
				seen[v] = struct{}{}
			}
		}
	}

	forEachWALEntry(walPaths, func(e *chunk.Entry) bool {
		if !matchesParams(e, params) {
			return true
		}
		if v := dictColumnValue(e, column); v != "" {
			seen[v] = struct{}{}
		}
		return len(seen) < maxDistinctValues
	})

	return sortedKeys(seen)
}

// dictColumnValue returns an entry's value for a dict-encoded column.
func dictColumnValue(e *chunk.Entry, column string) string {
	switch column {
	case "service":
		return e.Service
	case "level":
		return e.Level
	case "env":
		return e.Env
	case "event_type":
		return e.EventType
	default:
		return ""
	}
}

// sortedKeys returns a set's members in sorted order.
func sortedKeys(set map[string]struct{}) ([]string, error) {
	result := make([]string, 0, len(set))
	for v := range set {
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

// Tail subscribes to live log entries. The snapshot and the subscription are
// taken atomically, so no batch can slip between them; an entry may appear both
// in the snapshot and on the channel, so consumers dedupe by ID.
func (s *Store) Tail() (snapshot []chunk.Entry, ch <-chan []chunk.Entry, unsubscribe func()) {
	return s.ring.SnapshotAndSubscribe()
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

	// Decode each column at most once for this scan; the cache dies with the
	// function, so nothing is retained past the query.
	cols := newChunkColumns(r)

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

	matchingRows, err = applyColumnFilters(cols, params, matchingRows)
	if err != nil {
		return nil, err
	}

	if len(matchingRows) == 0 {
		return nil, nil
	}

	// Read full entries for matching rows, then apply the structured filters that
	// aren't column-indexed (method/path/tenant_id/min_duration/n_plus_one). The
	// FTS Query was already resolved via the inverted index above, so clear it.
	filterParams := params
	filterParams.Query = ""

	// Narrow to the rows that can actually survive into the caller's page before
	// materializing anything. Every row here becomes a full entry — a dozen-odd
	// column reads each — and the caller then throws all but offset+limit of them
	// away. On an unfiltered query that meant materializing every row of every
	// hour in range: a 24h summary over a normal day's volume ran for minutes and
	// timed out, on a query whose answer is 2000 rows.
	//
	// Only safe when no post-read filter can reject a row, since those decide
	// which rows survive and are only knowable after the read.
	if limit := params.Offset + params.Limit; limit > 0 && len(matchingRows) > limit && !needsPostReadFilter(filterParams) {
		matchingRows, err = topRowsByTs(cols, matchingRows, params.SortAsc, limit)
		if err != nil {
			return nil, err
		}
	}

	entries := make([]chunk.Entry, 0, len(matchingRows))
	skipped := 0
	var firstErr error
	defer func() {
		if skipped > 0 {
			// Never silent: a chunk that can't be decoded is missing data, and
			// the caller is reading the result as the whole answer.
			slog.Warn("search: skipped unreadable rows", "chunk", filepath.Base(chunkPath), "rows", skipped, "error", firstErr)
		}
	}()
	for _, row := range matchingRows {
		entry, err := readEntryFromChunk(cols, row)
		if err != nil {
			skipped++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !matchesParams(entry, filterParams) {
			continue
		}
		entries = append(entries, *entry)
	}

	return entries, nil
}

// applyColumnFilters narrows a row set using the column-indexed predicates and
// the time range, without materializing any entry.
func applyColumnFilters(c *chunkColumns, params SearchParams, rows []int) ([]int, error) {
	dictFilters := []struct{ col, value string }{
		{"level", params.Level},
		{"service", params.Service},
		{"env", params.Env},
	}
	var err error
	for _, f := range dictFilters {
		if rows, err = filterByColumn(c, f.col, f.value, rows); err != nil {
			return nil, err
		}
	}

	sparseFilters := []struct{ col, value string }{
		{"trace_id", params.TraceID},
		{"request_id", params.RequestID},
		{"user_id", params.UserID},
		{"event_type", params.EventType},
		{"error_class", params.ErrorClass},
		{"error_fingerprint", params.ErrorFingerprint},
	}
	for _, f := range sparseFilters {
		if rows, err = filterBySparseColumn(c, f.col, f.value, rows); err != nil {
			return nil, err
		}
	}

	if params.Start != nil || params.End != nil {
		if rows, err = filterByTimeRange(c, params.Start, params.End, rows); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// needsPostReadFilter reports whether matchesParams can still reject a row after
// it is read. The column-indexed filters (level/service/env/trace/request/user/
// event_type/error_class/error_fingerprint) and the time range have already been
// applied to the row set by this point; these are the ones that have not.
func needsPostReadFilter(p SearchParams) bool {
	return p.Method != "" ||
		p.Path != "" ||
		p.Handler != "" ||
		p.TenantID != "" ||
		p.SourceFile != "" ||
		p.CommitHash != "" ||
		p.MinDurationMs > 0 ||
		p.PositiveDurationOnly ||
		p.MinSQLCount > 0 ||
		p.NPlusOneOnly ||
		p.RequestsOnly ||
		p.SinceID > 0 ||
		len(p.MetadataFilter) > 0 ||
		len(p.Exclude) > 0
}

// topRowsByTs picks the n rows that sort first by timestamp, reading only the ts
// column rather than materializing entries. Returns rows in ascending row order
// so the caller's sequential read stays cache-friendly; final ordering is applied
// by the caller once segments are merged.
// A failed ts read is reported rather than silently answered with an unordered
// row set: the caller would present the wrong rows as "the newest N".
func topRowsByTs(c *chunkColumns, rows []int, asc bool, n int) ([]int, error) {
	tsValues, err := c.zstdInt64("ts")
	if err != nil {
		return nil, fmt.Errorf("read ts column for ordering: %w", err)
	}
	return selectTopRows(tsValues, rows, asc, n), nil
}

// selectTopRows picks the n rows that sort first by timestamp and returns them
// in ascending row order, so the caller's sequential read stays in order.
func selectTopRows(tsValues []int64, rows []int, asc bool, n int) []int {
	if n <= 0 || len(rows) <= n {
		return rows
	}

	ordered := make([]int, len(rows))
	copy(ordered, rows)
	sort.Slice(ordered, func(i, j int) bool {
		ti, tj := tsAt(tsValues, ordered[i]), tsAt(tsValues, ordered[j])
		if ti != tj {
			if asc {
				return ti < tj
			}
			return ti > tj
		}
		// Stable tiebreak on row order, mirroring the ID tiebreak the caller
		// applies after the merge (row order is id order within a chunk).
		if asc {
			return ordered[i] < ordered[j]
		}
		return ordered[i] > ordered[j]
	})

	ordered = ordered[:n]
	sort.Ints(ordered)
	return ordered
}

// tsAt reads a row's timestamp, treating a short column as "no timestamp" rather
// than panicking on a truncated chunk.
func tsAt(tsValues []int64, row int) int64 {
	if row < 0 || row >= len(tsValues) {
		return 0
	}
	return tsValues[row]
}

// searchWALs scans the given WAL files (live + sealing-in-progress) for entries
// matching params.
func searchWALs(paths []string, params SearchParams) []chunk.Entry {
	var matches []chunk.Entry
	forEachWALEntry(paths, func(e *chunk.Entry) bool {
		if matchesParams(e, params) {
			matches = append(matches, *e)
		}
		return true
	})
	return matches
}

func findInWALs(paths []string, id int64) (*chunk.Entry, error) {
	var found *chunk.Entry
	forEachWALEntry(paths, func(e *chunk.Entry) bool {
		if e.ID == id {
			cp := *e
			found = &cp
			return false
		}
		return true
	})
	if found == nil {
		return nil, fmt.Errorf("%w: entry ID %d", ErrEntryNotFound, id)
	}
	return found, nil
}

// countWALsByLevel counts unsealed entries per level.
//
// Time filtering uses the event timestamp (ts), the same column Search and the
// sealed scans use. It used to filter on ReceivedAt here only, so a buffered
// SDK batch was counted while the hour was live and dropped from the same
// window once the hour sealed — counts silently changed hours after the fact
// and never reconciled with Search.
func countWALsByLevel(paths []string, start, end time.Time, service, environment string) map[string]int {
	counts := make(map[string]int)
	forEachWALEntry(paths, func(e *chunk.Entry) bool {
		if !walEntryInScope(e, start, end, service, environment) {
			return true
		}
		counts[e.Level]++
		return true
	})
	return counts
}

func countWALsByService(paths []string, start, end time.Time, environment string) map[string]ServiceCount {
	counts := make(map[string]ServiceCount)
	forEachWALEntry(paths, func(e *chunk.Entry) bool {
		if !walEntryInScope(e, start, end, "", environment) {
			return true
		}
		c := counts[e.Service]
		c.Total++
		if isErrorLevel(e.Level) {
			c.Errors++
		}
		counts[e.Service] = c
		return true
	})
	return counts
}

// walEntryInScope applies the shared time/service/env predicate used by the
// unsealed-count paths.
func walEntryInScope(e *chunk.Entry, start, end time.Time, service, environment string) bool {
	if e.Ts < start.UnixMilli() || e.Ts > end.UnixMilli() {
		return false
	}
	if service != "" && !strings.EqualFold(e.Service, service) {
		return false
	}
	return envMatches(environment, e.Env)
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

// addWALHistogram folds unsealed entries into the minute buckets, honouring the
// service/level/env filters the caller asked for.
func addWALHistogram(minuteCounts map[string]HBucket, paths []string, start, end time.Time, f HistogramFilter) {
	startMs := start.UnixMilli()
	endMs := end.UnixMilli()
	forEachWALEntry(paths, func(e *chunk.Entry) bool {
		if e.ReceivedAt < startMs || e.ReceivedAt > endMs {
			return true
		}
		if !f.matches(e) {
			return true
		}
		key := time.UnixMilli(e.ReceivedAt).UTC().Format("2006-01-02T15:04")
		bucket := minuteCounts[key]
		bucket.Total++
		if isErrorLevel(e.Level) {
			bucket.Errors++
		}
		minuteCounts[key] = bucket
		return true
	})
}

// --- Column filtering helpers ---

func filterByColumn(c *chunkColumns, colName, value string, rows []int) ([]int, error) {
	if value == "" {
		return rows, nil
	}

	colType := getColumnType(colName)
	var values []string
	var err error

	switch colType {
	case chunkpkg.ColDictBitpack:
		values, err = c.dictBitpack(colName)
	case chunkpkg.ColDictZstd:
		values, err = c.dictZstd(colName)
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

func filterBySparseColumn(c *chunkColumns, colName, value string, rows []int) ([]int, error) {
	if value == "" {
		return rows, nil
	}

	if !c.hasColumn(colName) {
		return nil, nil // column doesn't exist → no matches
	}

	values, err := c.sparseStrings(colName)
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

// filterByTimeRange narrows rows to those whose ts falls inside [start, end].
//
// A failed ts read is fatal, not "return everything": silently skipping the
// filter answers a bounded question with out-of-range rows, and the caller
// cannot tell that from a genuine result.
func filterByTimeRange(c *chunkColumns, start, end *time.Time, rows []int) ([]int, error) {
	tsValues, err := c.zstdInt64("ts")
	if err != nil {
		return nil, fmt.Errorf("read ts column for time filter: %w", err)
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
// Any read error (other than an absent column) aborts and is returned, so a
// corrupt chunk can never masquerade as an entry full of empty fields.
func readEntryFromChunk(cols *chunkColumns, row int) (*chunk.Entry, error) {
	e := &chunk.Entry{}
	c := &chunkRow{c: cols, row: row}

	c.i64("id", cols.deltaInt64, &e.ID)
	c.i64("ts", cols.zstdInt64, &e.Ts)
	c.i64("received_at", cols.deltaInt64, &e.ReceivedAt)
	c.str("level", cols.dictBitpack, &e.Level)
	c.str("service", cols.dictZstd, &e.Service)
	c.str("env", cols.dictZstd, &e.Env)
	c.str("message", cols.zstdBlockStrings, &e.Message)
	// kind is a dict column (schema.go) and must be read with the dict reader.
	// It was listed among the sparse-string columns, so the dict bytes were
	// parsed with the wrong decoder and Kind came back empty from every chunk.
	c.str("kind", cols.dictZstd, &e.Kind)

	sparseStrings := map[string]*string{
		"version": &e.Version, "host": &e.Host,
		"event_type": &e.EventType,
		"trace_id":   &e.TraceID, "span_id": &e.SpanID,
		"parent_span_id": &e.ParentSpanID, "request_id": &e.RequestID,
		"user_id": &e.UserID, "tenant_id": &e.TenantID,
		"session_id": &e.SessionID, "method": &e.Method,
		"path": &e.Path, "route": &e.Route, "handler": &e.Handler,
		"error_class": &e.ErrorClass, "error_message": &e.ErrorMessage,
		"source_file": &e.SourceFile, "error_fingerprint": &e.ErrorFingerprint,
		"job_class": &e.JobClass, "job_queue": &e.JobQueue, "job_id": &e.JobID,
	}
	for col, dest := range sparseStrings {
		c.str(col, cols.sparseStrings, dest)
	}

	// Status (stored as sparse dict string, needs int conversion)
	var status string
	c.str("status", cols.sparseStrings, &status)
	if status != "" {
		if v, err := strconv.Atoi(status); err == nil {
			e.Status = v
		}
	}

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
		c.sparseInt(col, dest)
	}

	c.sparseBool("n_plus_one", &e.NPlusOne)

	var body []byte
	c.sparseBytes("body", &body)
	if body != nil {
		e.Body = body
	}

	if c.err != nil {
		return nil, c.err
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
	// SourceFile, CommitHash and SinceID reach the engine but had no clause here,
	// so they were accepted and ignored. A dropped filter is worse than an
	// unsupported one: the caller gets a full result set and reads it as the
	// answer to the narrower question it asked.
	if p.SourceFile != "" && !strings.EqualFold(e.SourceFile, p.SourceFile) {
		return false
	}
	// The commit is stored in the columnar entry's Version field (see the
	// adapter's CommitHash <-> Version mapping).
	if p.CommitHash != "" && !strings.EqualFold(e.Version, p.CommitHash) {
		return false
	}
	// Cursor pagination: strictly greater, so a poller that passes back the last
	// id it saw doesn't receive that row again on every tick.
	if p.SinceID > 0 && e.ID <= p.SinceID {
		return false
	}
	if p.Method != "" && !strings.EqualFold(e.Method, p.Method) {
		return false
	}
	if p.Path != "" && !strings.Contains(strings.ToLower(e.Path), strings.ToLower(p.Path)) {
		return false
	}
	if p.RequestsOnly && !isRequestEntry(e) {
		return false
	}
	if p.Handler != "" && !handlerMatches(e.Handler, p.Handler) {
		return false
	}
	if p.MinDurationMs > 0 && e.DurationMs < p.MinDurationMs {
		return false
	}
	if p.PositiveDurationOnly && e.DurationMs <= 0 {
		return false
	}
	if p.MinSQLCount > 0 && e.DbCount < p.MinSQLCount {
		return false
	}
	if p.NPlusOneOnly && (e.NPlusOne == nil || !*e.NPlusOne) {
		return false
	}
	// Full-text uses the same token-AND semantics as the inverted index used for
	// sealed chunks. A raw substring match here made one query return different
	// rows for the live hour than for the sealed hours, and results changed
	// under the caller at the seal boundary.
	if p.Query != "" && !messageMatchesQuery(e.Message, p.Query) {
		return false
	}
	if !matchesMetadata(e, p.MetadataFilter) {
		return false
	}
	if !matchesExclude(e, p.Exclude) {
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
