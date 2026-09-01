package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type traceStore struct {
	db *bun.DB
}

// NewTraceStore creates a new TraceStore backed by SQLite.
func NewTraceStore(db *bun.DB) store.TraceStore {
	return &traceStore{db: db}
}

// UpsertTraceStatus uses a transaction with read-modify-write for services list merging.
func (s *traceStore) UpsertTraceStatus(ctx context.Context, traceID string, entry store.LogEntry) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return upsertTraceStatus(ctx, tx, traceID, entry)
	})
}

func (s *traceStore) UpsertTraceStatusBatch(ctx context.Context, entries []store.LogEntry) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for i := range entries {
			if err := upsertTraceStatus(ctx, tx, entries[i].TraceID, entries[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

func upsertTraceStatus(ctx context.Context, db bun.IDB, traceID string, entry store.LogEntry) error {
	if traceID == "" {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	ts := entry.Timestamp.UTC().Format(time.RFC3339)

	// Check if trace already exists
	var existing struct {
		spanCount   int
		rootSpanID  string
		services    string
		firstSeenAt string
		hasErrors   int
		durationMs  float64
	}

	err := db.QueryRowContext(ctx,
		`SELECT span_count, COALESCE(root_span_id, ''), services,
			        first_seen_at, has_errors, duration_ms
			 FROM trace_status WHERE trace_id = ?`, traceID,
	).Scan(&existing.spanCount, &existing.rootSpanID, &existing.services,
		&existing.firstSeenAt, &existing.hasErrors, &existing.durationMs)

	isError := isErrorLevel(entry.Level)
	isRoot := entry.SpanID != "" && entry.ParentSpanID == ""

	if err == sql.ErrNoRows {
		// New trace -- insert
		services := "[]"
		if entry.Service != "" {
			servicesJSON, _ := json.Marshal([]string{entry.Service})
			services = string(servicesJSON)
		}

		rootSpanID := sql.NullString{}
		if isRoot {
			rootSpanID = sql.NullString{String: entry.SpanID, Valid: true}
		}

		hasErrors := 0
		if isError {
			hasErrors = 1
		}

		_, err = db.ExecContext(ctx,
			`INSERT INTO trace_status (trace_id, span_count, root_span_id, services, first_seen_at, last_updated_at, duration_ms, status, has_errors)
				 VALUES (?, 1, ?, ?, ?, ?, 0, 'partial', ?)`,
			traceID, rootSpanID, services, ts, now, hasErrors,
		)
		if err != nil {
			return fmt.Errorf("insert trace_status: %w", err)
		}

		return nil
	}
	if err != nil {
		return fmt.Errorf("query trace_status: %w", err)
	}

	// Update existing trace
	newSpanCount := existing.spanCount + 1

	rootSpanID := existing.rootSpanID
	if isRoot && rootSpanID == "" {
		rootSpanID = entry.SpanID
	}

	var servicesList []string
	if err := json.Unmarshal([]byte(existing.services), &servicesList); err != nil {
		servicesList = []string{}
	}
	if entry.Service != "" && !containsString(servicesList, entry.Service) {
		servicesList = append(servicesList, entry.Service)
	}
	servicesJSON, _ := json.Marshal(servicesList)

	hasErrors := existing.hasErrors
	if isError {
		hasErrors = 1
	}

	// Duration spans the observed event timestamps, never ingestion
	// wall-clock: last_updated_at records when we wrote the row, so folding
	// it in would add the agent's shipping lag to every trace. There is no
	// column for the latest event timestamp, but the stored pair
	// (first_seen_at, duration_ms) encodes it exactly, so recover it.
	earliestTS := existing.firstSeenAt
	if ts < earliestTS {
		earliestTS = ts
	}
	earliestTime := parseTime(earliestTS)
	prevLatest := parseTime(existing.firstSeenAt).
		Add(time.Duration(existing.durationMs) * time.Millisecond)

	latestTime := entry.Timestamp.UTC()
	if prevLatest.After(latestTime) {
		latestTime = prevLatest
	}
	durationMs := float64(latestTime.Sub(earliestTime).Milliseconds())

	rootSpanNull := sql.NullString{}
	if rootSpanID != "" {
		rootSpanNull = sql.NullString{String: rootSpanID, Valid: true}
	}

	_, err = db.ExecContext(ctx,
		`UPDATE trace_status
			 SET span_count = ?,
			     root_span_id = ?,
			     services = ?,
			     first_seen_at = ?,
			     last_updated_at = ?,
			     duration_ms = ?,
			     has_errors = ?
			 WHERE trace_id = ?`,
		newSpanCount, rootSpanNull, string(servicesJSON), earliestTS, now, durationMs, hasErrors, traceID,
	)
	if err != nil {
		return fmt.Errorf("update trace_status: %w", err)
	}

	return nil
}

// GetTraceStatus returns the current status of a trace.
func (s *traceStore) GetTraceStatus(ctx context.Context, traceID string) (*store.TraceStatus, error) {
	ts := new(store.TraceStatus)
	err := s.db.NewSelect().Model(ts).Where("trace_id = ?", traceID).Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query trace_status: %w", err)
	}
	return ts, nil
}

// ListRecentTraces returns paginated recent traces ordered by last_updated_at descending.
func (s *traceStore) ListRecentTraces(ctx context.Context, limit, offset int) ([]store.TraceStatus, int, error) {
	if limit <= 0 {
		limit = 50
	}

	// Get total count
	total, err := s.db.NewSelect().Model((*store.TraceStatus)(nil)).Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count trace_status: %w", err)
	}

	var traces []store.TraceStatus
	err = s.db.NewSelect().Model(&traces).
		OrderExpr("last_updated_at DESC").
		Offset(offset).
		Limit(limit).
		Scan(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("query trace_status: %w", err)
	}

	return traces, total, nil
}

// staleTraceBatchSize is how many rows one UPDATE statement may touch, matching
// the retention sweep's batching for the same reason.
const staleTraceBatchSize = 1000

// MarkStaleTraces marks partial traces as 'timeout' if their last_updated_at
// is older than the given threshold.
//
// The update is batched because the driver is pure Go: SQLite's page churn for
// a statement is Go heap, not memory outside the runtime, so one UPDATE across
// every stale row is a heap spike proportional to the table. A backlogged sweep
// over 740k rows was enough on its own to push a 256MB container into the OOM
// killer, and the rows it walks are exactly the ones an unbounded trace_status
// accumulates.
func (s *traceStore) MarkStaleTraces(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	total := 0
	for {
		res, err := s.db.NewRaw(
			`UPDATE trace_status SET status = 'timeout' WHERE rowid IN (
				SELECT rowid FROM trace_status
				WHERE status = 'partial' AND last_updated_at < ? LIMIT ?)`,
			cutoff, staleTraceBatchSize,
		).Exec(ctx)
		if err != nil {
			return total, fmt.Errorf("mark stale traces: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
		if n < staleTraceBatchSize {
			return total, nil
		}
		// A sweep this large is maintenance, not a request: yield rather than
		// hold the write lock for the whole backlog.
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
	}
}

// isErrorLevel returns true if the log level indicates an error.
func isErrorLevel(level string) bool {
	lower := strings.ToLower(level)
	return lower == "error" || lower == "fatal"
}

// containsString checks if a slice contains a string.
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
