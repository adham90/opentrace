package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type eventStore struct {
	db *sql.DB
}

// NewEventStore creates a new EventStore backed by SQLite.
func NewEventStore(db *sql.DB) store.EventStore {
	return &eventStore{db: db}
}

func (s *eventStore) Create(ctx context.Context, params store.CreateEventParams) (*store.Event, error) {
	metadataJSON := "{}"
	if params.Metadata != nil {
		b, err := json.Marshal(params.Metadata)
		if err != nil {
			return nil, fmt.Errorf("marshaling event metadata: %w", err)
		}
		metadataJSON = string(b)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO events (event_type, source, service, title, description, metadata_json, external_id, external_url, author, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(params.EventType), params.Source, params.Service,
		params.Title, params.Description, metadataJSON,
		params.ExternalID, params.ExternalURL, params.Author, now,
	)
	if err != nil {
		return nil, fmt.Errorf("creating event: %w", err)
	}

	id, _ := result.LastInsertId()
	return s.GetByID(ctx, id)
}

func (s *eventStore) GetByID(ctx context.Context, id int64) (*store.Event, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, event_type, source, service, title, description, metadata_json, external_id, external_url, author, created_at
		 FROM events WHERE id = ?`, id)

	return scanEvent(row)
}

func (s *eventStore) List(ctx context.Context, params store.ListEventParams) ([]store.Event, error) {
	var where []string
	var args []any

	if params.EventType != "" {
		where = append(where, "event_type = ?")
		args = append(args, string(params.EventType))
	}
	if params.Service != "" {
		where = append(where, "service = ?")
		args = append(args, params.Service)
	}
	if !params.Since.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, params.Since.Format(time.RFC3339))
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	query := fmt.Sprintf(
		`SELECT id, event_type, source, service, title, description, metadata_json, external_id, external_url, author, created_at
		 FROM events %s ORDER BY created_at DESC LIMIT ?`, whereClause)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}
	defer rows.Close()

	var events []store.Event
	for rows.Next() {
		e, err := scanEventRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		events = append(events, *e)
	}
	return events, rows.Err()
}

func (s *eventStore) GetByExternalID(ctx context.Context, eventType store.EventType, externalID string) (*store.Event, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, event_type, source, service, title, description, metadata_json, external_id, external_url, author, created_at
		 FROM events WHERE event_type = ? AND external_id = ?`,
		string(eventType), externalID)

	e, err := scanEvent(row)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	return e, err
}

func (s *eventStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		result, err := s.db.ExecContext(ctx,
			`DELETE FROM events WHERE rowid IN (SELECT rowid FROM events WHERE created_at < ? LIMIT 1000)`,
			cutoff)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning events: %w", err)
		}
		n, _ := result.RowsAffected()
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}

func scanEvent(row *sql.Row) (*store.Event, error) {
	var e store.Event
	var metadataJSON, createdAt string

	err := row.Scan(
		&e.ID, &e.EventType, &e.Source, &e.Service,
		&e.Title, &e.Description, &metadataJSON,
		&e.ExternalID, &e.ExternalURL, &e.Author, &createdAt,
	)
	if err != nil {
		return nil, err
	}

	if metadataJSON != "" && metadataJSON != "{}" {
		if err := json.Unmarshal([]byte(metadataJSON), &e.Metadata); err != nil {
			slog.Warn("event: malformed metadata_json", "id", e.ID, "error", err)
		}
	}
	e.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &e, nil
}

func scanEventRow(rows *sql.Rows) (*store.Event, error) {
	var e store.Event
	var metadataJSON, createdAt string

	err := rows.Scan(
		&e.ID, &e.EventType, &e.Source, &e.Service,
		&e.Title, &e.Description, &metadataJSON,
		&e.ExternalID, &e.ExternalURL, &e.Author, &createdAt,
	)
	if err != nil {
		return nil, err
	}

	if metadataJSON != "" && metadataJSON != "{}" {
		if err := json.Unmarshal([]byte(metadataJSON), &e.Metadata); err != nil {
			slog.Warn("event: malformed metadata_json", "id", e.ID, "error", err)
		}
	}
	e.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &e, nil
}
