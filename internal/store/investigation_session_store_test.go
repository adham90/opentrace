package store

import (
	"context"
	"testing"
	"time"
)

func TestInvestigationSessionStore_CreateAndGet(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, err := s.Create(ctx, CreateInvestigationSessionParams{
		UserID:        "user-1",
		UserEmail:     "dev@example.com",
		UserRole:      "admin",
		ClientName:    "claude-code",
		ClientVersion: "1.2.3",
		Workspace:     "/home/dev/project",
		Transport:     "stdio",
		ConnectionID:  "conn-abc",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if sess.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", sess.UserID, "user-1")
	}
	if sess.ClientName != "claude-code" {
		t.Errorf("ClientName = %q, want %q", sess.ClientName, "claude-code")
	}
	if sess.Status != InvestigationStatusOpen {
		t.Errorf("Status = %q, want %q", sess.Status, InvestigationStatusOpen)
	}

	got, err := s.GetByID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("GetByID returned wrong session")
	}
}

func TestInvestigationSessionStore_RecordStep(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, err := s.Create(ctx, CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Record 3 steps.
	if err := s.RecordStep(ctx, sess.ID, "diagnose", false); err != nil {
		t.Fatalf("RecordStep 1: %v", err)
	}
	if err := s.RecordStep(ctx, sess.ID, "error_detail", false); err != nil {
		t.Fatalf("RecordStep 2: %v", err)
	}
	if err := s.RecordStep(ctx, sess.ID, "log_search", true); err != nil {
		t.Fatalf("RecordStep 3: %v", err)
	}

	got, err := s.GetByID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.TotalSteps != 3 {
		t.Errorf("TotalSteps = %d, want 3", got.TotalSteps)
	}
	if got.TotalErrors != 1 {
		t.Errorf("TotalErrors = %d, want 1", got.TotalErrors)
	}
	if len(got.ToolSequence) != 3 {
		t.Errorf("ToolSequence len = %d, want 3", len(got.ToolSequence))
	}
	if got.ToolFingerprint != "diagnose|error_detail|log_search" {
		t.Errorf("ToolFingerprint = %q, want %q", got.ToolFingerprint, "diagnose|error_detail|log_search")
	}
}

func TestInvestigationSessionStore_Close(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, err := s.Create(ctx, CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.Close(ctx, sess.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := s.GetByID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != InvestigationStatusUnresolved {
		t.Errorf("Status = %q, want %q (auto-set on close)", got.Status, InvestigationStatusUnresolved)
	}
	if got.EndedAt == nil {
		t.Error("EndedAt should be set after Close")
	}
	if got.DurationSeconds < 0 {
		t.Errorf("DurationSeconds = %d, want >= 0", got.DurationSeconds)
	}
}

func TestInvestigationSessionStore_Update(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, err := s.Create(ctx, CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	intent := "investigation"
	detail := "payment timeout errors"
	summary := "Found missing index"
	status := InvestigationStatusResolved

	err = s.Update(ctx, sess.ID, UpdateInvestigationSessionParams{
		Intent:       &intent,
		IntentDetail: &detail,
		Summary:      &summary,
		Status:       &status,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.GetByID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Intent != "investigation" {
		t.Errorf("Intent = %q, want %q", got.Intent, "investigation")
	}
	if got.Summary != "Found missing index" {
		t.Errorf("Summary = %q, want %q", got.Summary, "Found missing index")
	}
	if got.Status != InvestigationStatusResolved {
		t.Errorf("Status = %q, want %q", got.Status, InvestigationStatusResolved)
	}
}

func TestInvestigationSessionStore_FindRecent(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	_, err := s.Create(ctx, CreateInvestigationSessionParams{
		UserID:    "user-1",
		Workspace: "/home/dev/project",
		Transport: "stdio",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Should find the recent session.
	found, err := s.FindRecent(ctx, FindRecentSessionParams{
		UserID:    "user-1",
		Workspace: "/home/dev/project",
		MaxAge:    30 * time.Minute,
		Status:    InvestigationStatusOpen,
	})
	if err != nil {
		t.Fatalf("FindRecent: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find recent session")
	}

	// Different user should not find it.
	notFound, err := s.FindRecent(ctx, FindRecentSessionParams{
		UserID:    "user-2",
		Workspace: "/home/dev/project",
		MaxAge:    30 * time.Minute,
		Status:    InvestigationStatusOpen,
	})
	if err != nil {
		t.Fatalf("FindRecent: %v", err)
	}
	if notFound != nil {
		t.Error("should not find session for different user")
	}
}

func TestInvestigationSessionStore_List(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := s.Create(ctx, CreateInvestigationSessionParams{
			UserID:    "user-1",
			Transport: "stdio",
		})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	sessions, err := s.List(ctx, ListInvestigationSessionParams{
		UserID: "user-1",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("List returned %d sessions, want 3", len(sessions))
	}
}

func TestInvestigationSessionStore_Stats(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, _ := s.Create(ctx, CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})
	_ = s.Close(ctx, sess.ID)

	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalSessions != 1 {
		t.Errorf("TotalSessions = %d, want 1", stats.TotalSessions)
	}
}

func TestInvestigationSessionStore_Prune(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, _ := s.Create(ctx, CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})
	_ = s.Close(ctx, sess.ID)

	// Backdate the ended_at to make it prunable.
	_, err := db.ExecContext(ctx,
		`UPDATE investigation_sessions SET ended_at = datetime('now', '-2 days') WHERE id = ?`, sess.ID)
	if err != nil {
		t.Fatalf("backdating ended_at: %v", err)
	}

	// Prune sessions older than 1 day.
	n, err := s.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("Prune deleted %d, want 1", n)
	}
}

func TestInvestigationSessionStore_GetNotFound(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	_, err := s.GetByID(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Errorf("GetByID should return ErrNotFound, got %v", err)
	}
}
