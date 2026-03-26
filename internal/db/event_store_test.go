package db

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestEventStore_CreateAndGetByID(t *testing.T) {
	db := setupTestDB(t)
	s := NewEventStore(db)
	ctx := context.Background()

	e, err := s.Create(ctx, store.CreateEventParams{
		EventType:   store.EventTypePR,
		Source:      "github",
		Service:     "api",
		Title:       "Fix payment bug",
		Description: "Resolves issue #42",
		Metadata:    map[string]any{"pr_number": float64(123)},
		ExternalID:  "pr-123",
		ExternalURL: "https://github.com/org/repo/pull/123",
		Author:      "alice@example.com",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if e.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	if e.EventType != store.EventTypePR {
		t.Errorf("EventType = %q, want %q", e.EventType, store.EventTypePR)
	}
	if e.Title != "Fix payment bug" {
		t.Errorf("Title = %q, want %q", e.Title, "Fix payment bug")
	}

	got, err := s.GetByID(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Service != "api" {
		t.Errorf("Service = %q, want %q", got.Service, "api")
	}
}

func TestEventStore_List(t *testing.T) {
	db := setupTestDB(t)
	s := NewEventStore(db)
	ctx := context.Background()

	// Create events of different types
	s.Create(ctx, store.CreateEventParams{EventType: store.EventTypePR, Service: "api", Title: "PR 1"})
	s.Create(ctx, store.CreateEventParams{EventType: store.EventTypeTest, Service: "api", Title: "Test 1"})
	s.Create(ctx, store.CreateEventParams{EventType: store.EventTypePR, Service: "web", Title: "PR 2"})

	// List all
	all, err := s.List(ctx, store.ListEventParams{})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("got %d events, want 3", len(all))
	}

	// Filter by type
	prs, err := s.List(ctx, store.ListEventParams{EventType: store.EventTypePR})
	if err != nil {
		t.Fatalf("List PRs: %v", err)
	}
	if len(prs) != 2 {
		t.Errorf("got %d PR events, want 2", len(prs))
	}

	// Filter by service
	apiEvents, err := s.List(ctx, store.ListEventParams{Service: "api"})
	if err != nil {
		t.Fatalf("List api: %v", err)
	}
	if len(apiEvents) != 2 {
		t.Errorf("got %d api events, want 2", len(apiEvents))
	}
}

func TestEventStore_GetByExternalID(t *testing.T) {
	db := setupTestDB(t)
	s := NewEventStore(db)
	ctx := context.Background()

	s.Create(ctx, store.CreateEventParams{EventType: store.EventTypePR, ExternalID: "pr-42", Title: "PR 42"})

	got, err := s.GetByExternalID(ctx, store.EventTypePR, "pr-42")
	if err != nil {
		t.Fatalf("GetByExternalID: %v", err)
	}
	if got.Title != "PR 42" {
		t.Errorf("Title = %q, want %q", got.Title, "PR 42")
	}

	_, err = s.GetByExternalID(ctx, store.EventTypePR, "nonexistent")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestEventStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	s := NewEventStore(db)
	ctx := context.Background()

	s.Create(ctx, store.CreateEventParams{EventType: store.EventTypeCommit, Title: "Commit 1"})

	// Back-date the record so it's clearly older than now.
	oldTime := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	db.ExecContext(ctx, `UPDATE events SET created_at = ?`, oldTime)

	// Prune with 1-hour window should delete the back-dated event.
	n, err := s.Prune(ctx, 1*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
}

func TestEventStore_ListWithSince(t *testing.T) {
	db := setupTestDB(t)
	s := NewEventStore(db)
	ctx := context.Background()

	s.Create(ctx, store.CreateEventParams{EventType: store.EventTypeAlert, Service: "api", Title: "Alert 1"})

	// Since = future, should find nothing
	future := time.Now().Add(1 * time.Hour)
	events, err := s.List(ctx, store.ListEventParams{Since: future})
	if err != nil {
		t.Fatalf("List with since: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}
