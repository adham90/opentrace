package engine

import (
	"fmt"
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
	params = params.withDefaultRange()
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
		out = append(out, searchWALs(s.walCache, walPaths, params)...)
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
	// Only the top offset+limit rows can survive, so each sealed chunk is
	// narrowed to its own best offset+limit by reading the sort column alone.
	// Materializing every matching row first meant a 24h "20 slowest endpoints"
	// call built hundreds of thousands of 600-byte entries to return twenty.
	want := offset + limit
	entries, _, err := s.collectMatchingTop(params, requestSortColumn(sortBy), want)
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

// requestSortColumn maps a sortRequestEntries key to the column holding it.
func requestSortColumn(sortBy string) string {
	switch sortBy {
	case "sql_count":
		return "db_count"
	case "db_time_ms":
		return "db_ms"
	case "duplicate_queries":
		return "dup_queries"
	default:
		return "duration_ms"
	}
}

// collectMatchingTop is collectMatching that keeps only the rows with the
// highest values in column, at most n per chunk. n <= 0 means "everything",
// which is collectMatching exactly.
func (s *Store) collectMatchingTop(params SearchParams, column string, n int) ([]chunk.Entry, bool, error) {
	if n <= 0 {
		return s.collectMatching(params)
	}
	params = params.withDefaultRange()
	scan := params
	scan.Limit, scan.Offset = 0, 0

	segs, walPaths := s.queryView(*scan.Start, *scan.End)

	var out []chunk.Entry
	truncated := false
	for _, seg := range segs {
		if len(out) >= maxScanRows {
			truncated = true
			break
		}
		err := s.scanSegment(seg, scan, func(cols *chunkColumns, rows []int) error {
			top, err := topRowsByColumn(cols, rows, column, n)
			if err != nil {
				return err
			}
			appendRows(cols, top, &out)
			return nil
		})
		if err != nil {
			slog.Warn("scan: segment error", "segment", seg.DirName, "error", err)
		}
	}
	if !truncated {
		out = append(out, searchWALs(s.walCache, walPaths, scan)...)
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

// topRowsByColumn returns the n rows with the largest values in a sparse int
// column, in ascending row order so the materializing read stays sequential.
func topRowsByColumn(c *chunkColumns, rows []int, column string, n int) ([]int, error) {
	if len(rows) <= n {
		return rows, nil
	}
	values, err := c.sparseInt64(column)
	if err != nil {
		return nil, fmt.Errorf("read column %s for ordering: %w", column, err)
	}
	at := func(row int) int64 {
		if row < len(values) && values[row] != nil {
			return *values[row]
		}
		return 0
	}
	ordered := make([]int, len(rows))
	copy(ordered, rows)
	sort.Slice(ordered, func(i, j int) bool {
		vi, vj := at(ordered[i]), at(ordered[j])
		if vi != vj {
			return vi > vj
		}
		// Row order is ID order within a chunk, matching the ID tiebreak the
		// caller applies after the merge.
		return ordered[i] < ordered[j]
	})
	ordered = ordered[:n]
	sort.Ints(ordered)
	return ordered, nil
}

// appendRows materializes rows onto out, filling each entry in place. A row
// that cannot be decoded is dropped with a warning rather than failing the
// whole scan: the rest of the range is still a better answer than nothing.
func appendRows(c *chunkColumns, rows []int, out *[]chunk.Entry) {
	base := len(*out)
	*out = append(*out, make([]chunk.Entry, len(rows))...)
	n := base
	for _, row := range rows {
		if err := fillEntryFromChunkProjected(c, row, &(*out)[n], false); err != nil {
			slog.Warn("scan: skipped unreadable row", "row", row, "error", err)
			continue
		}
		n++
	}
	*out = (*out)[:n]
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
//
// Sealed rows are summed straight out of the four numeric columns involved.
// Building an entry per row to add up five integers meant decoding ~45 columns
// and allocating a 600-byte struct for every request in the range, which is
// what made the performance summary the slowest tool in the set.
func (s *Store) AggregateRequests(params SearchParams) (RequestAggregate, error) {
	params = params.withDefaultRange()
	scan := params
	scan.Limit, scan.Offset = 0, 0

	agg := RequestAggregate{}
	segs, walPaths := s.queryView(*scan.Start, *scan.End)
	scanned := 0

	for _, seg := range segs {
		if scanned >= maxScanRows {
			agg.Truncated = true
			break
		}
		err := s.scanSegment(seg, scan, func(cols *chunkColumns, rows []int) error {
			durations, err := cols.sparseInt64("duration_ms")
			if err != nil {
				return err
			}
			dbCounts, err := cols.sparseInt64("db_count")
			if err != nil {
				return err
			}
			hits, err := cols.sparseInt64("cache_hits")
			if err != nil {
				return err
			}
			misses, err := cols.sparseInt64("cache_misses")
			if err != nil {
				return err
			}
			at := func(v []*int64, row int) int {
				if row < len(v) && v[row] != nil {
					return int(*v[row])
				}
				return 0
			}
			for _, row := range rows {
				scanned++
				d := at(durations, row)
				if d <= 0 {
					continue
				}
				agg.Count++
				agg.TotalDurationMs += float64(d)
				agg.TotalSQLCount += float64(at(dbCounts, row))
				agg.CacheHits += at(hits, row)
				agg.CacheReads += at(hits, row) + at(misses, row)
			}
			return nil
		})
		if err != nil {
			slog.Warn("scan: segment error", "segment", seg.DirName, "error", err)
		}
	}

	if !agg.Truncated {
		for _, e := range searchWALs(s.walCache, walPaths, scan) {
			if e.DurationMs <= 0 {
				continue
			}
			agg.Count++
			agg.TotalDurationMs += float64(e.DurationMs)
			agg.TotalSQLCount += float64(e.DbCount)
			agg.CacheHits += e.CacheHits
			agg.CacheReads += e.CacheHits + e.CacheMisses
		}
	}
	if agg.Truncated {
		slog.Warn("scan: result truncated", "max_rows", maxScanRows)
	}
	return agg, nil
}

// withDefaultRange fills in the default one-hour window when the caller left the
// range open.
func (p SearchParams) withDefaultRange() SearchParams {
	now := time.Now().UTC()
	if p.Start == nil {
		start := now.Add(-time.Hour)
		p.Start = &start
	}
	if p.End == nil {
		p.End = &now
	}
	return p
}
