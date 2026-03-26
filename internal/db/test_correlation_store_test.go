package db

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestTestCorrelationStore_RefreshAndQuery(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Seed an error group to be picked up by RefreshUncoveredPaths.
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx,
		`INSERT INTO error_groups (fingerprint, service, exception_class, source_file, status, occurrence_count, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, 'unresolved', 5, ?, ?)`,
		"fp-abc", "api", "RuntimeError", "payments.go", now, now)
	if err != nil {
		t.Fatalf("seeding error_groups: %v", err)
	}

	tcs := NewTestCorrelationStore(db)

	if err := tcs.RefreshUncoveredPaths(ctx); err != nil {
		t.Fatalf("RefreshUncoveredPaths: %v", err)
	}

	paths, err := tcs.TopByPriority(ctx, "api", 10)
	if err != nil {
		t.Fatalf("TopByPriority: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("got %d paths, want 1", len(paths))
	}
	if paths[0].ErrorFingerprint != "fp-abc" {
		t.Errorf("fingerprint = %q, want %q", paths[0].ErrorFingerprint, "fp-abc")
	}
	if paths[0].ErrorCount != 5 {
		t.Errorf("error_count = %d, want 5", paths[0].ErrorCount)
	}
}

func TestTestCorrelationStore_GetByFingerprint(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	tcs := NewTestCorrelationStore(db)

	// Insert directly
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx,
		`INSERT INTO uncovered_error_paths (error_fingerprint, service, error_class, error_count, priority_score, last_seen_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"fp-xyz", "web", "TypeError", 10, 50.0, now, now, now)
	if err != nil {
		t.Fatalf("inserting: %v", err)
	}

	p, err := tcs.GetByFingerprint(ctx, "fp-xyz")
	if err != nil {
		t.Fatalf("GetByFingerprint: %v", err)
	}
	if p.ErrorClass != "TypeError" {
		t.Errorf("error_class = %q, want %q", p.ErrorClass, "TypeError")
	}

	_, err = tcs.GetByFingerprint(ctx, "nonexistent")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestTestCorrelationStore_TopByPriorityAll(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	tcs := NewTestCorrelationStore(db)

	now := time.Now().UTC().Format(time.RFC3339)
	for _, fp := range []string{"fp-1", "fp-2"} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO uncovered_error_paths (error_fingerprint, service, error_class, error_count, priority_score, last_seen_at, created_at, updated_at)
			 VALUES (?, 'api', 'Error', 3, 10.0, ?, ?, ?)`,
			fp, now, now, now)
		if err != nil {
			t.Fatalf("inserting: %v", err)
		}
	}

	// Query all services
	paths, err := tcs.TopByPriority(ctx, "", 10)
	if err != nil {
		t.Fatalf("TopByPriority: %v", err)
	}
	if len(paths) != 2 {
		t.Errorf("got %d, want 2", len(paths))
	}
}

func TestTestCorrelationStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	tcs := NewTestCorrelationStore(db)

	oldTime := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx,
		`INSERT INTO uncovered_error_paths (error_fingerprint, service, error_class, error_count, priority_score, last_seen_at, created_at, updated_at)
		 VALUES ('fp-old', 'api', 'Error', 1, 1.0, ?, ?, ?)`,
		oldTime, oldTime, oldTime)
	if err != nil {
		t.Fatalf("inserting: %v", err)
	}

	n, err := tcs.Prune(ctx, 1*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
}
