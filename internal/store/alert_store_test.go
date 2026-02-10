package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAlertStore_CRUD(t *testing.T) {
	db := setupTestDB(t)
	as := NewAlertStore(db)
	ctx := context.Background()

	// Create alert
	a, err := as.Create(ctx, CreateAlertParams{
		Title:    "Payment errors spike",
		Summary:  "Found 47 payment errors in the last 15 minutes",
		Severity: SeverityCritical,
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
	a2, err := as.Create(ctx, CreateAlertParams{
		Title:    "Memory leak",
		Summary:  "RSS growing 12MB/min",
		Severity: SeverityWarning,
	})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}

	// CountUnread
	count, err := as.CountUnread(ctx, "")
	if err != nil {
		t.Fatalf("CountUnread: %v", err)
	}
	if count != 2 {
		t.Errorf("unread count = %d, want 2", count)
	}

	// List all
	all, err := as.List(ctx, ListAlertParams{Limit: 10})
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

	count, _ = as.CountUnread(ctx, "")
	if count != 1 {
		t.Errorf("unread after MarkRead = %d, want 1", count)
	}

	// List unread only
	unread, err := as.List(ctx, ListAlertParams{UnreadOnly: true, Limit: 10})
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

	count, _ = as.CountUnread(ctx, "")
	if count != 0 {
		t.Errorf("unread after Dismiss = %d, want 0", count)
	}

	// MarkRead not found
	err = as.MarkRead(ctx, uuid.New())
	if err != ErrNotFound {
		t.Errorf("MarkRead unknown = %v, want ErrNotFound", err)
	}

	// Dismiss not found
	err = as.Dismiss(ctx, uuid.New())
	if err != ErrNotFound {
		t.Errorf("Dismiss unknown = %v, want ErrNotFound", err)
	}
}

func TestAlertStore_WithWatcher(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatcherStore(db)
	rs := NewWatcherRunStore(db)
	as := NewAlertStore(db)
	ctx := context.Background()

	// Create watcher + run + alert
	w, _ := ws.Create(ctx, CreateWatcherParams{
		Title:       "Error Watcher",
		Description: "Watch for errors",
	})
	run, _ := rs.Create(ctx, w.ID)

	a, err := as.Create(ctx, CreateAlertParams{
		WatcherID: &w.ID,
		RunID:     &run.ID,
		Title:     "Errors found",
		Summary:   "Found 10 errors",
		Severity:  SeverityWarning,
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
	filtered, err := as.List(ctx, ListAlertParams{WatcherID: &w.ID, Limit: 10})
	if err != nil {
		t.Fatalf("List by watcher: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("filtered len = %d, want 1", len(filtered))
	}

	// Filter by different watcher
	other := uuid.New()
	empty, err := as.List(ctx, ListAlertParams{WatcherID: &other, Limit: 10})
	if err != nil {
		t.Fatalf("List by other: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("other watcher len = %d, want 0", len(empty))
	}
}

func TestAlertStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	as := NewAlertStore(db)
	ctx := context.Background()

	// Insert an alert with a backdated created_at
	_, err := db.ExecContext(ctx,
		`INSERT INTO alerts (id, title, summary, severity, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		"old-alert-1", "Old alert", "Very old", "warning",
		time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("inserting old alert: %v", err)
	}

	// Insert a recent alert
	_, err = as.Create(ctx, CreateAlertParams{
		Title:    "Recent alert",
		Summary:  "Just happened",
		Severity: SeverityInfo,
	})
	if err != nil {
		t.Fatalf("Create recent alert: %v", err)
	}

	// Prune alerts older than 24 hours
	pruned, err := as.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want 1", pruned)
	}

	// Verify only recent remains
	all, err := as.List(ctx, ListAlertParams{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("remaining = %d, want 1", len(all))
	}
	if all[0].Title != "Recent alert" {
		t.Errorf("title = %q, want %q", all[0].Title, "Recent alert")
	}
}

func TestAlertStore_DefaultSeverity(t *testing.T) {
	db := setupTestDB(t)
	as := NewAlertStore(db)
	ctx := context.Background()

	a, err := as.Create(ctx, CreateAlertParams{
		Title:   "No severity",
		Summary: "Test default",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.Severity != SeverityWarning {
		t.Errorf("default severity = %q, want %q", a.Severity, SeverityWarning)
	}
}
