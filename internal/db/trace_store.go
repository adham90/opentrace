package db

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
	if traceID == "" {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	ts := entry.Timestamp.UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Check if trace already exists
	var existing struct {
		spanCount   int
		rootSpanID  string
		services    string
		firstSeenAt string
		hasErrors   int
		earliestTS  string
		latestTS    string
	}

	err = tx.QueryRowContext(ctx,
		`SELECT span_count, COALESCE(root_span_id, ''), services,
		        first_seen_at, has_errors,
		        first_seen_at, last_updated_at
		 FROM trace_status WHERE trace_id = ?`, traceID,
	).Scan(&existing.spanCount, &existing.rootSpanID, &existing.services,
		&existing.firstSeenAt, &existing.hasErrors,
		&existing.earliestTS, &existing.latestTS)

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

		_, err = tx.ExecContext(ctx,
			`INSERT INTO trace_status (trace_id, span_count, root_span_id, services, first_seen_at, last_updated_at, duration_ms, status, has_errors)
			 VALUES (?, 1, ?, ?, ?, ?, 0, 'partial', ?)`,
			traceID, rootSpanID, services, ts, now, hasErrors,
		)
		if err != nil {
			return fmt.Errorf("insert trace_status: %w", err)
		}

		return tx.Commit()
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

	earliestTS := existing.firstSeenAt
	if ts < earliestTS {
		earliestTS = ts
	}
	earliestTime, _ := time.Parse(time.RFC3339, earliestTS)
	entryTime := entry.Timestamp.UTC()
	latestTime, _ := time.Parse(time.RFC3339, existing.latestTS)
	if entryTime.After(latestTime) {
		latestTime = entryTime
	}
	durationMs := float64(latestTime.Sub(earliestTime).Milliseconds())

	rootSpanNull := sql.NullString{}
	if rootSpanID != "" {
		rootSpanNull = sql.NullString{String: rootSpanID, Valid: true}
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE trace_status
		 SET span_count = ?,
		     root_span_id = ?,
		     services = ?,
		     last_updated_at = ?,
		     duration_ms = ?,
		     has_errors = ?
		 WHERE trace_id = ?`,
		newSpanCount, rootSpanNull, string(servicesJSON), now, durationMs, hasErrors, traceID,
	)
	if err != nil {
		return fmt.Errorf("update trace_status: %w", err)
	}

	return tx.Commit()
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

// MarkStaleTraces marks partial traces as 'timeout' if their last_updated_at
// is older than the given threshold.
func (s *traceStore) MarkStaleTraces(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	res, err := s.db.NewUpdate().Model((*store.TraceStatus)(nil)).
		Set("status = ?", "timeout").
		Where("status = ?", "partial").
		Where("last_updated_at < ?", cutoff).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("mark stale traces: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// isErrorLevel returns true if the log level indicates an error.
func isErrorLevel(level string) bool {
	upper := strings.ToUpper(level)
	return upper == "ERROR" || upper == "FATAL"
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
