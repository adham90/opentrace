package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestWatcherRunStore_Lifecycle(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatcherStore(db)
	rs := NewWatcherRunStore(db)
	ctx := context.Background()

	// Create a watcher first
	w, err := ws.Create(ctx, CreateWatcherParams{
		Title:       "Test Watcher",
		Description: "For run tests",
	})
	if err != nil {
		t.Fatalf("Create watcher: %v", err)
	}

	// Create a run
	run, err := rs.Create(ctx, w.ID)
	if err != nil {
		t.Fatalf("Create run: %v", err)
	}
	if run.Status != "running" {
		t.Errorf("run status = %q, want %q", run.Status, "running")
	}
	if run.WatcherID != w.ID {
		t.Errorf("run watcher_id = %v, want %v", run.WatcherID, w.ID)
	}

	// Complete the run
	err = rs.Complete(ctx, run.ID, "Found 3 errors", map[string]any{"count": 3}, true)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Get the completed run
	got, err := rs.GetByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("completed status = %q, want %q", got.Status, "completed")
	}
	if got.Summary == nil || *got.Summary != "Found 3 errors" {
		t.Errorf("summary = %v, want %q", got.Summary, "Found 3 errors")
	}
	if !got.HasAlert {
		t.Error("expected has_alert = true")
	}
	if got.FinishedAt == nil {
		t.Error("expected finished_at to be set")
	}

	// GetByID not found
	_, err = rs.GetByID(ctx, uuid.New())
	if err != ErrNotFound {
		t.Errorf("GetByID unknown = %v, want ErrNotFound", err)
	}

	// List runs
	runs, err := rs.List(ctx, w.ID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("List len = %d, want 1", len(runs))
	}
}

func TestWatcherRunStore_Fail(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatcherStore(db)
	rs := NewWatcherRunStore(db)
	ctx := context.Background()

	w, err := ws.Create(ctx, CreateWatcherParams{
		Title:       "Fail Test",
		Description: "For fail tests",
	})
	if err != nil {
		t.Fatalf("Create watcher: %v", err)
	}

	run, err := rs.Create(ctx, w.ID)
	if err != nil {
		t.Fatalf("Create run: %v", err)
	}

	err = rs.Fail(ctx, run.ID, "connection timeout")
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}

	got, err := rs.GetByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != "error" {
		t.Errorf("failed status = %q, want %q", got.Status, "error")
	}
	if got.Error == nil || *got.Error != "connection timeout" {
		t.Errorf("error = %v, want %q", got.Error, "connection timeout")
	}
}

func TestWatcherRunStore_CompleteNotFound(t *testing.T) {
	db := setupTestDB(t)
	rs := NewWatcherRunStore(db)
	ctx := context.Background()

	err := rs.Complete(ctx, uuid.New(), "summary", nil, false)
	if err != ErrNotFound {
		t.Errorf("Complete unknown = %v, want ErrNotFound", err)
	}
}
