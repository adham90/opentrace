package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
)

// PgEmbeddingStore implements EmbeddingStore using pgx and pgvector.
type PgEmbeddingStore struct {
	pool *pgxpool.Pool
}

// NewPgEmbeddingStore creates a new PgEmbeddingStore.
func NewPgEmbeddingStore(pool *pgxpool.Pool) *PgEmbeddingStore {
	return &PgEmbeddingStore{pool: pool}
}

func (s *PgEmbeddingStore) UpsertChunks(ctx context.Context, chunks []CodeChunk) error {
	if len(chunks) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Delete existing chunks for all unique file paths in the batch
	paths := make(map[string]bool)
	for _, c := range chunks {
		paths[c.FilePath] = true
	}
	for path := range paths {
		if _, err := tx.Exec(ctx, "DELETE FROM code_embeddings WHERE file_path = $1", path); err != nil {
			return fmt.Errorf("deleting old chunks for %s: %w", path, err)
		}
	}

	// Insert new chunks
	for _, c := range chunks {
		vec := pgvector.NewVector(float32Slice(c.Embedding))
		_, err := tx.Exec(ctx,
			"INSERT INTO code_embeddings (file_path, chunk_index, content, embedding) VALUES ($1, $2, $3, $4)",
			c.FilePath, c.ChunkIndex, c.Content, vec,
		)
		if err != nil {
			return fmt.Errorf("inserting chunk %s:%d: %w", c.FilePath, c.ChunkIndex, err)
		}
	}

	return tx.Commit(ctx)
}

func (s *PgEmbeddingStore) Search(ctx context.Context, embedding []float64, limit int) ([]CodeSearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	vec := pgvector.NewVector(float32Slice(embedding))
	rows, err := s.pool.Query(ctx,
		`SELECT file_path, chunk_index, content, 1 - (embedding <=> $1) AS similarity
		 FROM code_embeddings
		 ORDER BY embedding <=> $1
		 LIMIT $2`,
		vec, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("searching embeddings: %w", err)
	}
	defer rows.Close()

	results := make([]CodeSearchResult, 0)
	for rows.Next() {
		var r CodeSearchResult
		if err := rows.Scan(&r.FilePath, &r.ChunkIndex, &r.Content, &r.Similarity); err != nil {
			return nil, fmt.Errorf("scanning result: %w", err)
		}
		results = append(results, r)
	}

	return results, rows.Err()
}

func (s *PgEmbeddingStore) DeleteByPath(ctx context.Context, filePath string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM code_embeddings WHERE file_path = $1", filePath)
	if err != nil {
		return fmt.Errorf("deleting by path: %w", err)
	}
	return nil
}

func (s *PgEmbeddingStore) DeleteAll(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM code_embeddings")
	if err != nil {
		return fmt.Errorf("deleting all: %w", err)
	}
	return nil
}

// float32Slice converts []float64 to []float32 for pgvector.
func float32Slice(f64 []float64) []float32 {
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}
