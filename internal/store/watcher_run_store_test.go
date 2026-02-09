package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/opentrace/opentrace/internal/store"
	"github.com/opentrace/opentrace/internal/testutil"
)

func TestPgWatcherRunStore_Lifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ws := store.NewPgWatcherStore(pool)
	rs := store.NewPgWatcherRunStore(pool)
	ctx := context.Background()

	// Create a watcher first
	w, err := ws.Create(ctx, store.CreateWatcherParams{
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
	if err != store.ErrNotFound {
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

func TestPgWatcherRunStore_Fail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ws := store.NewPgWatcherStore(pool)
	rs := store.NewPgWatcherRunStore(pool)
	ctx := context.Background()

	w, err := ws.Create(ctx, store.CreateWatcherParams{
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

func TestPgWatcherRunStore_CompleteNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	rs := store.NewPgWatcherRunStore(pool)
	ctx := context.Background()

	err := rs.Complete(ctx, uuid.New(), "summary", nil, false)
	if err != store.ErrNotFound {
		t.Errorf("Complete unknown = %v, want ErrNotFound", err)
	}
}
