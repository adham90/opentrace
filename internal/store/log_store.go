package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// logStore implements LogStore using database/sql (SQLite).
type logStore struct {
	db *sql.DB
}

// NewLogStore creates a new LogStore backed by SQLite.
func NewLogStore(db *sql.DB) LogStore {
	return &logStore{db: db}
}

func (s *logStore) BatchInsert(ctx context.Context, entries []LogEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO logs (timestamp, level, service, trace_id, span_id, parent_span_id, message, event_type, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	// Prepare summary statement lazily (only if any entry has a request_summary)
	var summaryStmt *sql.Stmt

	for _, e := range entries {
		meta, err := json.Marshal(e.Metadata)
		if err != nil {
			return 0, fmt.Errorf("marshaling metadata: %w", err)
		}
		ts := e.Timestamp.UTC().Format(time.RFC3339Nano)
		res, err := stmt.ExecContext(ctx, ts, e.Level, e.Service, e.TraceID, e.SpanID, e.ParentSpanID, e.Message, e.EventType, string(meta))
		if err != nil {
			return 0, fmt.Errorf("inserting log entry: %w", err)
		}

		if e.RequestSummary != nil {
			logID, err := res.LastInsertId()
			if err != nil {
				return 0, fmt.Errorf("getting last insert id: %w", err)
			}

			if summaryStmt == nil {
				summaryStmt, err = tx.PrepareContext(ctx,
					`INSERT INTO request_summaries (
						log_id, controller, action, method, path, status,
						duration_ms, db_time_ms, view_time_ms,
						sql_count, sql_total_ms, sql_slowest_ms, sql_slowest_name, n_plus_one,
						view_count, view_total_ms, view_slowest_ms, view_slowest_template,
						cache_reads, cache_hits, cache_writes, cache_hit_ratio,
						http_external_count, http_external_total_ms, http_slowest_ms, http_slowest_host,
						memory_before_mb, memory_after_mb, memory_delta_mb, timeline,
						time_breakdown, duplicate_queries, worst_duplicate_count, top_duplicates
					) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
				if err != nil {
					return 0, fmt.Errorf("prepare summary insert: %w", err)
				}
				defer summaryStmt.Close()
			}

			rs := e.RequestSummary
			nPlusOne := 0
			if rs.NPlusOne {
				nPlusOne = 1
			}
			_, err = summaryStmt.ExecContext(ctx,
				logID, rs.Controller, rs.Action, rs.Method, rs.Path, rs.Status,
				rs.DurationMs, rs.DBTimeMs, rs.ViewTimeMs,
				rs.SQLCount, rs.SQLTotalMs, rs.SQLSlowestMs, rs.SQLSlowestName, nPlusOne,
				rs.ViewCount, rs.ViewTotalMs, rs.ViewSlowestMs, rs.ViewSlowestTemplate,
				rs.CacheReads, rs.CacheHits, rs.CacheWrites, rs.CacheHitRatio,
				rs.HTTPExternalCount, rs.HTTPExternalTotalMs, rs.HTTPSlowestMs, rs.HTTPSlowestHost,
				rs.MemoryBeforeMb, rs.MemoryAfterMb, rs.MemoryDeltaMb, rs.Timeline,
				rs.TimeBreakdown, rs.DuplicateQueries, rs.WorstDuplicateCount, rs.TopDuplicates,
			)
			if err != nil {
				return 0, fmt.Errorf("inserting request summary: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit batch insert: %w", err)
	}

	return len(entries), nil
}

func (s *logStore) Search(ctx context.Context, params LogSearchParams) ([]LogEntry, error) {
	var conditions []string
	var args []any
	useFTS := false

	if params.Query != "" && params.Query != "*" {
		useFTS = true
		conditions = append(conditions, "logs_fts MATCH ?")
		args = append(args, params.Query)
	}
	if params.Service != "" {
		conditions = append(conditions, "l.service = ? COLLATE NOCASE")
		args = append(args, params.Service)
	}
	if params.Level != "" {
		levels := strings.Split(params.Level, ",")
		if len(levels) == 1 {
			conditions = append(conditions, "l.level = ? COLLATE NOCASE")
			args = append(args, levels[0])
		} else {
			placeholders := make([]string, len(levels))
			for i, lv := range levels {
				placeholders[i] = "?"
				args = append(args, strings.TrimSpace(lv))
			}
			conditions = append(conditions, "l.level COLLATE NOCASE IN ("+strings.Join(placeholders, ",")+")")
		}
	}
	if params.TraceID != "" {
		conditions = append(conditions, "l.trace_id = ?")
		args = append(args, params.TraceID)
	}
	if params.EventType != "" {
		conditions = append(conditions, "l.event_type = ?")
		args = append(args, params.EventType)
	}
	if params.SinceID > 0 {
		conditions = append(conditions, "l.id > ?")
		args = append(args, params.SinceID)
	}
	if params.Start != nil {
		conditions = append(conditions, "l.timestamp >= ?")
		args = append(args, params.Start.UTC().Format(time.RFC3339Nano))
	}
	if params.End != nil {
		conditions = append(conditions, "l.timestamp <= ?")
		args = append(args, params.End.UTC().Format(time.RFC3339Nano))
	}

	var query string
	if useFTS {
		query = `SELECT l.id, l.timestamp, l.level, l.service, l.trace_id, l.span_id, l.parent_span_id, l.message, l.event_type, l.metadata
		         FROM logs l JOIN logs_fts ON l.id = logs_fts.rowid`
	} else {
		query = `SELECT l.id, l.timestamp, l.level, l.service, l.trace_id, l.span_id, l.parent_span_id, l.message, l.event_type, l.metadata
		         FROM logs l`
	}

	// Metadata key-value filters using json_extract.
	// Operators: "~value" → LIKE %value%, "*" → IS NOT NULL, plain → exact match.
	for k, v := range params.MetadataFilter {
		path := "$." + k
		if v == "*" {
			conditions = append(conditions, "json_extract(l.metadata, ?) IS NOT NULL")
			args = append(args, path)
		} else if strings.HasPrefix(v, "~") {
			conditions = append(conditions, "json_extract(l.metadata, ?) LIKE ?")
			args = append(args, path, "%"+v[1:]+"%")
		} else {
			conditions = append(conditions, "json_extract(l.metadata, ?) = ?")
			args = append(args, path, v)
		}
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	if params.SortAsc {
		query += " ORDER BY l.timestamp ASC"
	} else {
		query += " ORDER BY l.timestamp DESC"
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	query += " LIMIT ?"
	args = append(args, limit)

	if params.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, params.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching logs: %w", err)
	}
	defer rows.Close()

	result := make([]LogEntry, 0)
	for rows.Next() {
		var entry LogEntry
		var tsStr string
		var metaJSON sql.NullString
		var spanID, parentSpanID sql.NullString
		if err := rows.Scan(
			&entry.ID, &tsStr, &entry.Level, &entry.Service,
			&entry.TraceID, &spanID, &parentSpanID, &entry.Message, &entry.EventType, &metaJSON,
		); err != nil {
			return nil, fmt.Errorf("scanning log entry: %w", err)
		}
		entry.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
		if spanID.Valid {
			entry.SpanID = spanID.String
		}
		if parentSpanID.Valid {
			entry.ParentSpanID = parentSpanID.String
		}
		if metaJSON.Valid && metaJSON.String != "" {
			if err := json.Unmarshal([]byte(metaJSON.String), &entry.Metadata); err != nil {
				slog.Warn("invalid metadata JSON in log entry", "entry_id", entry.ID, "error", err)
			}
		}
		result = append(result, entry)
	}

	return result, rows.Err()
}

func (s *logStore) CountByLevel(ctx context.Context, params LogCountParams) (map[string]int, error) {
	query := `SELECT level, COUNT(*) FROM logs WHERE timestamp >= ? AND timestamp < ?`
	args := []any{params.Since.UTC().Format(time.RFC3339Nano), params.Until.UTC().Format(time.RFC3339Nano)}

	if params.Service != "" {
		query += ` AND service = ? COLLATE NOCASE`
		args = append(args, params.Service)
	}
	if params.Level != "" {
		query += ` AND level = ? COLLATE NOCASE`
		args = append(args, params.Level)
	}
	query += ` GROUP BY level`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("counting logs by level: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var level string
		var count int
		if err := rows.Scan(&level, &count); err != nil {
			return nil, fmt.Errorf("scanning level count: %w", err)
		}
		result[level] = count
	}
	return result, rows.Err()
}

func (s *logStore) CountByService(ctx context.Context, params LogCountParams) ([]ServiceLogCount, error) {
	query := `SELECT service,
	                 COUNT(*) AS total,
	                 SUM(CASE WHEN level IN ('error', 'fatal') THEN 1 ELSE 0 END) AS error_count
	          FROM logs WHERE timestamp >= ? AND timestamp < ?`
	args := []any{params.Since.UTC().Format(time.RFC3339Nano), params.Until.UTC().Format(time.RFC3339Nano)}

	if params.Service != "" {
		query += ` AND service = ? COLLATE NOCASE`
		args = append(args, params.Service)
	}
	if params.Level != "" {
		query += ` AND level = ? COLLATE NOCASE`
		args = append(args, params.Level)
	}
	query += ` GROUP BY service ORDER BY total DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("counting logs by service: %w", err)
	}
	defer rows.Close()

	var result []ServiceLogCount
	for rows.Next() {
		var sc ServiceLogCount
		if err := rows.Scan(&sc.Service, &sc.Total, &sc.ErrorCount); err != nil {
			return nil, fmt.Errorf("scanning service count: %w", err)
		}
		result = append(result, sc)
	}
	return result, rows.Err()
}

func (s *logStore) DistinctValues(ctx context.Context, field string, params LogCountParams) ([]string, error) {
	// Whitelist allowed field names to prevent SQL injection.
	var col string
	switch field {
	case "service":
		col = "service"
	case "level":
		col = "level"
	case "event_type":
		col = "event_type"
	default:
		return nil, fmt.Errorf("unsupported field %q (use service, level, or event_type)", field)
	}

	query := fmt.Sprintf(`SELECT DISTINCT %s FROM logs WHERE %s != '' AND timestamp >= ? AND timestamp < ?`, col, col)
	args := []any{params.Since.UTC().Format(time.RFC3339Nano), params.Until.UTC().Format(time.RFC3339Nano)}

	if params.Service != "" {
		query += ` AND service = ? COLLATE NOCASE`
		args = append(args, params.Service)
	}
	query += fmt.Sprintf(` ORDER BY %s`, col)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("distinct values: %w", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var val string
		if err := rows.Scan(&val); err != nil {
			return nil, fmt.Errorf("scanning distinct value: %w", err)
		}
		result = append(result, val)
	}
	return result, rows.Err()
}

func (s *logStore) MetadataKeys(ctx context.Context, params LogCountParams) ([]string, error) {
	query := `SELECT DISTINCT jk.key FROM logs, json_each(logs.metadata) AS jk
	          WHERE logs.timestamp >= ? AND logs.timestamp < ? AND logs.metadata != '{}' AND logs.metadata != 'null'`
	args := []any{params.Since.UTC().Format(time.RFC3339Nano), params.Until.UTC().Format(time.RFC3339Nano)}

	if params.Service != "" {
		query += ` AND logs.service = ? COLLATE NOCASE`
		args = append(args, params.Service)
	}
	query += ` ORDER BY jk.key`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("metadata keys: %w", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scanning metadata key: %w", err)
		}
		result = append(result, key)
	}
	return result, rows.Err()
}

func (s *logStore) GetByID(ctx context.Context, id int64) (*LogEntry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, timestamp, level, service, trace_id, span_id, parent_span_id, message, event_type, metadata FROM logs WHERE id = ?`, id)

	var entry LogEntry
	var tsStr string
	var metaJSON sql.NullString
	var spanID, parentSpanID sql.NullString
	if err := row.Scan(&entry.ID, &tsStr, &entry.Level, &entry.Service,
		&entry.TraceID, &spanID, &parentSpanID, &entry.Message, &entry.EventType, &metaJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting log by id: %w", err)
	}
	entry.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
	if spanID.Valid {
		entry.SpanID = spanID.String
	}
	if parentSpanID.Valid {
		entry.ParentSpanID = parentSpanID.String
	}
	if metaJSON.Valid && metaJSON.String != "" {
		if err := json.Unmarshal([]byte(metaJSON.String), &entry.Metadata); err != nil {
			slog.Warn("invalid metadata JSON in log entry", "entry_id", entry.ID, "error", err)
		}
	}

	// Load associated request summary if present
	var rs RequestSummary
	var nPlusOne int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, log_id, controller, action, method, path, status,
			duration_ms, db_time_ms, view_time_ms,
			sql_count, sql_total_ms, sql_slowest_ms, sql_slowest_name, n_plus_one,
			view_count, view_total_ms, view_slowest_ms, view_slowest_template,
			cache_reads, cache_hits, cache_writes, cache_hit_ratio,
			http_external_count, http_external_total_ms, http_slowest_ms, http_slowest_host,
			memory_before_mb, memory_after_mb, memory_delta_mb, timeline,
			COALESCE(time_breakdown, ''), COALESCE(duplicate_queries, 0),
			COALESCE(worst_duplicate_count, 0), COALESCE(top_duplicates, '')
		FROM request_summaries WHERE log_id = ?`, id,
	).Scan(&rs.ID, &rs.LogID, &rs.Controller, &rs.Action, &rs.Method, &rs.Path, &rs.Status,
		&rs.DurationMs, &rs.DBTimeMs, &rs.ViewTimeMs,
		&rs.SQLCount, &rs.SQLTotalMs, &rs.SQLSlowestMs, &rs.SQLSlowestName, &nPlusOne,
		&rs.ViewCount, &rs.ViewTotalMs, &rs.ViewSlowestMs, &rs.ViewSlowestTemplate,
		&rs.CacheReads, &rs.CacheHits, &rs.CacheWrites, &rs.CacheHitRatio,
		&rs.HTTPExternalCount, &rs.HTTPExternalTotalMs, &rs.HTTPSlowestMs, &rs.HTTPSlowestHost,
		&rs.MemoryBeforeMb, &rs.MemoryAfterMb, &rs.MemoryDeltaMb, &rs.Timeline,
		&rs.TimeBreakdown, &rs.DuplicateQueries, &rs.WorstDuplicateCount, &rs.TopDuplicates)
	if err == nil {
		rs.NPlusOne = nPlusOne == 1
		entry.RequestSummary = &rs
	}
	// sql.ErrNoRows is fine — most logs won't have a request summary

	return &entry, nil
}

func (s *logStore) SearchRequestSummaries(ctx context.Context, params RequestSummarySearchParams) ([]RequestSummaryResult, error) {
	query := `SELECT rs.id, rs.log_id, rs.controller, rs.action, rs.method, rs.path, rs.status,
		rs.duration_ms, rs.db_time_ms, rs.view_time_ms,
		rs.sql_count, rs.sql_total_ms, rs.sql_slowest_ms, rs.sql_slowest_name, rs.n_plus_one,
		rs.view_count, rs.view_total_ms, rs.view_slowest_ms, rs.view_slowest_template,
		rs.cache_reads, rs.cache_hits, rs.cache_writes, rs.cache_hit_ratio,
		rs.http_external_count, rs.http_external_total_ms, rs.http_slowest_ms, rs.http_slowest_host,
		rs.memory_before_mb, rs.memory_after_mb, rs.memory_delta_mb, rs.timeline,
		COALESCE(rs.time_breakdown, ''), COALESCE(rs.duplicate_queries, 0),
		COALESCE(rs.worst_duplicate_count, 0), COALESCE(rs.top_duplicates, ''),
		l.timestamp, l.service, l.trace_id
	FROM request_summaries rs
	JOIN logs l ON l.id = rs.log_id`

	var conditions []string
	var args []any

	if params.Start != nil {
		conditions = append(conditions, "l.timestamp >= ?")
		args = append(args, params.Start.UTC().Format(time.RFC3339Nano))
	}
	if params.End != nil {
		conditions = append(conditions, "l.timestamp <= ?")
		args = append(args, params.End.UTC().Format(time.RFC3339Nano))
	}
	if params.Controller != "" {
		conditions = append(conditions, "rs.controller LIKE ?")
		args = append(args, "%"+params.Controller+"%")
	}
	if params.Action != "" {
		conditions = append(conditions, "rs.action = ?")
		args = append(args, params.Action)
	}
	if params.Path != "" {
		conditions = append(conditions, "rs.path LIKE ?")
		args = append(args, "%"+params.Path+"%")
	}
	if params.NPlusOneOnly {
		conditions = append(conditions, "rs.n_plus_one = 1")
	}
	if params.MinDurationMs > 0 {
		conditions = append(conditions, "rs.duration_ms >= ?")
		args = append(args, params.MinDurationMs)
	}
	if params.MinSQLCount > 0 {
		conditions = append(conditions, "rs.sql_count >= ?")
		args = append(args, params.MinSQLCount)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	// Sort by (whitelist allowed columns).
	sortCol := "rs.duration_ms"
	switch params.SortBy {
	case "sql_count":
		sortCol = "rs.sql_count"
	case "db_time_ms":
		sortCol = "rs.db_time_ms"
	case "duplicate_queries":
		sortCol = "COALESCE(rs.duplicate_queries, 0)"
	}
	query += " ORDER BY " + sortCol + " DESC"

	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	query += " LIMIT ?"
	args = append(args, limit)

	if params.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, params.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching request summaries: %w", err)
	}
	defer rows.Close()

	var results []RequestSummaryResult
	for rows.Next() {
		var r RequestSummaryResult
		var nPlusOne int
		var tsStr string
		if err := rows.Scan(
			&r.ID, &r.LogID, &r.Controller, &r.Action, &r.Method, &r.Path, &r.Status,
			&r.DurationMs, &r.DBTimeMs, &r.ViewTimeMs,
			&r.SQLCount, &r.SQLTotalMs, &r.SQLSlowestMs, &r.SQLSlowestName, &nPlusOne,
			&r.ViewCount, &r.ViewTotalMs, &r.ViewSlowestMs, &r.ViewSlowestTemplate,
			&r.CacheReads, &r.CacheHits, &r.CacheWrites, &r.CacheHitRatio,
			&r.HTTPExternalCount, &r.HTTPExternalTotalMs, &r.HTTPSlowestMs, &r.HTTPSlowestHost,
			&r.MemoryBeforeMb, &r.MemoryAfterMb, &r.MemoryDeltaMb, &r.Timeline,
			&r.TimeBreakdown, &r.DuplicateQueries, &r.WorstDuplicateCount, &r.TopDuplicates,
			&tsStr, &r.Service, &r.TraceID,
		); err != nil {
			return nil, fmt.Errorf("scanning request summary: %w", err)
		}
		r.NPlusOne = nPlusOne == 1
		r.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
		results = append(results, r)
	}

	return results, rows.Err()
}

func (s *logStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM logs WHERE timestamp < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("pruning logs: %w", err)
	}
	return result.RowsAffected()
}

func (s *logStore) RecordBatch(ctx context.Context, batchID string, logCount int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO ingest_batches (batch_id, log_count) VALUES (?, ?)`,
		batchID, logCount,
	)
	return err
}

func (s *logStore) GetBatch(ctx context.Context, batchID string) (*BatchRecord, error) {
	var rec BatchRecord
	var tsStr string
	err := s.db.QueryRowContext(ctx,
		`SELECT batch_id, log_count, received_at FROM ingest_batches WHERE batch_id = ?`,
		batchID,
	).Scan(&rec.BatchID, &rec.LogCount, &tsStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rec.ReceivedAt, _ = time.Parse(time.RFC3339Nano, tsStr)
	return &rec, nil
}

func (s *logStore) PruneBatches(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM ingest_batches WHERE received_at < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("pruning batches: %w", err)
	}
	return result.RowsAffected()
}
