package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgLogStore implements LogStore using pgx.
type PgLogStore struct {
	pool *pgxpool.Pool
}

// NewPgLogStore creates a new PgLogStore.
func NewPgLogStore(pool *pgxpool.Pool) *PgLogStore {
	return &PgLogStore{pool: pool}
}

func (s *PgLogStore) BatchInsert(ctx context.Context, entries []LogEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	rows := make([][]any, len(entries))
	for i, e := range entries {
		meta, err := json.Marshal(e.Metadata)
		if err != nil {
			return 0, fmt.Errorf("marshaling metadata: %w", err)
		}
		rows[i] = []any{e.Timestamp, e.Level, e.Service, e.TraceID, e.Message, e.Environment, meta}
	}

	count, err := s.pool.CopyFrom(ctx,
		pgx.Identifier{"logs"},
		[]string{"timestamp", "level", "service", "trace_id", "message", "environment", "metadata"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return 0, fmt.Errorf("batch insert: %w", err)
	}

	return int(count), nil
}

func (s *PgLogStore) Search(ctx context.Context, params LogSearchParams) ([]LogEntry, error) {
	var conditions []string
	var args []any
	argN := 1

	if params.Query != "" {
		conditions = append(conditions, fmt.Sprintf("to_tsvector('english', message) @@ plainto_tsquery('english', $%d)", argN))
		args = append(args, params.Query)
		argN++
	}
	if params.Service != "" {
		conditions = append(conditions, fmt.Sprintf("LOWER(service) = LOWER($%d)", argN))
		args = append(args, params.Service)
		argN++
	}
	if params.Level != "" {
		conditions = append(conditions, fmt.Sprintf("LOWER(level) = LOWER($%d)", argN))
		args = append(args, params.Level)
		argN++
	}
	if params.TraceID != "" {
		conditions = append(conditions, fmt.Sprintf("trace_id = $%d", argN))
		args = append(args, params.TraceID)
		argN++
	}
	if params.Environment != "" {
		conditions = append(conditions, fmt.Sprintf("environment = $%d", argN))
		args = append(args, params.Environment)
		argN++
	}
	if params.Start != nil {
		conditions = append(conditions, fmt.Sprintf("timestamp >= $%d", argN))
		args = append(args, *params.Start)
		argN++
	}
	if params.End != nil {
		conditions = append(conditions, fmt.Sprintf("timestamp <= $%d", argN))
		args = append(args, *params.End)
		argN++
	}

	query := "SELECT id, timestamp, level, service, trace_id, message, environment, metadata, created_at FROM logs"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY timestamp DESC"

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	query += fmt.Sprintf(" LIMIT $%d", argN)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching logs: %w", err)
	}
	defer rows.Close()

	result := make([]LogEntry, 0)
	for rows.Next() {
		var entry LogEntry
		var metaJSON []byte
		if err := rows.Scan(
			&entry.ID, &entry.Timestamp, &entry.Level, &entry.Service,
			&entry.TraceID, &entry.Message, &entry.Environment, &metaJSON, &entry.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning log entry: %w", err)
		}
		if metaJSON != nil {
			if err := json.Unmarshal(metaJSON, &entry.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshaling metadata: %w", err)
			}
		}
		result = append(result, entry)
	}

	return result, rows.Err()
}
