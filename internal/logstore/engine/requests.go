package engine

import (
	"log/slog"
	"sort"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

// maxScanRows bounds how many matching rows a whole-range scan will hold in
// memory. Sorting "slowest first" or averaging over a range is only correct if
// the whole range is examined, but it must not be able to OOM the server.
const maxScanRows = 500000

// RequestAggregate holds whole-range aggregates for request entries.
type RequestAggregate struct {
	Count           int
	TotalDurationMs float64
	TotalSQLCount   float64
	CacheReads      int
	CacheHits       int
	// Truncated reports that the scan hit maxScanRows and the aggregate covers
	// only part of the range.
	Truncated bool
}

// collectMatching returns every entry matching params across sealed segments
// and unsealed WALs, ignoring Limit/Offset. The second result reports that the
// scan was truncated at maxScanRows.
func (s *Store) collectMatching(params SearchParams) ([]chunk.Entry, bool, error) {
	now := time.Now().UTC()
	if params.Start == nil {
		start := now.Add(-time.Hour)
		params.Start = &start
	}
	if params.End == nil {
		params.End = &now
	}
	// A per-segment cap would defeat the point: the answer depends on rows from
	// anywhere in the range, not on the newest ones.
	params.Limit = 0
	params.Offset = 0

	segs, walPaths := s.queryView(*params.Start, *params.End)

	var out []chunk.Entry
	truncated := false
	for _, seg := range segs {
		if len(out) >= maxScanRows {
			truncated = true
			break
		}
		entries, err := s.searchSegment(seg, params)
		if err != nil {
			slog.Warn("scan: segment error", "segment", seg.DirName, "error", err)
			continue
		}
		out = append(out, entries...)
	}
	if !truncated {
		out = append(out, searchWALs(walPaths, params)...)
	}
	if len(out) > maxScanRows {
		out = out[:maxScanRows]
		truncated = true
	}
	if truncated {
		slog.Warn("scan: result truncated", "max_rows", maxScanRows)
	}
	return out, truncated, nil
}

// SearchRequests returns request entries matching params, ordered by sortBy
// across the whole range before limit/offset are applied.
//
// The caller used to limit at the engine and sort afterwards, which turned
// "the 20 slowest requests in 24h" into "the 20 most recent requests, reordered"
// and made filters like n_plus_one_only examine only that recent sample.
func (s *Store) SearchRequests(params SearchParams, sortBy string, limit, offset int) ([]chunk.Entry, error) {
	entries, _, err := s.collectMatching(params)
	if err != nil {
		return nil, err
	}
	sortRequestEntries(entries, sortBy)

	if offset > 0 {
		if offset >= len(entries) {
			return nil, nil
		}
		entries = entries[offset:]
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// sortRequestEntries orders entries by a request metric, descending, with a
// stable ID tiebreak.
func sortRequestEntries(entries []chunk.Entry, sortBy string) {
	key := func(e *chunk.Entry) int {
		switch sortBy {
		case "sql_count":
			return e.DbCount
		case "db_time_ms":
			return e.DbMs
		case "duplicate_queries":
			return e.DupQueries
		default: // duration_ms
			return e.DurationMs
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		ki, kj := key(&entries[i]), key(&entries[j])
		if ki != kj {
			return ki > kj
		}
		return entries[i].ID < entries[j].ID
	})
}

// AggregateRequests computes whole-range aggregates for request entries.
func (s *Store) AggregateRequests(params SearchParams) (RequestAggregate, error) {
	entries, truncated, err := s.collectMatching(params)
	if err != nil {
		return RequestAggregate{}, err
	}

	agg := RequestAggregate{Truncated: truncated}
	for i := range entries {
		e := &entries[i]
		if e.DurationMs <= 0 {
			continue
		}
		agg.Count++
		agg.TotalDurationMs += float64(e.DurationMs)
		agg.TotalSQLCount += float64(e.DbCount)
		agg.CacheHits += e.CacheHits
		agg.CacheReads += e.CacheHits + e.CacheMisses
	}
	return agg, nil
}
