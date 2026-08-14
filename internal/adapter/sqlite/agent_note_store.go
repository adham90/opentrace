package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type agentNoteStore struct {
	db *bun.DB
}

// NewAgentNoteStore creates a new AgentNoteStore backed by SQLite.
func NewAgentNoteStore(db *bun.DB) store.AgentNoteStore {
	return &agentNoteStore{db: db}
}

// Upsert writes the note with RFC3339 timestamps. They are formatted here
// rather than handed to bun as time.Time values because Prune compares
// updated_at lexicographically against an RFC3339 cutoff, and bun's own
// rendering of a time sorts below that for the same instant.
func (s *agentNoteStore) Upsert(ctx context.Context, entityType, entityID, note string) (*store.AgentNote, error) {
	now := rfc3339(time.Now().UTC())
	_, err := s.db.NewRaw(`
		INSERT INTO agent_notes (entity_type, entity_id, note, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (entity_type, entity_id) DO UPDATE SET
			note = excluded.note,
			updated_at = excluded.updated_at`,
		entityType, entityID, note, now, now,
	).Exec(ctx)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, entityType, entityID)
}

func (s *agentNoteStore) Get(ctx context.Context, entityType, entityID string) (*store.AgentNote, error) {
	var note store.AgentNote
	err := s.db.NewSelect().Model(&note).
		Where("entity_type = ?", entityType).
		Where("entity_id = ?", entityID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &note, nil
}

func (s *agentNoteStore) List(ctx context.Context, entityType string) ([]store.AgentNote, error) {
	var notes []store.AgentNote
	q := s.db.NewSelect().Model(&notes).OrderExpr("updated_at DESC")
	if entityType != "" {
		q = q.Where("entity_type = ?", entityType)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	return notes, nil
}

func (s *agentNoteStore) Delete(ctx context.Context, entityType, entityID string) error {
	res, err := s.db.NewDelete().Model((*store.AgentNote)(nil)).
		Where("entity_type = ?", entityType).
		Where("entity_id = ?", entityID).
		Exec(ctx)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *agentNoteStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := rfc3339(time.Now().UTC().Add(-olderThan))
	res, err := s.db.NewDelete().Model((*store.AgentNote)(nil)).
		Where("updated_at < ?", cutoff).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
