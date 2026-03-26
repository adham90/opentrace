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

type eventStore struct {
	db *sql.DB
	q  *Queries
}

// NewEventStore creates a new EventStore backed by SQLite.
func NewEventStore(db *sql.DB) store.EventStore {
	return &eventStore{db: db, q: New(db)}
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

	row, err := s.q.CreateEvent(ctx, CreateEventParams{
		EventType:    string(params.EventType),
		Source:       params.Source,
		Service:      params.Service,
		Title:        params.Title,
		Description:  params.Description,
		MetadataJson: metadataJSON,
		ExternalID:   params.ExternalID,
		ExternalUrl:  params.ExternalURL,
		Author:       params.Author,
		CreatedAt:    now,
	})
	if err != nil {
		return nil, fmt.Errorf("creating event: %w", err)
	}

	e := toStoreEvent(row)
	return &e, nil
}

func (s *eventStore) GetByID(ctx context.Context, id int64) (*store.Event, error) {
	row, err := s.q.GetEventByID(ctx, id)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying event: %w", err)
	}
	e := toStoreEvent(row)
	return &e, nil
}

// List uses dynamic WHERE clauses — kept hand-written.
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
	row, err := s.q.GetEventByExternalID(ctx, GetEventByExternalIDParams{
		EventType:  string(eventType),
		ExternalID: externalID,
	})
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying event by external ID: %w", err)
	}
	e := toStoreEvent(row)
	return &e, nil
}

func (s *eventStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		n, err := s.q.PruneEvents(ctx, cutoff)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning events: %w", err)
		}
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
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
		_ = json.Unmarshal([]byte(metadataJSON), &e.Metadata)
	}
	e.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &e, nil
}
