package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupLogStore(t *testing.T) (*PgLogStore, func()) {
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

	return NewPgLogStore(pool), func() {
		pool.Close()
		cleanup()
	}
}

func TestBatchInsert_Success(t *testing.T) {
	store, cleanup := setupLogStore(t)
	defer cleanup()

	entries := []LogEntry{
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "request received"},
		{Timestamp: time.Now(), Level: "ERROR", Service: "api", Message: "database connection failed"},
		{Timestamp: time.Now(), Level: "WARN", Service: "worker", Message: "slow query detected"},
	}

	count, err := store.BatchInsert(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
}

func TestBatchInsert_Empty(t *testing.T) {
	store, cleanup := setupLogStore(t)
	defer cleanup()

	count, err := store.BatchInsert(context.Background(), []LogEntry{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}

func TestLogSearch_FTS(t *testing.T) {
	store, cleanup := setupLogStore(t)
	defer cleanup()

	ctx := context.Background()
	entries := []LogEntry{
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "user authentication successful"},
		{Timestamp: time.Now(), Level: "ERROR", Service: "api", Message: "database connection timeout"},
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "user logged out"},
	}
	store.BatchInsert(ctx, entries)

	results, err := store.Search(ctx, LogSearchParams{Query: "authentication"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	if results[0].Message != "user authentication successful" {
		t.Errorf("message = %q, unexpected", results[0].Message)
	}
}

func TestLogSearch_FilterByService(t *testing.T) {
	store, cleanup := setupLogStore(t)
	defer cleanup()

	ctx := context.Background()
	entries := []LogEntry{
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "msg one"},
		{Timestamp: time.Now(), Level: "INFO", Service: "worker", Message: "msg two"},
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "msg three"},
	}
	store.BatchInsert(ctx, entries)

	results, err := store.Search(ctx, LogSearchParams{Service: "worker"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
}

func TestLogSearch_FilterByLevel(t *testing.T) {
	store, cleanup := setupLogStore(t)
	defer cleanup()

	ctx := context.Background()
	entries := []LogEntry{
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "info msg"},
		{Timestamp: time.Now(), Level: "ERROR", Service: "api", Message: "error msg"},
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "another info"},
	}
	store.BatchInsert(ctx, entries)

	results, err := store.Search(ctx, LogSearchParams{Level: "ERROR"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
}

func TestLogSearch_TimeBounds(t *testing.T) {
	store, cleanup := setupLogStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()
	entries := []LogEntry{
		{Timestamp: now.Add(-2 * time.Hour), Level: "INFO", Service: "api", Message: "old msg"},
		{Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api", Message: "recent msg"},
		{Timestamp: now.Add(-5 * time.Minute), Level: "INFO", Service: "api", Message: "very recent msg"},
	}
	store.BatchInsert(ctx, entries)

	start := now.Add(-1 * time.Hour)
	end := now
	results, err := store.Search(ctx, LogSearchParams{Start: &start, End: &end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
}

func TestLogSearch_NoResults(t *testing.T) {
	store, cleanup := setupLogStore(t)
	defer cleanup()

	results, err := store.Search(context.Background(), LogSearchParams{Service: "nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("len = %d, want 0", len(results))
	}
}
