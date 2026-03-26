package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type agentNoteStore struct {
	db *sql.DB
	q  *Queries
}

// NewAgentNoteStore creates a new AgentNoteStore backed by SQLite.
func NewAgentNoteStore(db *sql.DB) store.AgentNoteStore {
	return &agentNoteStore{db: db, q: New(db)}
}

func (s *agentNoteStore) Upsert(ctx context.Context, entityType, entityID, note string) (*store.AgentNote, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	row, err := s.q.UpsertAgentNote(ctx, UpsertAgentNoteParams{
		EntityType: entityType,
		EntityID:   entityID,
		Note:       note,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		return nil, err
	}
	result := toStoreAgentNote(row)
	return &result, nil
}

func (s *agentNoteStore) Get(ctx context.Context, entityType, entityID string) (*store.AgentNote, error) {
	row, err := s.q.GetAgentNote(ctx, GetAgentNoteParams{
		EntityType: entityType,
		EntityID:   entityID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	result := toStoreAgentNote(row)
	return &result, nil
}

func (s *agentNoteStore) List(ctx context.Context, entityType string) ([]store.AgentNote, error) {
	if entityType != "" {
		rows, err := s.q.ListAgentNotesByType(ctx, entityType)
		if err != nil {
			return nil, err
		}
		return toStoreAgentNotes(rows), nil
	}
	rows, err := s.q.ListAllAgentNotes(ctx)
	if err != nil {
		return nil, err
	}
	return toStoreAgentNotes(rows), nil
}

func (s *agentNoteStore) Delete(ctx context.Context, entityType, entityID string) error {
	n, err := s.q.DeleteAgentNote(ctx, DeleteAgentNoteParams{
		EntityType: entityType,
		EntityID:   entityID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *agentNoteStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	return s.q.PruneAgentNotes(ctx, cutoff)
}
