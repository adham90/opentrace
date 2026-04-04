package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

// NewLogStore creates a SQLite-backed log store that inserts directly into
// the logs + request_summaries tables. Used by analytics/trend/error_impact
// tests that need data in SQLite tables for their SQL JOIN queries.
//
// This is NOT the production log store (which is the segmented engine).
// It only exists so that tests for SQLite-only stores (analytics, trends)
// can seed data into the tables they query.
func NewLogStore(db *bun.DB) store.LogStore {
	return &testLogStore{db: db}
}

// testLogStore inserts directly into SQLite for test seeding.
type testLogStore struct {
	db *bun.DB
}

func (s *testLogStore) BatchInsert(ctx context.Context, entries []store.LogEntry) (int, error) {
	for i := range entries {
		e := &entries[i]
		meta, _ := json.Marshal(e.Metadata)
		ts := e.Timestamp.UTC().Format(time.RFC3339Nano)
		res, err := s.db.ExecContext(ctx,
			`INSERT INTO logs (timestamp, level, service, environment, commit_hash,
			trace_id, span_id, parent_span_id, request_id, user_id,
			message, event_type, exception_class, error_fingerprint,
			source_file, source_line, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ts, e.Level, e.Service, e.Environment, e.CommitHash,
			e.TraceID, e.SpanID, e.ParentSpanID, e.RequestID, e.UserID,
			e.Message, e.EventType, e.ExceptionClass, e.ErrorFingerprint,
			e.SourceFile, e.SourceLine, string(meta))
		if err != nil {
			return 0, fmt.Errorf("insert log: %w", err)
		}
		logID, _ := res.LastInsertId()
		e.ID = logID

		if e.RequestSummary != nil {
			rs := e.RequestSummary
			npo := 0
			if rs.NPlusOne {
				npo = 1
			}
			s.db.ExecContext(ctx,
				`INSERT INTO request_summaries (log_id, controller, action, method, path, status,
				duration_ms, db_time_ms, view_time_ms, sql_count, sql_total_ms, sql_slowest_ms,
				sql_slowest_name, n_plus_one, view_count, view_total_ms, view_slowest_ms,
				view_slowest_template, cache_reads, cache_hits, cache_writes, cache_hit_ratio,
				http_external_count, http_external_total_ms, http_slowest_ms, http_slowest_host,
				memory_before_mb, memory_after_mb, memory_delta_mb, timeline,
				time_breakdown, duplicate_queries, worst_duplicate_count, top_duplicates)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				logID, rs.Controller, rs.Action, rs.Method, rs.Path, rs.Status,
				rs.DurationMs, rs.DBTimeMs, rs.ViewTimeMs,
				rs.SQLCount, rs.SQLTotalMs, rs.SQLSlowestMs, rs.SQLSlowestName, npo,
				rs.ViewCount, rs.ViewTotalMs, rs.ViewSlowestMs, rs.ViewSlowestTemplate,
				rs.CacheReads, rs.CacheHits, rs.CacheWrites, rs.CacheHitRatio,
				rs.HTTPExternalCount, rs.HTTPExternalTotalMs, rs.HTTPSlowestMs, rs.HTTPSlowestHost,
				rs.MemoryBeforeMb, rs.MemoryAfterMb, rs.MemoryDeltaMb, rs.Timeline,
				rs.TimeBreakdown, rs.DuplicateQueries, rs.WorstDuplicateCount, rs.TopDuplicates)
		}
	}
	return len(entries), nil
}

func (s *testLogStore) Search(_ context.Context, _ store.LogSearchParams) ([]store.LogEntry, error) {
	return nil, nil
}
func (s *testLogStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }
func (s *testLogStore) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) {
	return nil, nil
}
func (s *testLogStore) CountByService(_ context.Context, _ store.LogCountParams) ([]store.ServiceLogCount, error) {
	return nil, nil
}
func (s *testLogStore) Histogram(_ context.Context, _ store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	return nil, nil
}
func (s *testLogStore) DistinctValues(_ context.Context, _ string, _ store.LogCountParams) ([]string, error) {
	return nil, nil
}
func (s *testLogStore) MetadataKeys(_ context.Context, _ store.LogCountParams) ([]string, error) {
	return nil, nil
}
func (s *testLogStore) GetByID(_ context.Context, _ int64) (*store.LogEntry, error) {
	return nil, nil
}
func (s *testLogStore) SearchRequestSummaries(_ context.Context, _ store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	return nil, nil
}
func (s *testLogStore) AggregateRequestSummaries(_ context.Context, _ store.RequestSummaryAggregateParams) (*store.RequestSummaryAggregates, error) {
	return nil, nil
}
func (s *testLogStore) RecordBatch(_ context.Context, _ string, _ int) error       { return nil }
func (s *testLogStore) GetBatch(_ context.Context, _ string) (*store.BatchRecord, error) {
	return nil, nil
}
func (s *testLogStore) PruneBatches(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
