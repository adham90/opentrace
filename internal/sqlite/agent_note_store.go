package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type agentNoteStore struct {
	db *sql.DB
}

// NewAgentNoteStore creates a new AgentNoteStore backed by SQLite.
func NewAgentNoteStore(db *sql.DB) store.AgentNoteStore {
	return &agentNoteStore{db: db}
}

func (s *agentNoteStore) Upsert(ctx context.Context, entityType, entityID, note string) (*store.AgentNote, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_notes (entity_type, entity_id, note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(entity_type, entity_id) DO UPDATE SET note = excluded.note, updated_at = excluded.updated_at`,
		entityType, entityID, note, now, now,
	)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, entityType, entityID)
}

func (s *agentNoteStore) Get(ctx context.Context, entityType, entityID string) (*store.AgentNote, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, entity_type, entity_id, note, created_at, updated_at
		 FROM agent_notes WHERE entity_type = ? AND entity_id = ?`,
		entityType, entityID,
	)

	var n store.AgentNote
	var createdStr, updatedStr string
	err := row.Scan(&n.ID, &n.EntityType, &n.EntityID, &n.Note, &createdStr, &updatedStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	n.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	n.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return &n, nil
}

func (s *agentNoteStore) List(ctx context.Context, entityType string) ([]store.AgentNote, error) {
	var rows *sql.Rows
	var err error
	if entityType != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, entity_type, entity_id, note, created_at, updated_at
			 FROM agent_notes WHERE entity_type = ? ORDER BY updated_at DESC`, entityType)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, entity_type, entity_id, note, created_at, updated_at
			 FROM agent_notes ORDER BY updated_at DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []store.AgentNote
	for rows.Next() {
		var n store.AgentNote
		var createdStr, updatedStr string
		if err := rows.Scan(&n.ID, &n.EntityType, &n.EntityID, &n.Note, &createdStr, &updatedStr); err != nil {
			return nil, err
		}
		n.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		n.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

func (s *agentNoteStore) Delete(ctx context.Context, entityType, entityID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM agent_notes WHERE entity_type = ? AND entity_id = ?`,
		entityType, entityID,
	)
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
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `DELETE FROM agent_notes WHERE updated_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
