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

func (s *agentNoteStore) Upsert(ctx context.Context, entityType, entityID, note string) (*store.AgentNote, error) {
	now := time.Now().UTC()
	an := &store.AgentNote{
		EntityType: entityType,
		EntityID:   entityID,
		Note:       note,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	_, err := s.db.NewInsert().Model(an).
		On("CONFLICT (entity_type, entity_id) DO UPDATE").
		Set("note = EXCLUDED.note").
		Set("updated_at = EXCLUDED.updated_at").
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	return an, nil
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
	cutoff := time.Now().UTC().Add(-olderThan)
	res, err := s.db.NewDelete().Model((*store.AgentNote)(nil)).
		Where("updated_at < ?", cutoff.Format(time.RFC3339)).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
