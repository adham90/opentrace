package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestErrorImpactStore_TrackImpact(t *testing.T) {
	t.Skip("depends on logs/request_summaries tables — removed in segmented store migration")
	db := setupTestDB(t)
	eis := NewErrorImpactStore(db)
	ctx := context.Background()

	// Track first occurrence
	err := eis.TrackImpact(ctx, "fp-001", "production", "user-1", map[string]any{"browser": "Chrome"}, 100, "api")
	if err != nil {
		t.Fatalf("TrackImpact: %v", err)
	}

	// Track second occurrence for same user → should increment
	err = eis.TrackImpact(ctx, "fp-001", "production", "user-1", map[string]any{"browser": "Chrome"}, 101, "api")
	if err != nil {
		t.Fatalf("TrackImpact second: %v", err)
	}

	// Track different user
	err = eis.TrackImpact(ctx, "fp-001", "production", "user-2", map[string]any{"browser": "Safari"}, 102, "api")
	if err != nil {
		t.Fatalf("TrackImpact user-2: %v", err)
	}

	impact, err := eis.GetImpact(ctx, "fp-001", "")
	if err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if impact.UniqueUsers != 2 {
		t.Errorf("unique_users = %d, want 2", impact.UniqueUsers)
	}
	if impact.TotalOccurrences != 3 {
		t.Errorf("total_occurrences = %d, want 3", impact.TotalOccurrences)
	}
}

func TestErrorImpactStore_GetAffectedUsers(t *testing.T) {
	t.Skip("depends on logs/request_summaries tables — removed in segmented store migration")
	db := setupTestDB(t)
	eis := NewErrorImpactStore(db)
	ctx := context.Background()

	// User-1: 3 occurrences
	eis.TrackImpact(ctx, "fp-002", "production", "user-1", map[string]any{"browser": "Chrome"}, 1, "api")
	eis.TrackImpact(ctx, "fp-002", "production", "user-1", map[string]any{"browser": "Chrome"}, 2, "api")
	eis.TrackImpact(ctx, "fp-002", "production", "user-1", map[string]any{"browser": "Chrome"}, 3, "api")

	// User-2: 1 occurrence
	eis.TrackImpact(ctx, "fp-002", "production", "user-2", map[string]any{"browser": "Safari"}, 4, "api")

	users, err := eis.GetAffectedUsers(ctx, "fp-002", 10)
	if err != nil {
		t.Fatalf("GetAffectedUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
	// user-1 should be first (most occurrences)
	if users[0].UserID != "user-1" {
		t.Errorf("first user = %s, want user-1", users[0].UserID)
	}
	if users[0].OccurrenceCount != 3 {
		t.Errorf("user-1 occurrences = %d, want 3", users[0].OccurrenceCount)
	}
}

func TestErrorImpactStore_GetUserErrors(t *testing.T) {
	t.Skip("depends on logs/request_summaries tables — removed in segmented store migration")
	db := setupTestDB(t)
	ls := NewLogStore(db)
	egs := NewErrorGroupStore(db)
	eis := NewErrorImpactStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Create error group via log
	entry := store.LogEntry{
		Timestamp:        now.Add(-10 * time.Minute),
		Level:            "ERROR",
		Service:          "api",
		Message:          "not found",
		ExceptionClass:   "RecordNotFound",
		ErrorFingerprint: "fp-uf-1",
		UserID:           "user-10",
	}
	ls.BatchInsert(ctx, []store.LogEntry{entry})
	egs.Upsert(ctx, entry)

	// Track impact
	eis.TrackImpact(ctx, "fp-uf-1", "production", "user-10", nil, 1, "api")

	errors, err := eis.GetUserErrors(ctx, "user-10", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("GetUserErrors: %v", err)
	}
	if len(errors) != 1 {
		t.Fatalf("got %d errors, want 1", len(errors))
	}
	if errors[0].ExceptionClass != "RecordNotFound" {
		t.Errorf("exception_class = %s, want RecordNotFound", errors[0].ExceptionClass)
	}
}

func TestErrorImpactStore_ComputeImpactScores(t *testing.T) {
	t.Skip("depends on logs/request_summaries tables — removed in segmented store migration")
	db := setupTestDB(t)
	ls := NewLogStore(db)
	egs := NewErrorGroupStore(db)
	eis := NewErrorImpactStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Create error group
	entry := store.LogEntry{
		Timestamp:        now.Add(-5 * time.Minute),
		Level:            "ERROR",
		Service:          "api",
		Message:          "timeout",
		ExceptionClass:   "Timeout::Error",
		ErrorFingerprint: "fp-score-1",
	}
	ls.BatchInsert(ctx, []store.LogEntry{entry})
	egs.Upsert(ctx, entry)

	// Track 5 users with multiple occurrences each
	for i := 1; i <= 5; i++ {
		for j := 0; j < i; j++ {
			eis.TrackImpact(ctx, "fp-score-1", "production", fmt.Sprintf("user-%d", i),
				map[string]any{"browser": "Chrome"}, int64(i*10+j), "api")
		}
	}

	if err := eis.ComputeImpactScores(ctx); err != nil {
		t.Fatalf("ComputeImpactScores: %v", err)
	}

	// Check that error group was updated
	eg, err := egs.Get(ctx, "fp-score-1", "")
	if err != nil {
		t.Fatalf("Get error group: %v", err)
	}
	if eg.UniqueUsers != 5 {
		t.Errorf("unique_users = %d, want 5", eg.UniqueUsers)
	}
	if eg.ImpactScore <= 0 {
		t.Errorf("impact_score = %f, want > 0", eg.ImpactScore)
	}
}

func TestErrorImpactStore_TopByImpact(t *testing.T) {
	t.Skip("depends on logs/request_summaries tables — removed in segmented store migration")
	db := setupTestDB(t)
	ls := NewLogStore(db)
	egs := NewErrorGroupStore(db)
	eis := NewErrorImpactStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Error A: high impact (3 users)
	entryA := store.LogEntry{
		Timestamp:        now.Add(-5 * time.Minute),
		Level:            "ERROR",
		Service:          "api",
		Message:          "error A",
		ExceptionClass:   "ErrorA",
		ErrorFingerprint: "fp-top-a",
	}
	ls.BatchInsert(ctx, []store.LogEntry{entryA})
	egs.Upsert(ctx, entryA)
	eis.TrackImpact(ctx, "fp-top-a", "production", "u1", nil, 1, "api")
	eis.TrackImpact(ctx, "fp-top-a", "production", "u2", nil, 2, "api")
	eis.TrackImpact(ctx, "fp-top-a", "production", "u3", nil, 3, "api")

	// Error B: low impact (1 user)
	entryB := store.LogEntry{
		Timestamp:        now.Add(-5 * time.Minute),
		Level:            "ERROR",
		Service:          "api",
		Message:          "error B",
		ExceptionClass:   "ErrorB",
		ErrorFingerprint: "fp-top-b",
	}
	ls.BatchInsert(ctx, []store.LogEntry{entryB})
	egs.Upsert(ctx, entryB)
	eis.TrackImpact(ctx, "fp-top-b", "production", "u1", nil, 4, "api")

	// Compute scores
	eis.ComputeImpactScores(ctx)

	// Query top by impact
	results, err := eis.TopByImpact(ctx, store.ImpactQueryParams{
		Since: now.Add(-time.Hour),
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("TopByImpact: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("got %d results, want >= 2", len(results))
	}

	// Error A should rank higher (more users)
	if results[0].Fingerprint != "fp-top-a" {
		t.Errorf("top error = %s, want fp-top-a", results[0].Fingerprint)
	}
	if results[0].UniqueUsers != 3 {
		t.Errorf("unique_users = %d, want 3", results[0].UniqueUsers)
	}
}

func TestErrorImpactStore_FindCommonTraits(t *testing.T) {
	t.Skip("depends on logs/request_summaries tables — removed in segmented store migration")
	db := setupTestDB(t)
	eis := NewErrorImpactStore(db)
	ctx := context.Background()

	// 4 users: 3 on Safari, 1 on Chrome
	eis.TrackImpact(ctx, "fp-traits", "production", "u1", map[string]any{"browser": "Safari", "os": "iOS"}, 1, "api")
	eis.TrackImpact(ctx, "fp-traits", "production", "u2", map[string]any{"browser": "Safari", "os": "iOS"}, 2, "api")
	eis.TrackImpact(ctx, "fp-traits", "production", "u3", map[string]any{"browser": "Safari", "os": "macOS"}, 3, "api")
	eis.TrackImpact(ctx, "fp-traits", "production", "u4", map[string]any{"browser": "Chrome", "os": "Windows"}, 4, "api")

	traits, err := eis.FindCommonTraits(ctx, "fp-traits")
	if err != nil {
		t.Fatalf("FindCommonTraits: %v", err)
	}
	if traits == nil {
		t.Fatal("expected non-nil traits")
	}

	// browser should show Safari dominant
	browserTraits, ok := traits["browser"].(map[string]float64)
	if !ok {
		t.Fatal("expected browser trait distribution")
	}
	if browserTraits["Safari"] < 0.5 {
		t.Errorf("Safari share = %v, want >= 0.5", browserTraits["Safari"])
	}
}
