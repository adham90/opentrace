package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type traceStore struct {
	db *sql.DB
}

// NewTraceStore creates a new TraceStore backed by SQLite.
func NewTraceStore(db *sql.DB) store.TraceStore {
	return &traceStore{db: db}
}

// UpsertTraceStatus updates the trace_status row for the given trace.
// Called on each log ingestion for entries with a non-empty trace_id.
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
		// New trace — insert
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

	// Update root span if this is the root
	rootSpanID := existing.rootSpanID
	if isRoot && rootSpanID == "" {
		rootSpanID = entry.SpanID
	}

	// Update services list
	var servicesList []string
	if err := json.Unmarshal([]byte(existing.services), &servicesList); err != nil {
		servicesList = []string{}
	}
	if entry.Service != "" && !containsString(servicesList, entry.Service) {
		servicesList = append(servicesList, entry.Service)
	}
	servicesJSON, _ := json.Marshal(servicesList)

	// Update has_errors
	hasErrors := existing.hasErrors
	if isError {
		hasErrors = 1
	}

	// Calculate duration: distance between earliest and latest timestamps
	// We track first_seen_at as the earliest log timestamp and compute
	// duration against the current entry's timestamp.
	earliestTS := existing.firstSeenAt
	if ts < earliestTS {
		earliestTS = ts
	}
	// Parse timestamps to compute duration
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
	var ts store.TraceStatus
	var rootSpanID sql.NullString
	var servicesJSON string
	var firstSeenAt, lastUpdatedAt string
	var hasErrors int

	err := s.db.QueryRowContext(ctx,
		`SELECT trace_id, span_count, root_span_id, services,
		        first_seen_at, last_updated_at, duration_ms, status, has_errors
		 FROM trace_status WHERE trace_id = ?`, traceID,
	).Scan(&ts.TraceID, &ts.SpanCount, &rootSpanID, &servicesJSON,
		&firstSeenAt, &lastUpdatedAt, &ts.DurationMs, &ts.Status, &hasErrors)

	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query trace_status: %w", err)
	}

	if rootSpanID.Valid {
		ts.RootSpanID = rootSpanID.String
	}
	if err := json.Unmarshal([]byte(servicesJSON), &ts.Services); err != nil {
		ts.Services = []string{}
	}
	ts.FirstSeenAt, _ = time.Parse(time.RFC3339, firstSeenAt)
	ts.LastUpdatedAt, _ = time.Parse(time.RFC3339, lastUpdatedAt)
	ts.HasErrors = hasErrors == 1

	return &ts, nil
}

// ListRecentTraces returns paginated recent traces ordered by last_updated_at descending.
func (s *traceStore) ListRecentTraces(ctx context.Context, limit, offset int) ([]store.TraceStatus, int, error) {
	if limit <= 0 {
		limit = 50
	}

	// Get total count
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trace_status`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count trace_status: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT trace_id, span_count, root_span_id, services,
		        first_seen_at, last_updated_at, duration_ms, status, has_errors
		 FROM trace_status
		 ORDER BY last_updated_at DESC
		 LIMIT ? OFFSET ?`, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("query trace_status: %w", err)
	}
	defer rows.Close()

	var results []store.TraceStatus
	for rows.Next() {
		var ts store.TraceStatus
		var rootSpanID sql.NullString
		var servicesJSON string
		var firstSeenAt, lastUpdatedAt string
		var hasErrors int

		if err := rows.Scan(&ts.TraceID, &ts.SpanCount, &rootSpanID, &servicesJSON,
			&firstSeenAt, &lastUpdatedAt, &ts.DurationMs, &ts.Status, &hasErrors); err != nil {
			return nil, 0, fmt.Errorf("scan trace_status: %w", err)
		}

		if rootSpanID.Valid {
			ts.RootSpanID = rootSpanID.String
		}
		if err := json.Unmarshal([]byte(servicesJSON), &ts.Services); err != nil {
			ts.Services = []string{}
		}
		ts.FirstSeenAt, _ = time.Parse(time.RFC3339, firstSeenAt)
		ts.LastUpdatedAt, _ = time.Parse(time.RFC3339, lastUpdatedAt)
		ts.HasErrors = hasErrors == 1

		results = append(results, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate trace_status: %w", err)
	}

	return results, total, nil
}

// MarkStaleTraces marks partial traces as 'timeout' if their last_updated_at
// is older than the given threshold.
func (s *traceStore) MarkStaleTraces(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE trace_status SET status = 'timeout'
		 WHERE status = 'partial' AND last_updated_at < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("mark stale traces: %w", err)
	}
	n, _ := result.RowsAffected()
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
