package store

import (
	"context"
	"testing"
	"time"
)

func TestAgentNoteStore_UpsertAndGet(t *testing.T) {
	db := setupTestDB(t)
	s := NewAgentNoteStore(db)
	ctx := context.Background()

	note, err := s.Upsert(ctx, "service", "myapp", "This service handles user authentication")
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if note.EntityType != "service" || note.EntityID != "myapp" {
		t.Errorf("entity = %s/%s, want service/myapp", note.EntityType, note.EntityID)
	}
	if note.Note != "This service handles user authentication" {
		t.Errorf("Note = %q", note.Note)
	}

	got, err := s.Get(ctx, "service", "myapp")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Note != "This service handles user authentication" {
		t.Errorf("Get Note = %q", got.Note)
	}
}

func TestAgentNoteStore_UpsertUpdates(t *testing.T) {
	db := setupTestDB(t)
	s := NewAgentNoteStore(db)
	ctx := context.Background()

	s.Upsert(ctx, "error", "fp123", "Known issue — Redis timeout under load")
	note, err := s.Upsert(ctx, "error", "fp123", "Fixed in v2.1.0 — Redis connection pool increased")
	if err != nil {
		t.Fatalf("Upsert update: %v", err)
	}
	if note.Note != "Fixed in v2.1.0 — Redis connection pool increased" {
		t.Errorf("Note not updated: %q", note.Note)
	}
}

func TestAgentNoteStore_List(t *testing.T) {
	db := setupTestDB(t)
	s := NewAgentNoteStore(db)
	ctx := context.Background()

	s.Upsert(ctx, "service", "app1", "Main app")
	s.Upsert(ctx, "service", "app2", "Worker")
	s.Upsert(ctx, "query", "q1", "Known slow query")

	// List all
	all, err := s.List(ctx, "")
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("List all = %d, want 3", len(all))
	}

	// List by type
	services, err := s.List(ctx, "service")
	if err != nil {
		t.Fatalf("List service: %v", err)
	}
	if len(services) != 2 {
		t.Errorf("List service = %d, want 2", len(services))
	}
}

func TestAgentNoteStore_Delete(t *testing.T) {
	db := setupTestDB(t)
	s := NewAgentNoteStore(db)
	ctx := context.Background()

	s.Upsert(ctx, "endpoint", "/api/users", "Slow under load")
	if err := s.Delete(ctx, "endpoint", "/api/users"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := s.Get(ctx, "endpoint", "/api/users")
	if err != ErrNotFound {
		t.Errorf("Get after delete: err = %v, want ErrNotFound", err)
	}

	if err := s.Delete(ctx, "endpoint", "/api/users"); err != ErrNotFound {
		t.Errorf("Delete nonexistent: err = %v, want ErrNotFound", err)
	}
}

func TestAgentNoteStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	s := NewAgentNoteStore(db)
	ctx := context.Background()

	// Create a note, then manually backdate it
	s.Upsert(ctx, "service", "old", "Old note")
	old := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	db.ExecContext(ctx, `UPDATE agent_notes SET updated_at = ? WHERE entity_id = 'old'`, old)

	s.Upsert(ctx, "service", "recent", "Recent note")

	n, err := s.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned = %d, want 1", n)
	}

	notes, _ := s.List(ctx, "")
	if len(notes) != 1 {
		t.Errorf("remaining = %d, want 1", len(notes))
	}
}
