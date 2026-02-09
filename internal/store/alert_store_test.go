package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/opentrace/opentrace/internal/store"
	"github.com/opentrace/opentrace/internal/testutil"
)

func TestPgAlertStore_CRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	as := store.NewPgAlertStore(pool)
	ctx := context.Background()

	// Create alert
	a, err := as.Create(ctx, store.CreateAlertParams{
		Title:    "Payment errors spike",
		Summary:  "Found 47 payment errors in the last 15 minutes",
		Severity: store.SeverityCritical,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.ID == uuid.Nil {
		t.Fatal("expected non-nil ID")
	}
	if a.Title != "Payment errors spike" {
		t.Errorf("title = %q, want %q", a.Title, "Payment errors spike")
	}
	if a.Read {
		t.Error("expected read = false")
	}
	if a.Dismissed {
		t.Error("expected dismissed = false")
	}

	// Create a second alert
	a2, err := as.Create(ctx, store.CreateAlertParams{
		Title:    "Memory leak",
		Summary:  "RSS growing 12MB/min",
		Severity: store.SeverityWarning,
	})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}

	// CountUnread
	count, err := as.CountUnread(ctx)
	if err != nil {
		t.Fatalf("CountUnread: %v", err)
	}
	if count != 2 {
		t.Errorf("unread count = %d, want 2", count)
	}

	// List all
	all, err := as.List(ctx, store.ListAlertParams{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("List len = %d, want 2", len(all))
	}

	// Mark first as read
	err = as.MarkRead(ctx, a.ID)
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	count, _ = as.CountUnread(ctx)
	if count != 1 {
		t.Errorf("unread after MarkRead = %d, want 1", count)
	}

	// List unread only
	unread, err := as.List(ctx, store.ListAlertParams{UnreadOnly: true, Limit: 10})
	if err != nil {
		t.Fatalf("List unread: %v", err)
	}
	if len(unread) != 1 {
		t.Errorf("unread list len = %d, want 1", len(unread))
	}
	if unread[0].ID != a2.ID {
		t.Errorf("unread alert ID = %v, want %v", unread[0].ID, a2.ID)
	}

	// Dismiss
	err = as.Dismiss(ctx, a2.ID)
	if err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	count, _ = as.CountUnread(ctx)
	if count != 0 {
		t.Errorf("unread after Dismiss = %d, want 0", count)
	}

	// MarkRead not found
	err = as.MarkRead(ctx, uuid.New())
	if err != store.ErrNotFound {
		t.Errorf("MarkRead unknown = %v, want ErrNotFound", err)
	}

	// Dismiss not found
	err = as.Dismiss(ctx, uuid.New())
	if err != store.ErrNotFound {
		t.Errorf("Dismiss unknown = %v, want ErrNotFound", err)
	}
}

func TestPgAlertStore_WithWatcher(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ws := store.NewPgWatcherStore(pool)
	rs := store.NewPgWatcherRunStore(pool)
	as := store.NewPgAlertStore(pool)
	ctx := context.Background()

	// Create watcher + run + alert
	w, _ := ws.Create(ctx, store.CreateWatcherParams{
		Title:       "Error Watcher",
		Description: "Watch for errors",
	})
	run, _ := rs.Create(ctx, w.ID)

	a, err := as.Create(ctx, store.CreateAlertParams{
		WatcherID: &w.ID,
		RunID:     &run.ID,
		Title:     "Errors found",
		Summary:   "Found 10 errors",
		Severity:  store.SeverityWarning,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.WatcherID == nil || *a.WatcherID != w.ID {
		t.Errorf("watcher_id = %v, want %v", a.WatcherID, w.ID)
	}
	if a.RunID == nil || *a.RunID != run.ID {
		t.Errorf("run_id = %v, want %v", a.RunID, run.ID)
	}

	// Filter by watcher
	filtered, err := as.List(ctx, store.ListAlertParams{WatcherID: &w.ID, Limit: 10})
	if err != nil {
		t.Fatalf("List by watcher: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("filtered len = %d, want 1", len(filtered))
	}

	// Filter by different watcher
	other := uuid.New()
	empty, err := as.List(ctx, store.ListAlertParams{WatcherID: &other, Limit: 10})
	if err != nil {
		t.Fatalf("List by other: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("other watcher len = %d, want 0", len(empty))
	}
}

func TestPgAlertStore_DefaultSeverity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	as := store.NewPgAlertStore(pool)
	ctx := context.Background()

	a, err := as.Create(ctx, store.CreateAlertParams{
		Title:   "No severity",
		Summary: "Test default",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.Severity != store.SeverityWarning {
		t.Errorf("default severity = %q, want %q", a.Severity, store.SeverityWarning)
	}
}
