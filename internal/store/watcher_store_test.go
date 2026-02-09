package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opentrace/opentrace/internal/store"
	"github.com/opentrace/opentrace/internal/testutil"
)

func TestPgWatcherStore_CRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	s := store.NewPgWatcherStore(pool)
	ctx := context.Background()

	// Create
	params := store.CreateWatcherParams{
		Title:       "Test Watcher",
		Description: "Watch for errors in payment service",
		Severity:    store.SeverityCritical,
		Filters:     json.RawMessage(`{"service":"payment-api","level":"error"}`),
		TimeRange:   "15m",
		Notify:      json.RawMessage(`["dashboard"]`),
	}

	w, err := s.Create(ctx, params)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.ID == uuid.Nil {
		t.Fatal("expected non-nil ID")
	}
	if w.Title != params.Title {
		t.Errorf("title = %q, want %q", w.Title, params.Title)
	}
	if w.Severity != store.SeverityCritical {
		t.Errorf("severity = %q, want %q", w.Severity, store.SeverityCritical)
	}
	if w.Status != store.WatcherActive {
		t.Errorf("status = %q, want %q", w.Status, store.WatcherActive)
	}
	if w.TimeRange != "15m" {
		t.Errorf("time_range = %q, want %q", w.TimeRange, "15m")
	}
	if w.NextRunAt == nil {
		t.Fatal("expected next_run_at to be set")
	}

	// GetByID
	got, err := s.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Title != params.Title {
		t.Errorf("GetByID title = %q, want %q", got.Title, params.Title)
	}

	// GetByID not found
	_, err = s.GetByID(ctx, uuid.New())
	if err != store.ErrNotFound {
		t.Errorf("GetByID unknown = %v, want ErrNotFound", err)
	}

	// List
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List len = %d, want 1", len(list))
	}

	// Update
	newTitle := "Updated Title"
	newSeverity := store.SeverityWarning
	updated, err := s.Update(ctx, w.ID, store.UpdateWatcherParams{
		Title:    &newTitle,
		Severity: &newSeverity,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Title != newTitle {
		t.Errorf("updated title = %q, want %q", updated.Title, newTitle)
	}
	if updated.Severity != store.SeverityWarning {
		t.Errorf("updated severity = %q, want %q", updated.Severity, store.SeverityWarning)
	}

	// Update not found
	_, err = s.Update(ctx, uuid.New(), store.UpdateWatcherParams{Title: &newTitle})
	if err != store.ErrNotFound {
		t.Errorf("Update unknown = %v, want ErrNotFound", err)
	}

	// UpdateRunTime
	now := time.Now()
	next := now.Add(5 * time.Minute)
	err = s.UpdateRunTime(ctx, w.ID, now, next)
	if err != nil {
		t.Fatalf("UpdateRunTime: %v", err)
	}

	after, _ := s.GetByID(ctx, w.ID)
	if after.LastRunAt == nil {
		t.Fatal("expected last_run_at to be set")
	}

	// Delete
	err = s.Delete(ctx, w.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Delete not found
	err = s.Delete(ctx, w.ID)
	if err != store.ErrNotFound {
		t.Errorf("Delete again = %v, want ErrNotFound", err)
	}
}

func TestPgWatcherStore_Defaults(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	s := store.NewPgWatcherStore(pool)
	ctx := context.Background()

	w, err := s.Create(ctx, store.CreateWatcherParams{
		Title:       "Minimal Watcher",
		Description: "Just a test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if w.Severity != store.SeverityWarning {
		t.Errorf("default severity = %q, want %q", w.Severity, store.SeverityWarning)
	}
	if w.TimeRange != "15m" {
		t.Errorf("default time_range = %q, want %q", w.TimeRange, "15m")
	}
}

func TestPgWatcherStore_GetDueWatchers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	s := store.NewPgWatcherStore(pool)
	ctx := context.Background()

	// Create a watcher with next_run_at in the past (should be due)
	w1, err := s.Create(ctx, store.CreateWatcherParams{
		Title:       "Due Watcher",
		Description: "Should be due",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Set next_run_at to the past
	past := time.Now().Add(-1 * time.Minute)
	future := time.Now().Add(10 * time.Minute)
	err = s.UpdateRunTime(ctx, w1.ID, past, past)
	if err != nil {
		t.Fatalf("UpdateRunTime: %v", err)
	}

	// Create a watcher with next_run_at in the future (not due)
	w2, err := s.Create(ctx, store.CreateWatcherParams{
		Title:       "Future Watcher",
		Description: "Not due yet",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	err = s.UpdateRunTime(ctx, w2.ID, time.Now(), future)
	if err != nil {
		t.Fatalf("UpdateRunTime: %v", err)
	}

	due, err := s.GetDueWatchers(ctx)
	if err != nil {
		t.Fatalf("GetDueWatchers: %v", err)
	}

	if len(due) != 1 {
		t.Fatalf("GetDueWatchers len = %d, want 1", len(due))
	}
	if due[0].ID != w1.ID {
		t.Errorf("due watcher ID = %v, want %v", due[0].ID, w1.ID)
	}
}
