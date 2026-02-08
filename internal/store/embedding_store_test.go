package store

import (
	"context"
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupEmbeddingStore(t *testing.T) (*PgEmbeddingStore, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	connStr, cleanup := setupTestContainer(t)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		cleanup()
		t.Fatalf("failed to create pool: %v", err)
	}

	migrationsPath := "../../migrations"
	if err := RunMigrations(connStr, migrationsPath); err != nil {
		pool.Close()
		cleanup()
		t.Fatalf("failed to run migrations: %v", err)
	}

	return NewPgEmbeddingStore(pool), func() {
		pool.Close()
		cleanup()
	}
}

// makeEmbedding creates a normalized embedding of the given dimension with a value at pos.
func makeEmbedding(dim, pos int) []float64 {
	vec := make([]float64, dim)
	vec[pos%dim] = 1.0
	return vec
}

func TestUpsertChunks_Insert(t *testing.T) {
	store, cleanup := setupEmbeddingStore(t)
	defer cleanup()

	ctx := context.Background()
	chunks := []CodeChunk{
		{FilePath: "main.go", ChunkIndex: 0, Content: "package main", Embedding: makeEmbedding(768, 0)},
		{FilePath: "main.go", ChunkIndex: 1, Content: "func main() {}", Embedding: makeEmbedding(768, 1)},
	}

	if err := store.UpsertChunks(ctx, chunks); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify by searching
	results, err := store.Search(ctx, makeEmbedding(768, 0), 10)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
}

func TestUpsertChunks_Replace(t *testing.T) {
	store, cleanup := setupEmbeddingStore(t)
	defer cleanup()

	ctx := context.Background()

	// First insert
	chunks1 := []CodeChunk{
		{FilePath: "main.go", ChunkIndex: 0, Content: "old content", Embedding: makeEmbedding(768, 0)},
	}
	store.UpsertChunks(ctx, chunks1)

	// Upsert replaces
	chunks2 := []CodeChunk{
		{FilePath: "main.go", ChunkIndex: 0, Content: "new content", Embedding: makeEmbedding(768, 1)},
	}
	store.UpsertChunks(ctx, chunks2)

	results, err := store.Search(ctx, makeEmbedding(768, 1), 10)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1 (old should be replaced)", len(results))
	}
	if results[0].Content != "new content" {
		t.Errorf("content = %q, want %q", results[0].Content, "new content")
	}
}

func TestEmbeddingSearch_CosineSimilarity(t *testing.T) {
	store, cleanup := setupEmbeddingStore(t)
	defer cleanup()

	ctx := context.Background()
	chunks := []CodeChunk{
		{FilePath: "a.go", ChunkIndex: 0, Content: "file a", Embedding: makeEmbedding(768, 0)},
		{FilePath: "b.go", ChunkIndex: 0, Content: "file b", Embedding: makeEmbedding(768, 1)},
		{FilePath: "c.go", ChunkIndex: 0, Content: "file c", Embedding: makeEmbedding(768, 2)},
	}
	store.UpsertChunks(ctx, chunks)

	// Search with vector matching a.go
	results, err := store.Search(ctx, makeEmbedding(768, 0), 3)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len = %d, want 3", len(results))
	}
	// First result should be a.go (highest similarity)
	if results[0].FilePath != "a.go" {
		t.Errorf("first result = %q, want %q", results[0].FilePath, "a.go")
	}
	// Similarity should be ~1.0 for exact match
	if math.Abs(results[0].Similarity-1.0) > 0.01 {
		t.Errorf("similarity = %f, want ~1.0", results[0].Similarity)
	}
}

func TestDeleteByPath(t *testing.T) {
	store, cleanup := setupEmbeddingStore(t)
	defer cleanup()

	ctx := context.Background()
	chunks := []CodeChunk{
		{FilePath: "a.go", ChunkIndex: 0, Content: "file a", Embedding: makeEmbedding(768, 0)},
		{FilePath: "b.go", ChunkIndex: 0, Content: "file b", Embedding: makeEmbedding(768, 1)},
	}
	store.UpsertChunks(ctx, chunks)

	if err := store.DeleteByPath(ctx, "a.go"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	results, err := store.Search(ctx, makeEmbedding(768, 0), 10)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	if results[0].FilePath != "b.go" {
		t.Errorf("file = %q, want %q", results[0].FilePath, "b.go")
	}
}

func TestDeleteAll(t *testing.T) {
	store, cleanup := setupEmbeddingStore(t)
	defer cleanup()

	ctx := context.Background()
	chunks := []CodeChunk{
		{FilePath: "a.go", ChunkIndex: 0, Content: "file a", Embedding: makeEmbedding(768, 0)},
		{FilePath: "b.go", ChunkIndex: 0, Content: "file b", Embedding: makeEmbedding(768, 1)},
	}
	store.UpsertChunks(ctx, chunks)

	if err := store.DeleteAll(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	results, err := store.Search(ctx, makeEmbedding(768, 0), 10)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("len = %d, want 0", len(results))
	}
}
