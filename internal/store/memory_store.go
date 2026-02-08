package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgMemoryStore implements MemoryStore using pgx.
type PgMemoryStore struct {
	pool *pgxpool.Pool
}

// NewPgMemoryStore creates a new PgMemoryStore.
func NewPgMemoryStore(pool *pgxpool.Pool) *PgMemoryStore {
	return &PgMemoryStore{pool: pool}
}

func (s *PgMemoryStore) AddMemory(ctx context.Context, category, content, source string) error {
	// Upsert: if same content exists, update updated_at and source
	_, err := s.pool.Exec(ctx,
		`INSERT INTO agent_memory (category, content, source)
		 VALUES ($1, $2, $3)
		 ON CONFLICT ((content)) DO NOTHING`,
		category, content, nilIfEmpty(source))
	if err != nil {
		// Fallback: ON CONFLICT on content requires a unique index.
		// Since we don't have one, just do a check-then-insert.
		return s.addMemoryWithDedup(ctx, category, content, source)
	}
	return nil
}

func (s *PgMemoryStore) addMemoryWithDedup(ctx context.Context, category, content, source string) error {
	// Check if exact content already exists
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM agent_memory WHERE content = $1)`, content).Scan(&exists)
	if err != nil {
		return fmt.Errorf("checking memory existence: %w", err)
	}
	if exists {
		// Update the existing entry's timestamp and source
		_, err = s.pool.Exec(ctx,
			`UPDATE agent_memory SET updated_at = now(), source = COALESCE($1, source)
			 WHERE content = $2`, nilIfEmpty(source), content)
		return err
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO agent_memory (category, content, source) VALUES ($1, $2, $3)`,
		category, content, nilIfEmpty(source))
	if err != nil {
		return fmt.Errorf("adding memory: %w", err)
	}
	return nil
}

func (s *PgMemoryStore) ListMemories(ctx context.Context, category string) ([]MemoryEntry, error) {
	var query string
	var args []any

	if category != "" {
		query = `SELECT id, category, content, COALESCE(source, ''), created_at, updated_at
				 FROM agent_memory WHERE category = $1 ORDER BY updated_at DESC`
		args = []any{category}
	} else {
		query = `SELECT id, category, content, COALESCE(source, ''), created_at, updated_at
				 FROM agent_memory ORDER BY updated_at DESC`
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing memories: %w", err)
	}
	defer rows.Close()

	var entries []MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		if err := rows.Scan(&e.ID, &e.Category, &e.Content, &e.Source, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning memory: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *PgMemoryStore) SearchMemories(ctx context.Context, query string) ([]MemoryEntry, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, category, content, COALESCE(source, ''), created_at, updated_at
		 FROM agent_memory
		 WHERE to_tsvector('english', content) @@ plainto_tsquery('english', $1)
		 ORDER BY updated_at DESC
		 LIMIT 20`, query)
	if err != nil {
		return nil, fmt.Errorf("searching memories: %w", err)
	}
	defer rows.Close()

	var entries []MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		if err := rows.Scan(&e.ID, &e.Category, &e.Content, &e.Source, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning memory: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *PgMemoryStore) DeleteMemory(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM agent_memory WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting memory: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
