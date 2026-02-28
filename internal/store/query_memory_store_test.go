package store

import (
	"context"
	"testing"
	"time"
)

func TestQueryMemoryStore_UpsertAndGet(t *testing.T) {
	db := setupTestDB(t)
	s := NewQueryMemoryStore(db)
	ctx := context.Background()

	// Get non-existent should return ErrNotFound
	_, err := s.Get(ctx, "fp-unknown")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Upsert first time
	err = s.Upsert(ctx, UpsertQueryMemoryParams{
		Fingerprint: "SELECT * FROM users WHERE id = ?",
		SessionID:   "sess-1",
		RootCause:   "Missing index on users.id",
		Fix:         "Added index",
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Get should return the entry
	qm, err := s.Get(ctx, "SELECT * FROM users WHERE id = ?")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if qm.Fingerprint != "SELECT * FROM users WHERE id = ?" {
		t.Errorf("fingerprint = %q", qm.Fingerprint)
	}
	if qm.InvestigationCount != 1 {
		t.Errorf("investigation_count = %d, want 1", qm.InvestigationCount)
	}
	if qm.LastRootCause != "Missing index on users.id" {
		t.Errorf("root_cause = %q", qm.LastRootCause)
	}
	if qm.LastFix != "Added index" {
		t.Errorf("fix = %q", qm.LastFix)
	}
	if qm.LastInvestigationSessionID != "sess-1" {
		t.Errorf("session_id = %q", qm.LastInvestigationSessionID)
	}
}

func TestQueryMemoryStore_UpsertIncrementsCount(t *testing.T) {
	db := setupTestDB(t)
	s := NewQueryMemoryStore(db)
	ctx := context.Background()

	fp := "SELECT COUNT(*) FROM orders"

	// First upsert
	err := s.Upsert(ctx, UpsertQueryMemoryParams{
		Fingerprint: fp,
		SessionID:   "sess-1",
		RootCause:   "First root cause",
		Fix:         "First fix",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Second upsert — should increment count and update fields
	err = s.Upsert(ctx, UpsertQueryMemoryParams{
		Fingerprint: fp,
		SessionID:   "sess-2",
		RootCause:   "Updated root cause",
		Fix:         "Updated fix",
	})
	if err != nil {
		t.Fatal(err)
	}

	qm, err := s.Get(ctx, fp)
	if err != nil {
		t.Fatal(err)
	}
	if qm.InvestigationCount != 2 {
		t.Errorf("investigation_count = %d, want 2", qm.InvestigationCount)
	}
	if qm.LastInvestigationSessionID != "sess-2" {
		t.Errorf("session_id = %q, want sess-2", qm.LastInvestigationSessionID)
	}
	if qm.LastRootCause != "Updated root cause" {
		t.Errorf("root_cause = %q", qm.LastRootCause)
	}
}

func TestQueryMemoryStore_UpsertKeepsOldFieldsWhenEmpty(t *testing.T) {
	db := setupTestDB(t)
	s := NewQueryMemoryStore(db)
	ctx := context.Background()

	fp := "SELECT * FROM products"

	// First upsert with values
	err := s.Upsert(ctx, UpsertQueryMemoryParams{
		Fingerprint: fp,
		SessionID:   "sess-1",
		RootCause:   "Original cause",
		Fix:         "Original fix",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Second upsert with empty root_cause and fix — should keep originals
	err = s.Upsert(ctx, UpsertQueryMemoryParams{
		Fingerprint: fp,
		SessionID:   "sess-2",
		RootCause:   "",
		Fix:         "",
	})
	if err != nil {
		t.Fatal(err)
	}

	qm, err := s.Get(ctx, fp)
	if err != nil {
		t.Fatal(err)
	}
	if qm.LastRootCause != "Original cause" {
		t.Errorf("root_cause = %q, want 'Original cause'", qm.LastRootCause)
	}
	if qm.LastFix != "Original fix" {
		t.Errorf("fix = %q, want 'Original fix'", qm.LastFix)
	}
}

func TestQueryMemoryStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	s := NewQueryMemoryStore(db)
	ctx := context.Background()

	// Insert an entry
	err := s.Upsert(ctx, UpsertQueryMemoryParams{
		Fingerprint: "old-query",
		SessionID:   "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Prune with very short duration should not remove it (just inserted)
	n, err := s.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("pruned %d, expected 0 (entry is fresh)", n)
	}

	// Verify entry still exists
	_, err = s.Get(ctx, "old-query")
	if err != nil {
		t.Errorf("entry should still exist: %v", err)
	}

	// Force last_seen_at to a very old date
	_, err = db.ExecContext(ctx, `UPDATE query_memory SET last_seen_at = '2020-01-01T00:00:00Z' WHERE fingerprint = ?`, "old-query")
	if err != nil {
		t.Fatal(err)
	}

	// Prune should now remove it
	n, err = s.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("pruned %d, expected 1", n)
	}

	// Verify entry is gone
	_, err = s.Get(ctx, "old-query")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after prune, got %v", err)
	}
}
