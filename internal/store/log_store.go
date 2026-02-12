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
		`INSERT INTO logs (timestamp, level, service, trace_id, message, environment, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, e := range entries {
		meta, err := json.Marshal(e.Metadata)
		if err != nil {
			return 0, fmt.Errorf("marshaling metadata: %w", err)
		}
		ts := e.Timestamp.UTC().Format(time.RFC3339Nano)
		_, err = stmt.ExecContext(ctx, ts, e.Level, e.Service, e.TraceID, e.Message, e.Environment, string(meta))
		if err != nil {
			return 0, fmt.Errorf("inserting log entry: %w", err)
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

	if params.Query != "" {
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
	if params.Environment != "" {
		conditions = append(conditions, "l.environment = ?")
		args = append(args, params.Environment)
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
		query = `SELECT l.id, l.timestamp, l.level, l.service, l.trace_id, l.message, l.environment, l.metadata
		         FROM logs l JOIN logs_fts ON l.id = logs_fts.rowid`
	} else {
		query = `SELECT l.id, l.timestamp, l.level, l.service, l.trace_id, l.message, l.environment, l.metadata
		         FROM logs l`
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	// Metadata key-value filters using json_extract.
	for k, v := range params.MetadataFilter {
		conditions = append(conditions, "json_extract(l.metadata, ?) = ?")
		args = append(args, "$."+k, v)
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
		if err := rows.Scan(
			&entry.ID, &tsStr, &entry.Level, &entry.Service,
			&entry.TraceID, &entry.Message, &entry.Environment, &metaJSON,
		); err != nil {
			return nil, fmt.Errorf("scanning log entry: %w", err)
		}
		entry.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
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
	if params.Environment != "" {
		query += ` AND environment = ?`
		args = append(args, params.Environment)
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
	if params.Environment != "" {
		query += ` AND environment = ?`
		args = append(args, params.Environment)
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
	case "environment":
		col = "environment"
	default:
		return nil, fmt.Errorf("unsupported field %q (use service, level, or environment)", field)
	}

	query := fmt.Sprintf(`SELECT DISTINCT %s FROM logs WHERE %s != '' AND timestamp >= ? AND timestamp < ?`, col, col)
	args := []any{params.Since.UTC().Format(time.RFC3339Nano), params.Until.UTC().Format(time.RFC3339Nano)}

	if params.Service != "" {
		query += ` AND service = ? COLLATE NOCASE`
		args = append(args, params.Service)
	}
	if params.Environment != "" {
		query += ` AND environment = ?`
		args = append(args, params.Environment)
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
	if params.Environment != "" {
		query += ` AND logs.environment = ?`
		args = append(args, params.Environment)
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
		`SELECT id, timestamp, level, service, trace_id, message, environment, metadata FROM logs WHERE id = ?`, id)

	var entry LogEntry
	var tsStr string
	var metaJSON sql.NullString
	if err := row.Scan(&entry.ID, &tsStr, &entry.Level, &entry.Service,
		&entry.TraceID, &entry.Message, &entry.Environment, &metaJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting log by id: %w", err)
	}
	entry.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
	if metaJSON.Valid && metaJSON.String != "" {
		if err := json.Unmarshal([]byte(metaJSON.String), &entry.Metadata); err != nil {
			slog.Warn("invalid metadata JSON in log entry", "entry_id", entry.ID, "error", err)
		}
	}
	return &entry, nil
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
