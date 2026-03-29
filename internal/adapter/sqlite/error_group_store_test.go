package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestErrorGroupStore_Upsert_NewGroup(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	entry := store.LogEntry{
		ID:               0,
		Level:            "ERROR",
		Service:          "myapp",
		Environment:      "production",
		ExceptionClass:   "Redis::TimeoutError",
		Message:          "Connection timed out",
		ErrorFingerprint: "fp_abc123",
		SourceFile:       "app/models/user.rb",
		SourceLine:       42,
		Timestamp:        time.Now(),
	}

	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	eg, err := s.Get(ctx, "fp_abc123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if eg.Service != "myapp" {
		t.Errorf("Service = %q, want myapp", eg.Service)
	}
	if eg.ExceptionClass != "Redis::TimeoutError" {
		t.Errorf("ExceptionClass = %q, want Redis::TimeoutError", eg.ExceptionClass)
	}
	if eg.OccurrenceCount != 1 {
		t.Errorf("OccurrenceCount = %d, want 1", eg.OccurrenceCount)
	}
	if eg.Status != store.ErrorGroupUnresolved {
		t.Errorf("Status = %q, want unresolved", eg.Status)
	}
}

func TestErrorGroupStore_Upsert_IncrementExisting(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	entry := store.LogEntry{
		ID: 0, Level: "ERROR", Service: "myapp",
		ErrorFingerprint: "fp_inc", Timestamp: time.Now(),
	}
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	eg, err := s.Get(ctx, "fp_inc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if eg.OccurrenceCount != 2 {
		t.Errorf("OccurrenceCount = %d, want 2", eg.OccurrenceCount)
	}
}

func TestErrorGroupStore_Upsert_SkipsNonError(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	// INFO level should be skipped.
	entry := store.LogEntry{
		ID: 0, Level: "INFO", Service: "myapp",
		ErrorFingerprint: "fp_info", Timestamp: time.Now(),
	}
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	_, err := s.Get(ctx, "fp_info")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound for INFO-level entry, got: %v", err)
	}
}

func TestErrorGroupStore_Upsert_SkipsEmptyFingerprint(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	entry := store.LogEntry{ID: 0, Level: "ERROR", Service: "myapp", Timestamp: time.Now()}
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// No error group should be created.
	count, err := s.Count(ctx, "")
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestErrorGroupStore_Resolve_And_Reopen(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	entry := store.LogEntry{
		ID: 0, Level: "ERROR", Service: "myapp",
		ErrorFingerprint: "fp_resolve", Timestamp: time.Now(),
	}
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Resolve.
	if err := s.Resolve(ctx, "fp_resolve", "Fixed in PR #42"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	eg, _ := s.Get(ctx, "fp_resolve")
	if eg.Status != store.ErrorGroupResolved {
		t.Errorf("Status = %q, want resolved", eg.Status)
	}
	if eg.ResolvedAt == nil {
		t.Error("ResolvedAt should be set")
	}

	// Reopen via new occurrence.
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert reopen: %v", err)
	}
	eg, _ = s.Get(ctx, "fp_resolve")
	if eg.Status != store.ErrorGroupUnresolved {
		t.Errorf("Status = %q, want unresolved after reopen", eg.Status)
	}
	if eg.ReopenedCount != 1 {
		t.Errorf("ReopenedCount = %d, want 1", eg.ReopenedCount)
	}
	if eg.OccurrenceCount != 2 {
		t.Errorf("OccurrenceCount = %d, want 2", eg.OccurrenceCount)
	}

	// Verify events.
	events, err := s.ListEvents(ctx, "fp_resolve", 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	// Both have the same second-level timestamp; verify both actions present.
	actions := map[string]bool{}
	for _, ev := range events {
		actions[ev.Action] = true
	}
	if !actions["resolved"] {
		t.Error("expected 'resolved' event")
	}
	if !actions["reopened"] {
		t.Error("expected 'reopened' event")
	}
}

func TestErrorGroupStore_Ignore_StaysIgnored(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	entry := store.LogEntry{
		ID: 0, Level: "ERROR", Service: "myapp",
		ErrorFingerprint: "fp_ignore", Timestamp: time.Now(),
	}
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.Ignore(ctx, "fp_ignore", "Known noise"); err != nil {
		t.Fatalf("Ignore: %v", err)
	}

	// New occurrence should NOT reopen an ignored error.
	if err := s.Upsert(ctx, entry); err != nil {
		t.Fatalf("Upsert after ignore: %v", err)
	}
	eg, _ := s.Get(ctx, "fp_ignore")
	if eg.Status != store.ErrorGroupIgnored {
		t.Errorf("Status = %q, want ignored", eg.Status)
	}
	// Occurrence count still increments.
	if eg.OccurrenceCount != 2 {
		t.Errorf("OccurrenceCount = %d, want 2", eg.OccurrenceCount)
	}
}

func TestErrorGroupStore_List(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	for _, fp := range []string{"fp_a", "fp_b", "fp_c"} {
		entry := store.LogEntry{
			Level: "ERROR", Service: "myapp",
			ErrorFingerprint: fp, Timestamp: time.Now(),
		}
		if err := s.Upsert(ctx, entry); err != nil {
			t.Fatalf("Upsert %s: %v", fp, err)
		}
	}

	groups, err := s.List(ctx, store.ListErrorGroupParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(groups) != 3 {
		t.Errorf("len(groups) = %d, want 3", len(groups))
	}

	// Filter by status.
	if err := s.Resolve(ctx, "fp_b", "fixed"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	unresolved, err := s.List(ctx, store.ListErrorGroupParams{Status: store.ErrorGroupUnresolved})
	if err != nil {
		t.Fatalf("List unresolved: %v", err)
	}
	if len(unresolved) != 2 {
		t.Errorf("unresolved count = %d, want 2", len(unresolved))
	}
}

func TestErrorGroupStore_Count(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	for _, fp := range []string{"fp_1", "fp_2", "fp_3"} {
		entry := store.LogEntry{
			Level: "FATAL", Service: "myapp",
			ErrorFingerprint: fp, Timestamp: time.Now(),
		}
		if err := s.Upsert(ctx, entry); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	total, err := s.Count(ctx, "")
	if err != nil {
		t.Fatalf("Count all: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}

	unresolved, err := s.Count(ctx, store.ErrorGroupUnresolved)
	if err != nil {
		t.Fatalf("Count unresolved: %v", err)
	}
	if unresolved != 3 {
		t.Errorf("unresolved = %d, want 3", unresolved)
	}
}

func TestErrorGroupStore_Resolve_NotFound(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	err := s.Resolve(ctx, "nonexistent", "reason")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestErrorGroupStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	old := store.LogEntry{
		ID: 0, Level: "ERROR", Service: "myapp",
		ErrorFingerprint: "fp_old", Timestamp: time.Now().Add(-48 * time.Hour),
	}
	if err := s.Upsert(ctx, old); err != nil {
		t.Fatalf("Upsert old: %v", err)
	}

	recent := store.LogEntry{
		Level: "ERROR", Service: "myapp",
		ErrorFingerprint: "fp_recent", Timestamp: time.Now(),
	}
	if err := s.Upsert(ctx, recent); err != nil {
		t.Fatalf("Upsert recent: %v", err)
	}

	pruned, err := s.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want 1", pruned)
	}

	remaining, _ := s.Count(ctx, "")
	if remaining != 1 {
		t.Errorf("remaining = %d, want 1", remaining)
	}
}
