package store

import (
	"context"
	"testing"
	"time"
)

func TestBatchInsert_Success(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)

	entries := []LogEntry{
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "request received"},
		{Timestamp: time.Now(), Level: "ERROR", Service: "api", Message: "database connection failed"},
		{Timestamp: time.Now(), Level: "WARN", Service: "worker", Message: "slow query detected"},
	}

	count, err := s.BatchInsert(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
}

func TestBatchInsert_Empty(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)

	count, err := s.BatchInsert(context.Background(), []LogEntry{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}

func TestLogSearch_FTS(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	entries := []LogEntry{
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "user authentication successful"},
		{Timestamp: time.Now(), Level: "ERROR", Service: "api", Message: "database connection timeout"},
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "user logged out"},
	}
	s.BatchInsert(ctx, entries)

	results, err := s.Search(ctx, LogSearchParams{Query: "authentication"})
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
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	entries := []LogEntry{
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "msg one"},
		{Timestamp: time.Now(), Level: "INFO", Service: "worker", Message: "msg two"},
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "msg three"},
	}
	s.BatchInsert(ctx, entries)

	results, err := s.Search(ctx, LogSearchParams{Service: "worker"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
}

func TestLogSearch_FilterByLevel(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	entries := []LogEntry{
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "info msg"},
		{Timestamp: time.Now(), Level: "ERROR", Service: "api", Message: "error msg"},
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "another info"},
	}
	s.BatchInsert(ctx, entries)

	results, err := s.Search(ctx, LogSearchParams{Level: "ERROR"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
}

func TestLogSearch_TimeBounds(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	now := time.Now()
	entries := []LogEntry{
		{Timestamp: now.Add(-2 * time.Hour), Level: "INFO", Service: "api", Message: "old msg"},
		{Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api", Message: "recent msg"},
		{Timestamp: now.Add(-5 * time.Minute), Level: "INFO", Service: "api", Message: "very recent msg"},
	}
	s.BatchInsert(ctx, entries)

	start := now.Add(-1 * time.Hour)
	end := now
	results, err := s.Search(ctx, LogSearchParams{Start: &start, End: &end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
}

func TestLogSearch_NoResults(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)

	results, err := s.Search(context.Background(), LogSearchParams{Service: "nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("len = %d, want 0", len(results))
	}
}

func TestLogStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	now := time.Now()
	entries := []LogEntry{
		{Timestamp: now.Add(-48 * time.Hour), Level: "INFO", Service: "api", Message: "old msg"},
		{Timestamp: now.Add(-72 * time.Hour), Level: "ERROR", Service: "api", Message: "very old msg"},
		{Timestamp: now.Add(-1 * time.Hour), Level: "INFO", Service: "api", Message: "recent msg"},
	}
	_, err := s.BatchInsert(ctx, entries)
	if err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	// Prune entries older than 24 hours
	pruned, err := s.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if pruned != 2 {
		t.Errorf("pruned = %d, want 2", pruned)
	}

	// Verify only the recent one remains
	results, err := s.Search(ctx, LogSearchParams{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("remaining = %d, want 1", len(results))
	}
	if results[0].Message != "recent msg" {
		t.Errorf("message = %q, want %q", results[0].Message, "recent msg")
	}
}
