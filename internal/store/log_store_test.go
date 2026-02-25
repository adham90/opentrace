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

func TestSearch_MetadataExact(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	entries := []LogEntry{
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "req 1", Metadata: map[string]any{"host": "server-01", "status_code": "200"}},
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "req 2", Metadata: map[string]any{"host": "server-02", "status_code": "500"}},
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "req 3", Metadata: map[string]any{"host": "server-01", "status_code": "500"}},
	}
	s.BatchInsert(ctx, entries)

	results, err := s.Search(ctx, LogSearchParams{MetadataFilter: map[string]string{"host": "server-01"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
}

func TestSearch_MetadataContains(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	entries := []LogEntry{
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "req 1", Metadata: map[string]any{"path": "/api/users/123"}},
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "req 2", Metadata: map[string]any{"path": "/api/posts/456"}},
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "req 3", Metadata: map[string]any{"path": "/admin/settings"}},
	}
	s.BatchInsert(ctx, entries)

	// Use ~ prefix for LIKE match
	results, err := s.Search(ctx, LogSearchParams{MetadataFilter: map[string]string{"path": "~users"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}

	// Broader match
	results, err = s.Search(ctx, LogSearchParams{MetadataFilter: map[string]string{"path": "~/api"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
}

func TestSearch_MetadataExists(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	entries := []LogEntry{
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "req 1", Metadata: map[string]any{"user_id": "42"}},
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "req 2", Metadata: map[string]any{"host": "server-01"}},
		{Timestamp: time.Now(), Level: "INFO", Service: "api", Message: "req 3", Metadata: map[string]any{"user_id": "99", "host": "server-02"}},
	}
	s.BatchInsert(ctx, entries)

	// Use * for existence check
	results, err := s.Search(ctx, LogSearchParams{MetadataFilter: map[string]string{"user_id": "*"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
}

func TestSearchRequestSummaries_NPlusOne(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	now := time.Now()
	entries := []LogEntry{
		{
			Timestamp: now.Add(-1 * time.Hour), Level: "INFO", Service: "web", Message: "GET /users",
			RequestSummary: &RequestSummary{
				Controller: "UsersController", Action: "index", Method: "GET", Path: "/users",
				DurationMs: 250, SQLCount: 20, NPlusOne: true, DuplicateQueries: 3, WorstDuplicateCount: 15,
			},
		},
		{
			Timestamp: now.Add(-2 * time.Hour), Level: "INFO", Service: "web", Message: "GET /posts",
			RequestSummary: &RequestSummary{
				Controller: "PostsController", Action: "index", Method: "GET", Path: "/posts",
				DurationMs: 50, SQLCount: 3, NPlusOne: false,
			},
		},
		{
			Timestamp: now.Add(-3 * time.Hour), Level: "INFO", Service: "web", Message: "GET /comments",
			RequestSummary: &RequestSummary{
				Controller: "CommentsController", Action: "index", Method: "GET", Path: "/comments",
				DurationMs: 500, SQLCount: 40, NPlusOne: true, DuplicateQueries: 5, WorstDuplicateCount: 30,
			},
		},
	}
	_, err := s.BatchInsert(ctx, entries)
	if err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	// Query N+1 only
	start := now.Add(-24 * time.Hour)
	end := now
	results, err := s.SearchRequestSummaries(ctx, RequestSummarySearchParams{
		Start:         &start,
		End:           &end,
		NPlusOneOnly:  true,
	})
	if err != nil {
		t.Fatalf("SearchRequestSummaries: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
	for _, r := range results {
		if !r.NPlusOne {
			t.Errorf("expected NPlusOne=true, got false for log_id=%d", r.LogID)
		}
	}
}

func TestSearchRequestSummaries_Filters(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	now := time.Now()
	entries := []LogEntry{
		{
			Timestamp: now.Add(-1 * time.Hour), Level: "INFO", Service: "web", Message: "req 1",
			RequestSummary: &RequestSummary{
				Controller: "UsersController", Action: "index", Path: "/users",
				DurationMs: 200, SQLCount: 10,
			},
		},
		{
			Timestamp: now.Add(-2 * time.Hour), Level: "INFO", Service: "web", Message: "req 2",
			RequestSummary: &RequestSummary{
				Controller: "UsersController", Action: "show", Path: "/users/1",
				DurationMs: 1500, SQLCount: 25,
			},
		},
		{
			Timestamp: now.Add(-3 * time.Hour), Level: "INFO", Service: "web", Message: "req 3",
			RequestSummary: &RequestSummary{
				Controller: "PostsController", Action: "index", Path: "/posts",
				DurationMs: 100, SQLCount: 5,
			},
		},
	}
	_, err := s.BatchInsert(ctx, entries)
	if err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	start := now.Add(-24 * time.Hour)
	end := now

	// Filter by controller (LIKE match)
	results, err := s.SearchRequestSummaries(ctx, RequestSummarySearchParams{
		Start: &start, End: &end, Controller: "Users",
	})
	if err != nil {
		t.Fatalf("SearchRequestSummaries: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("controller filter: len = %d, want 2", len(results))
	}

	// Filter by min duration
	results, err = s.SearchRequestSummaries(ctx, RequestSummarySearchParams{
		Start: &start, End: &end, MinDurationMs: 1000,
	})
	if err != nil {
		t.Fatalf("SearchRequestSummaries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("min_duration filter: len = %d, want 1", len(results))
	}

	// Filter by min SQL count
	results, err = s.SearchRequestSummaries(ctx, RequestSummarySearchParams{
		Start: &start, End: &end, MinSQLCount: 20,
	})
	if err != nil {
		t.Fatalf("SearchRequestSummaries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("min_sql_count filter: len = %d, want 1", len(results))
	}

	// Sort by sql_count
	results, err = s.SearchRequestSummaries(ctx, RequestSummarySearchParams{
		Start: &start, End: &end, SortBy: "sql_count",
	})
	if err != nil {
		t.Fatalf("SearchRequestSummaries: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("sort_by: len = %d, want 3", len(results))
	}
	if results[0].SQLCount < results[1].SQLCount {
		t.Errorf("expected descending sort by sql_count, got %d then %d", results[0].SQLCount, results[1].SQLCount)
	}
}

func TestBatchInsert_MetadataJSONSkipsMarshal(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	// When MetadataJSON is set, it should be used directly instead of marshaling Metadata
	entries := []LogEntry{
		{
			Timestamp:    time.Now(),
			Level:        "INFO",
			Service:      "api",
			Message:      "pre-marshaled",
			Metadata:     map[string]any{"key": "should-be-ignored"},
			MetadataJSON: `{"key":"pre-marshaled-value"}`,
		},
	}

	count, err := s.BatchInsert(ctx, entries)
	if err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	results, err := s.Search(ctx, LogSearchParams{Limit: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	// The pre-marshaled value should be stored
	if v, ok := results[0].Metadata["key"].(string); !ok || v != "pre-marshaled-value" {
		t.Errorf("metadata key = %v, want pre-marshaled-value", results[0].Metadata["key"])
	}
}

func TestBatchInsert_MetadataFallbackMarshal(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	// When MetadataJSON is empty, Metadata map should be marshaled as before
	entries := []LogEntry{
		{
			Timestamp: time.Now(),
			Level:     "INFO",
			Service:   "api",
			Message:   "normal marshal",
			Metadata:  map[string]any{"key": "normal-value"},
		},
	}

	count, err := s.BatchInsert(ctx, entries)
	if err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	results, err := s.Search(ctx, LogSearchParams{Limit: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if v, ok := results[0].Metadata["key"].(string); !ok || v != "normal-value" {
		t.Errorf("metadata key = %v, want normal-value", results[0].Metadata["key"])
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

func TestLogStore_PruneBatches_SubSecondPrecision(t *testing.T) {
	db := setupTestDB(t)
	s := NewLogStore(db)
	ctx := context.Background()

	// Record two batches
	if err := s.RecordBatch(ctx, "batch-old", 5); err != nil {
		t.Fatalf("RecordBatch: %v", err)
	}
	if err := s.RecordBatch(ctx, "batch-new", 3); err != nil {
		t.Fatalf("RecordBatch: %v", err)
	}

	// Backdate the old batch to 10 days ago
	tenDaysAgo := time.Now().UTC().Add(-10 * 24 * time.Hour).Format(time.RFC3339Nano)
	_, err := db.ExecContext(ctx, `UPDATE ingest_batches SET received_at = ? WHERE batch_id = ?`, tenDaysAgo, "batch-old")
	if err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// Prune batches older than 7 days
	pruned, err := s.PruneBatches(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneBatches: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want 1", pruned)
	}

	// Verify the new batch still exists
	rec, err := s.GetBatch(ctx, "batch-new")
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if rec == nil {
		t.Error("batch-new should still exist after pruning")
	}

	// Verify the old batch is gone
	rec, err = s.GetBatch(ctx, "batch-old")
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if rec != nil {
		t.Error("batch-old should have been pruned")
	}
}
