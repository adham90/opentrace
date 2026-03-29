package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type eventStore struct {
	db *bun.DB
}

// NewEventStore creates a new EventStore backed by SQLite.
func NewEventStore(db *bun.DB) store.EventStore {
	return &eventStore{db: db}
}

func (s *eventStore) Create(ctx context.Context, params store.CreateEventParams) (*store.Event, error) {
	now := time.Now().UTC()

	e := store.Event{
		EventType:   params.EventType,
		Source:      params.Source,
		Service:     params.Service,
		Title:       params.Title,
		Description: params.Description,
		Metadata:    params.Metadata,
		ExternalID:  params.ExternalID,
		ExternalURL: params.ExternalURL,
		Author:      params.Author,
		CreatedAt:   now,
	}

	// Ensure metadata has a default for storage
	if e.Metadata == nil {
		e.Metadata = map[string]any{}
	}

	_, err := s.db.NewInsert().Model(&e).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating event: %w", err)
	}

	return &e, nil
}

func (s *eventStore) GetByID(ctx context.Context, id int64) (*store.Event, error) {
	e := new(store.Event)
	err := s.db.NewSelect().Model(e).Where("id = ?", id).Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying event: %w", err)
	}
	return e, nil
}

// List uses dynamic WHERE clauses built with bun's query builder.
func (s *eventStore) List(ctx context.Context, params store.ListEventParams) ([]store.Event, error) {
	var events []store.Event
	q := s.db.NewSelect().Model(&events)

	if params.EventType != "" {
		q = q.Where("event_type = ?", string(params.EventType))
	}
	if params.Service != "" {
		q = q.Where("service = ?", params.Service)
	}
	if !params.Since.IsZero() {
		q = q.Where("created_at >= ?", params.Since.Format(time.RFC3339))
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	q = q.OrderExpr("created_at DESC").Limit(limit)

	err := q.Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}
	return events, nil
}

func (s *eventStore) GetByExternalID(ctx context.Context, eventType store.EventType, externalID string) (*store.Event, error) {
	e := new(store.Event)
	err := s.db.NewSelect().Model(e).
		Where("event_type = ?", string(eventType)).
		Where("external_id = ?", externalID).
		Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying event by external ID: %w", err)
	}
	return e, nil
}

func (s *eventStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		res, err := s.db.NewRaw(
			"DELETE FROM events WHERE rowid IN (SELECT e.rowid FROM events e WHERE e.created_at < ? LIMIT 1000)",
			cutoff,
		).Exec(ctx)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning events: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}

// marshalMetadataJSON is a helper for JSON metadata fields (unused after bun handles it via model tags).
func marshalMetadataJSON(m map[string]any) string {
	if m == nil {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}
