package store

import (
	"context"
	"testing"
	"time"

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

func TestWatcherRunStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatcherStore(db)
	rs := NewWatcherRunStore(db)
	ctx := context.Background()

	w, err := ws.Create(ctx, CreateWatcherParams{
		Title:       "Prune Test",
		Description: "For prune tests",
	})
	if err != nil {
		t.Fatalf("Create watcher: %v", err)
	}

	// Insert a run with backdated created_at
	_, err = db.ExecContext(ctx,
		`INSERT INTO watcher_runs (id, watcher_id, started_at, status, created_at)
		 VALUES (?, ?, ?, 'completed', ?)`,
		"old-run-1", w.ID.String(),
		time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339),
		time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("inserting old run: %v", err)
	}

	// Create a recent run
	_, err = rs.Create(ctx, w.ID)
	if err != nil {
		t.Fatalf("Create run: %v", err)
	}

	// Prune runs older than 24 hours
	pruned, err := rs.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want 1", pruned)
	}

	// Verify only recent remains
	runs, err := rs.List(ctx, w.ID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("remaining = %d, want 1", len(runs))
	}
}

func TestWatcherRunStore_CountRuns(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatcherStore(db)
	rs := NewWatcherRunStore(db)
	ctx := context.Background()

	w, err := ws.Create(ctx, CreateWatcherParams{
		Title:       "Count Test",
		Description: "For count tests",
	})
	if err != nil {
		t.Fatalf("Create watcher: %v", err)
	}

	now := time.Now().UTC()
	periodStart := now.Add(-24 * time.Hour)

	// Insert runs with backdated started_at within period
	for _, r := range []struct {
		status string
		age    time.Duration
	}{
		{"completed", 2 * time.Hour},
		{"completed", 4 * time.Hour},
		{"error", 6 * time.Hour},
	} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO watcher_runs (id, watcher_id, started_at, status, created_at) VALUES (?, ?, ?, ?, ?)`,
			uuid.New().String(), w.ID.String(),
			now.Add(-r.age).Format(time.RFC3339), r.status,
			now.Add(-r.age).Format(time.RFC3339),
		)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Insert run outside period (48h ago)
	_, err = db.ExecContext(ctx,
		`INSERT INTO watcher_runs (id, watcher_id, started_at, status, created_at) VALUES (?, ?, ?, ?, ?)`,
		uuid.New().String(), w.ID.String(),
		now.Add(-48*time.Hour).Format(time.RFC3339), "completed",
		now.Add(-48*time.Hour).Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert old: %v", err)
	}

	// Count all in period
	total, err := rs.CountRuns(ctx, CountRunParams{Since: periodStart, Until: now})
	if err != nil {
		t.Fatalf("CountRuns total: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}

	// Count errors only
	errors, err := rs.CountRuns(ctx, CountRunParams{Since: periodStart, Until: now, Status: "error"})
	if err != nil {
		t.Fatalf("CountRuns errors: %v", err)
	}
	if errors != 1 {
		t.Errorf("errors = %d, want 1", errors)
	}

	// Count by watcher
	byWatcher, err := rs.CountRuns(ctx, CountRunParams{Since: periodStart, Until: now, WatcherID: &w.ID})
	if err != nil {
		t.Fatalf("CountRuns by watcher: %v", err)
	}
	if byWatcher != 3 {
		t.Errorf("by watcher = %d, want 3", byWatcher)
	}

	// Count with unknown watcher
	unknown := uuid.New()
	byUnknown, err := rs.CountRuns(ctx, CountRunParams{Since: periodStart, Until: now, WatcherID: &unknown})
	if err != nil {
		t.Fatalf("CountRuns unknown: %v", err)
	}
	if byUnknown != 0 {
		t.Errorf("unknown = %d, want 0", byUnknown)
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
