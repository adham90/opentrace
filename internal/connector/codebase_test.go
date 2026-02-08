package connector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/opentrace/opentrace/internal/store"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestChunkFile_Small(t *testing.T) {
	chunks := ChunkFile("test.go", "package main\nfunc main() {}")
	if len(chunks) != 1 {
		t.Fatalf("len = %d, want 1", len(chunks))
	}
	if chunks[0].FilePath != "test.go" {
		t.Errorf("file = %q, want %q", chunks[0].FilePath, "test.go")
	}
	if chunks[0].ChunkIndex != 0 {
		t.Errorf("chunk index = %d, want 0", chunks[0].ChunkIndex)
	}
}

func TestChunkFile_Large(t *testing.T) {
	// Create content larger than chunkSize (2000 chars)
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("// This is a line of code that takes up some space in the file\n")
	}
	content := sb.String()

	chunks := ChunkFile("big.go", content)
	if len(chunks) < 2 {
		t.Fatalf("len = %d, want >= 2 for large content (%d bytes)", len(chunks), len(content))
	}
	// Verify chunk indices are sequential
	for i, c := range chunks {
		if c.ChunkIndex != i {
			t.Errorf("chunk[%d].ChunkIndex = %d, want %d", i, c.ChunkIndex, i)
		}
	}
}

func TestChunkFile_Empty(t *testing.T) {
	chunks := ChunkFile("empty.go", "")
	if len(chunks) != 0 {
		t.Fatalf("len = %d, want 0", len(chunks))
	}
}

func TestCodebaseConnector_Type(t *testing.T) {
	c := &CodebaseConnector{repoPath: "/tmp"}
	if c.Type() != ConnectorCodebase {
		t.Fatalf("Type() = %q, want %q", c.Type(), ConnectorCodebase)
	}
}

func TestCodebaseConnector_Tools(t *testing.T) {
	c := &CodebaseConnector{repoPath: "/tmp"}
	tools := c.Tools()
	if len(tools) != 1 {
		t.Fatalf("len(Tools()) = %d, want 1", len(tools))
	}
	if tools[0].Name != "code_search" {
		t.Fatalf("tool name = %q, want %q", tools[0].Name, "code_search")
	}
}

// --- Integration tests ---

type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	// Simple deterministic embedding: hash text to position
	pos := 0
	for _, c := range text {
		pos += int(c)
	}
	vec := make([]float64, m.dim)
	vec[pos%m.dim] = 1.0
	return vec, nil
}

func (m *mockEmbedder) Dimension() int { return m.dim }

func setupCodebaseDB(t *testing.T) (*store.PgEmbeddingStore, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pgContainer, err := postgres.Run(ctx,
		"pgvector/pgvector:pg16",
		postgres.WithDatabase("codebase_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		pgContainer.Terminate(ctx)
		t.Fatalf("failed to get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		pgContainer.Terminate(ctx)
		t.Fatalf("failed to create pool: %v", err)
	}

	migrationsPath := "../../migrations"
	if err := store.RunMigrations(connStr, migrationsPath); err != nil {
		pool.Close()
		pgContainer.Terminate(ctx)
		t.Fatalf("failed to run migrations: %v", err)
	}

	return store.NewPgEmbeddingStore(pool), func() {
		pool.Close()
		pgContainer.Terminate(ctx)
	}
}

func TestCodebaseConnector_IndexRepo(t *testing.T) {
	embStore, cleanup := setupCodebaseDB(t)
	defer cleanup()

	// Create temp repo
	tmpDir, err := os.MkdirTemp("", "codebase-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\nfunc main() {}"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "util.go"), []byte("package main\nfunc helper() string { return \"hello\" }"), 0644)

	embedder := &mockEmbedder{dim: 768}
	ctx := context.Background()

	c, err := NewCodebaseConnector(ctx, tmpDir, embStore, embedder)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}

	// Verify embeddings were stored
	vec := make([]float64, 768)
	vec[0] = 1.0
	results, err := embStore.Search(ctx, vec, 10)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	_ = c // connector created successfully
}

func TestCodeSearch_EndToEnd(t *testing.T) {
	embStore, cleanup := setupCodebaseDB(t)
	defer cleanup()

	tmpDir, err := os.MkdirTemp("", "codesearch-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\nfunc main() { fmt.Println(\"hello\") }"), 0644)

	embedder := &mockEmbedder{dim: 768}
	ctx := context.Background()

	c, err := NewCodebaseConnector(ctx, tmpDir, embStore, embedder)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}

	tools := c.Tools()
	result, err := tools[0].Handler(ctx, map[string]any{"query": "main function"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "main.go") {
		t.Errorf("expected result to contain main.go, got: %s", result)
	}
}
