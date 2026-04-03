package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/internal/deepcapture"
	"github.com/adham90/opentrace/pkg/store"
)

// logStore implements LogStore using bun (SQLite).
type logStore struct {
	db *bun.DB
}

// NewLogStore creates a new LogStore backed by SQLite.
func NewLogStore(db *bun.DB) store.LogStore {
	return &logStore{db: db}
}

// BatchInsert inserts log entries in a single transaction using prepared
// statements.
func (s *logStore) BatchInsert(ctx context.Context, entries []store.LogEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	if err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO logs (timestamp, level, service, environment, commit_hash,
			                   trace_id, span_id, parent_span_id, request_id,
			                   user_id,
			                   message, event_type, exception_class, error_fingerprint,
			                   source_file, source_line, metadata)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare insert: %w", err)
		}
		defer stmt.Close()

		var summaryStmt *sql.Stmt

		for i := range entries {
			e := &entries[i]
			promoteFromMetadata(e)

			metaStr := e.MetadataJSON
			if metaStr == "" {
				meta, err := json.Marshal(e.Metadata)
				if err != nil {
					return fmt.Errorf("marshaling metadata: %w", err)
				}
				metaStr = string(meta)
			}
			ts := e.Timestamp.UTC().Format(time.RFC3339Nano)
			res, err := stmt.ExecContext(ctx, ts, e.Level, e.Service, e.Environment, e.CommitHash,
				e.TraceID, e.SpanID, e.ParentSpanID, e.RequestID,
				e.UserID,
				e.Message, e.EventType, e.ExceptionClass, e.ErrorFingerprint,
				e.SourceFile, e.SourceLine, metaStr)
			if err != nil {
				return fmt.Errorf("inserting log entry: %w", err)
			}

			// Always capture the auto-generated ID so downstream consumers
			// (processAfterInsert, deep capture, etc.) can reference it.
			logID, err := res.LastInsertId()
			if err != nil {
				return fmt.Errorf("getting last insert id: %w", err)
			}
			e.ID = logID

			if e.RequestSummary != nil {
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
						return fmt.Errorf("prepare summary insert: %w", err)
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
					return fmt.Errorf("inserting request summary: %w", err)
				}
			}

			// Process deep capture detail rows within the same transaction.
			if len(e.DeepCapture) > 0 {
				var doc deepcapture.Document
				if err := json.Unmarshal(e.DeepCapture, &doc); err != nil {
					slog.Warn("deepcapture: invalid document JSON",
						"error", err,
						"log_id", logID,
					)
				} else {
					// PII scrubbing — load config from app_config and scrub
					// the document before any detail rows are extracted.
					piiCfg := deepcapture.LoadPIIConfig(ctx, tx.Tx)
					deepcapture.ScrubDocument(&doc, piiCfg)

					if err := deepcapture.ProcessDocument(tx.Tx, &doc, logID); err != nil {
						slog.Warn("deepcapture: processing failed",
							"error", err,
							"log_id", logID,
						)
					}

					// Process in-request log entries (event.logs) as separate rows.
					deepcapture.ProcessInRequestLogs(tx.Tx, &doc, logID)
				}
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}

	return len(entries), nil
}

func promoteFromMetadata(e *store.LogEntry) {
	if e.Metadata == nil {
		return
	}
	if e.CommitHash == "" {
		if v, ok := e.Metadata["git_sha"].(string); ok && v != "" {
			e.CommitHash = v
		}
	}
	if e.RequestID == "" {
		if v, ok := e.Metadata["request_id"].(string); ok && v != "" {
			e.RequestID = v
		}
	}
	if e.ExceptionClass == "" {
		if v, ok := e.Metadata["exception_class"].(string); ok && v != "" {
			e.ExceptionClass = v
		}
	}
	if e.SourceFile == "" {
		if bt, ok := e.Metadata["backtrace"].([]any); ok && len(bt) > 0 {
			if line, ok := bt[0].(string); ok {
				e.SourceFile, e.SourceLine = parseBacktraceLine(line)
			}
		}
	}
	if e.UserID == "" {
		if v, ok := e.Metadata["user_id"].(string); ok && v != "" {
			e.UserID = v
		} else if v, ok := e.Metadata["user_id"].(float64); ok {
			e.UserID = strconv.FormatInt(int64(v), 10)
		}
	}
}

func parseBacktraceLine(line string) (string, int) {
	parts := strings.SplitN(line, ":", 3)
	if len(parts) < 2 {
		return "", 0
	}
	file := parts[0]
	lineNum, err := strconv.Atoi(parts[1])
	if err != nil {
		return file, 0
	}
	return file, lineNum
}

func (s *logStore) Search(ctx context.Context, params store.LogSearchParams) ([]store.LogEntry, error) {
	var qb queryBuilder
	useFTS := false

	if params.Query != "" && params.Query != "*" {
		useFTS = true
		qb.where("logs_fts MATCH ?", params.Query)
	}
	multiIn := func(col, value string) {
		vals := strings.Split(value, ",")
		if len(vals) == 1 {
			qb.where(col+" = ? COLLATE NOCASE", strings.TrimSpace(vals[0]))
		} else {
			ph := make([]string, len(vals))
			trimmed := make([]any, len(vals))
			for i, v := range vals {
				ph[i] = "?"
				trimmed[i] = strings.TrimSpace(v)
			}
			qb.where(col+" COLLATE NOCASE IN ("+strings.Join(ph, ",")+")", trimmed...)
		}
	}

	if params.Service != "" {
		multiIn("l.service", params.Service)
	}
	if params.Level != "" {
		multiIn("l.level", params.Level)
	}
	if params.TraceID != "" {
		qb.where("l.trace_id = ?", params.TraceID)
	}
	if params.Environment != "" {
		multiIn("l.environment", params.Environment)
	}
	if params.CommitHash != "" {
		if len(params.CommitHash) < 40 {
			qb.where("l.commit_hash LIKE ?", params.CommitHash+"%")
		} else {
			qb.where("l.commit_hash = ?", params.CommitHash)
		}
	}
	if params.RequestID != "" {
		qb.where("l.request_id = ?", params.RequestID)
	}
	if params.EventType != "" {
		multiIn("l.event_type", params.EventType)
	}
	if params.ExceptionClass != "" {
		multiIn("l.exception_class", params.ExceptionClass)
	}
	if params.ErrorFingerprint != "" {
		multiIn("l.error_fingerprint", params.ErrorFingerprint)
	}
	if params.SourceFile != "" {
		multiIn("l.source_file", params.SourceFile)
	}

	excludeColMap := map[string]string{
		"service": "l.service", "level": "l.level", "environment": "l.environment",
		"event_type": "l.event_type", "exception_class": "l.exception_class",
		"error_fingerprint": "l.error_fingerprint", "source_file": "l.source_file",
		"commit_hash": "l.commit_hash",
	}
	for field, rawVal := range params.Exclude {
		col, ok := excludeColMap[field]
		if !ok {
			continue
		}
		vals := strings.Split(rawVal, ",")
		if len(vals) == 1 {
			qb.where(col+" != ? COLLATE NOCASE", strings.TrimSpace(vals[0]))
		} else {
			ph := make([]string, len(vals))
			trimmed := make([]any, len(vals))
			for i, v := range vals {
				ph[i] = "?"
				trimmed[i] = strings.TrimSpace(v)
			}
			qb.where(col+" COLLATE NOCASE NOT IN ("+strings.Join(ph, ",")+")", trimmed...)
		}
	}
	if params.SinceID > 0 {
		qb.where("l.id > ?", params.SinceID)
	}
	if params.Start != nil {
		qb.where("l.timestamp >= ?", params.Start.UTC().Format(time.RFC3339Nano))
	}
	if params.End != nil {
		qb.where("l.timestamp <= ?", params.End.UTC().Format(time.RFC3339Nano))
	}

	const selectCols = `l.id, l.timestamp, l.level, l.service, l.environment, l.commit_hash,
		l.trace_id, l.span_id, l.parent_span_id, l.request_id,
		l.user_id,
		l.message, l.event_type, l.exception_class, l.error_fingerprint,
		l.source_file, l.source_line, l.metadata`

	var baseQuery string
	if useFTS {
		baseQuery = `SELECT ` + selectCols + ` FROM logs l JOIN logs_fts ON l.id = logs_fts.rowid`
	} else {
		baseQuery = `SELECT ` + selectCols + ` FROM logs l`
	}

	for k, v := range params.MetadataFilter {
		path := "$." + k
		if v == "*" {
			qb.where("json_extract(l.metadata, ?) IS NOT NULL", path)
		} else if strings.HasPrefix(v, "~") {
			qb.where("json_extract(l.metadata, ?) LIKE ?", path, "%"+v[1:]+"%")
		} else {
			qb.where("json_extract(l.metadata, ?) = ?", path, v)
		}
	}

	query, args := qb.build(baseQuery)

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

	result := make([]store.LogEntry, 0, limit)
	for rows.Next() {
		entry, err := scanLogRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning log entry: %w", err)
		}
		result = append(result, *entry)
	}

	return result, rows.Err()
}

// scanLogRow scans a single log row from *sql.Rows.
func scanLogRow(rows *sql.Rows) (*store.LogEntry, error) {
	var entry store.LogEntry
	var tsStr string
	var metaJSON sql.NullString
	var environment, commitHash, spanID, parentSpanID, requestID sql.NullString
	var userID sql.NullString
	var eventType, exceptionClass, errorFingerprint, sourceFile sql.NullString
	var sourceLine sql.NullInt64
	if err := rows.Scan(
		&entry.ID, &tsStr, &entry.Level, &entry.Service, &environment, &commitHash,
		&entry.TraceID, &spanID, &parentSpanID, &requestID,
		&userID,
		&entry.Message, &eventType, &exceptionClass, &errorFingerprint,
		&sourceFile, &sourceLine, &metaJSON,
	); err != nil {
		return nil, err
	}
	entry.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
	if environment.Valid {
		entry.Environment = environment.String
	}
	if commitHash.Valid {
		entry.CommitHash = commitHash.String
	}
	if spanID.Valid {
		entry.SpanID = spanID.String
	}
	if parentSpanID.Valid {
		entry.ParentSpanID = parentSpanID.String
	}
	if requestID.Valid {
		entry.RequestID = requestID.String
	}
	if userID.Valid {
		entry.UserID = userID.String
	}
	if eventType.Valid {
		entry.EventType = eventType.String
	}
	if exceptionClass.Valid {
		entry.ExceptionClass = exceptionClass.String
	}
	if errorFingerprint.Valid {
		entry.ErrorFingerprint = errorFingerprint.String
	}
	if sourceFile.Valid {
		entry.SourceFile = sourceFile.String
	}
	if sourceLine.Valid {
		entry.SourceLine = int(sourceLine.Int64)
	}
	if metaJSON.Valid && metaJSON.String != "" {
		if err := json.Unmarshal([]byte(metaJSON.String), &entry.Metadata); err != nil {
			slog.Warn("invalid metadata JSON in log entry", "entry_id", entry.ID, "error", err)
		}
	}
	return &entry, nil
}

func (s *logStore) CountByLevel(ctx context.Context, params store.LogCountParams) (map[string]int, error) {
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

func (s *logStore) Histogram(ctx context.Context, params store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	var buckets []store.LogHistogramBucket

	for t := params.Since; t.Before(params.Until); t = t.Add(params.Interval) {
		bucketEnd := t.Add(params.Interval)
		if bucketEnd.After(params.Until) {
			bucketEnd = params.Until
		}

		query := `SELECT COUNT(*) AS total,
		          COALESCE(SUM(CASE WHEN level IN ('ERROR', 'error', 'FATAL', 'fatal') THEN 1 ELSE 0 END), 0) AS error_count
		          FROM logs WHERE timestamp >= ? AND timestamp < ?`
		args := []any{t.UTC().Format(time.RFC3339Nano), bucketEnd.UTC().Format(time.RFC3339Nano)}

		if params.Service != "" {
			query += ` AND service = ? COLLATE NOCASE`
			args = append(args, params.Service)
		}
		if params.Level != "" {
			query += ` AND level = ? COLLATE NOCASE`
			args = append(args, params.Level)
		}

		var total, errorCount int
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total, &errorCount); err != nil {
			return nil, fmt.Errorf("histogram bucket query: %w", err)
		}

		buckets = append(buckets, store.LogHistogramBucket{
			Timestamp:  t,
			Total:      total,
			ErrorCount: errorCount,
		})
	}

	return buckets, nil
}

func (s *logStore) CountByService(ctx context.Context, params store.LogCountParams) ([]store.ServiceLogCount, error) {
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

	var result []store.ServiceLogCount
	for rows.Next() {
		var sc store.ServiceLogCount
		if err := rows.Scan(&sc.Service, &sc.Total, &sc.ErrorCount); err != nil {
			return nil, fmt.Errorf("scanning service count: %w", err)
		}
		result = append(result, sc)
	}
	return result, rows.Err()
}

func (s *logStore) DistinctValues(ctx context.Context, field string, params store.LogCountParams) ([]string, error) {
	var col string
	switch field {
	case "service":
		col = "service"
	case "level":
		col = "level"
	case "event_type":
		col = "event_type"
	case "environment":
		col = "environment"
	case "commit_hash":
		col = "commit_hash"
	case "request_id":
		col = "request_id"
	case "exception_class":
		col = "exception_class"
	case "error_fingerprint":
		col = "error_fingerprint"
	case "source_file":
		col = "source_file"
	default:
		return nil, fmt.Errorf("unsupported field %q", field)
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

func (s *logStore) MetadataKeys(ctx context.Context, params store.LogCountParams) ([]string, error) {
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

func (s *logStore) GetByID(ctx context.Context, id int64) (*store.LogEntry, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, timestamp, level, service, environment, commit_hash,
			trace_id, span_id, parent_span_id, request_id,
			user_id,
			message, event_type, exception_class, error_fingerprint,
			source_file, source_line, metadata
		FROM logs WHERE id = ?`, id)

	var entry store.LogEntry
	var tsStr string
	var metaJSON sql.NullString
	var environment, commitHash, spanID, parentSpanID, requestID sql.NullString
	var userID sql.NullString
	var eventType, exceptionClass, errorFingerprint, sourceFile sql.NullString
	var sourceLine sql.NullInt64

	err := row.Scan(
		&entry.ID, &tsStr, &entry.Level, &entry.Service, &environment, &commitHash,
		&entry.TraceID, &spanID, &parentSpanID, &requestID,
		&userID,
		&entry.Message, &eventType, &exceptionClass, &errorFingerprint,
		&sourceFile, &sourceLine, &metaJSON,
	)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting log by id: %w", err)
	}

	entry.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
	if environment.Valid {
		entry.Environment = environment.String
	}
	if commitHash.Valid {
		entry.CommitHash = commitHash.String
	}
	if spanID.Valid {
		entry.SpanID = spanID.String
	}
	if parentSpanID.Valid {
		entry.ParentSpanID = parentSpanID.String
	}
	if requestID.Valid {
		entry.RequestID = requestID.String
	}
	if userID.Valid {
		entry.UserID = userID.String
	}
	if eventType.Valid {
		entry.EventType = eventType.String
	}
	if exceptionClass.Valid {
		entry.ExceptionClass = exceptionClass.String
	}
	if errorFingerprint.Valid {
		entry.ErrorFingerprint = errorFingerprint.String
	}
	if sourceFile.Valid {
		entry.SourceFile = sourceFile.String
	}
	if sourceLine.Valid {
		entry.SourceLine = int(sourceLine.Int64)
	}
	if metaJSON.Valid && metaJSON.String != "" {
		json.Unmarshal([]byte(metaJSON.String), &entry.Metadata)
	}

	// Load associated request summary if present
	var rs store.RequestSummary
	var nPlusOne int
	err = s.db.QueryRowContext(ctx,
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

	return &entry, nil
}

func (s *logStore) SearchRequestSummaries(ctx context.Context, params store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	baseQuery := `SELECT rs.id, rs.log_id, rs.controller, rs.action, rs.method, rs.path, rs.status,
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

	var qb queryBuilder

	if params.Start != nil {
		qb.where("l.timestamp >= ?", params.Start.UTC().Format(time.RFC3339Nano))
	}
	if params.End != nil {
		qb.where("l.timestamp <= ?", params.End.UTC().Format(time.RFC3339Nano))
	}
	if params.Controller != "" {
		qb.where("rs.controller LIKE ?", "%"+params.Controller+"%")
	}
	if params.Action != "" {
		qb.where("rs.action = ?", params.Action)
	}
	if params.Path != "" {
		qb.where("rs.path LIKE ?", "%"+params.Path+"%")
	}
	if params.NPlusOneOnly {
		qb.where("rs.n_plus_one = 1")
	}
	if params.MinDurationMs > 0 {
		qb.where("rs.duration_ms >= ?", params.MinDurationMs)
	}
	if params.MinSQLCount > 0 {
		qb.where("rs.sql_count >= ?", params.MinSQLCount)
	}

	query, args := qb.build(baseQuery)

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

	var results []store.RequestSummaryResult
	for rows.Next() {
		var r store.RequestSummaryResult
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

// AggregateRequestSummaries computes aggregate metrics in a single SQL query
// instead of loading all rows into memory. Used by the watch metrics system.
func (s *logStore) AggregateRequestSummaries(ctx context.Context, params store.RequestSummaryAggregateParams) (*store.RequestSummaryAggregates, error) {
	query := `SELECT
		COUNT(*) AS cnt,
		COALESCE(AVG(rs.duration_ms), 0) AS avg_dur,
		COALESCE(AVG(rs.sql_count), 0) AS avg_sql,
		COALESCE(SUM(rs.cache_reads), 0) AS total_reads,
		COALESCE(SUM(rs.cache_hits), 0) AS total_hits
	FROM request_summaries rs
	JOIN logs l ON l.id = rs.log_id`

	var qb queryBuilder
	if params.Start != nil {
		qb.where("l.timestamp >= ?", params.Start.UTC().Format(time.RFC3339Nano))
	}
	if params.End != nil {
		qb.where("l.timestamp <= ?", params.End.UTC().Format(time.RFC3339Nano))
	}
	if params.Service != "" {
		qb.where("l.service = ?", params.Service)
	}
	if params.Endpoint != "" {
		qb.where("rs.path LIKE ?", "%"+params.Endpoint+"%")
	}

	fullQuery, args := qb.build(query)

	var agg store.RequestSummaryAggregates
	err := s.db.QueryRowContext(ctx, fullQuery, args...).Scan(
		&agg.Count, &agg.AvgDuration, &agg.AvgSQLCount, &agg.TotalReads, &agg.TotalHits,
	)
	if err != nil {
		return nil, fmt.Errorf("aggregating request summaries: %w", err)
	}
	if agg.TotalReads > 0 {
		agg.CacheHitRate = float64(agg.TotalHits) / float64(agg.TotalReads)
	}
	return &agg, nil
}

func (s *logStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano)
	var totalDeleted int64
	for {
		res, err := s.db.NewRaw(
			`DELETE FROM logs WHERE rowid IN (SELECT rowid FROM logs WHERE timestamp < ? LIMIT 1000)`, cutoff,
		).Exec(ctx)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning logs: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}

func (s *logStore) RecordBatch(ctx context.Context, batchID string, logCount int) error {
	_, err := s.db.NewRaw(`
		INSERT INTO ingest_batches (batch_id, log_count) VALUES (?, ?)`,
		batchID, logCount,
	).Exec(ctx)
	return err
}

func (s *logStore) GetBatch(ctx context.Context, batchID string) (*store.BatchRecord, error) {
	var batchIDResult string
	var logCount int
	var receivedAt string
	err := s.db.NewRaw(`
		SELECT batch_id, log_count, received_at FROM ingest_batches WHERE batch_id = ?`, batchID,
	).Scan(ctx, &batchIDResult, &logCount, &receivedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &store.BatchRecord{
		BatchID:    batchIDResult,
		LogCount:   logCount,
		ReceivedAt: parseTime(receivedAt),
	}, nil
}

func (s *logStore) PruneBatches(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano)
	res, err := s.db.NewRaw(`DELETE FROM ingest_batches WHERE received_at < ?`, cutoff).Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
