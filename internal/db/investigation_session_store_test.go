package db

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestInvestigationSessionStore_CreateAndGet(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, err := s.Create(ctx, store.CreateInvestigationSessionParams{
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
	if sess.Status != store.InvestigationStatusOpen {
		t.Errorf("Status = %q, want %q", sess.Status, store.InvestigationStatusOpen)
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

	sess, err := s.Create(ctx, store.CreateInvestigationSessionParams{
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

	sess, err := s.Create(ctx, store.CreateInvestigationSessionParams{
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
	if got.Status != store.InvestigationStatusUnresolved {
		t.Errorf("Status = %q, want %q (auto-set on close)", got.Status, store.InvestigationStatusUnresolved)
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

	sess, err := s.Create(ctx, store.CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	intent := "investigation"
	detail := "payment timeout errors"
	summary := "Found missing index"
	status := store.InvestigationStatusResolved

	err = s.Update(ctx, sess.ID, store.UpdateInvestigationSessionParams{
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
	if got.Status != store.InvestigationStatusResolved {
		t.Errorf("Status = %q, want %q", got.Status, store.InvestigationStatusResolved)
	}
}

func TestInvestigationSessionStore_FindRecent(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	_, err := s.Create(ctx, store.CreateInvestigationSessionParams{
		UserID:    "user-1",
		Workspace: "/home/dev/project",
		Transport: "stdio",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Should find the recent session.
	found, err := s.FindRecent(ctx, store.FindRecentSessionParams{
		UserID:    "user-1",
		Workspace: "/home/dev/project",
		MaxAge:    30 * time.Minute,
		Status:    store.InvestigationStatusOpen,
	})
	if err != nil {
		t.Fatalf("FindRecent: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find recent session")
	}

	// Different user should not find it.
	notFound, err := s.FindRecent(ctx, store.FindRecentSessionParams{
		UserID:    "user-2",
		Workspace: "/home/dev/project",
		MaxAge:    30 * time.Minute,
		Status:    store.InvestigationStatusOpen,
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
		_, err := s.Create(ctx, store.CreateInvestigationSessionParams{
			UserID:    "user-1",
			Transport: "stdio",
		})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	sessions, err := s.List(ctx, store.ListInvestigationSessionParams{
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

	sess, _ := s.Create(ctx, store.CreateInvestigationSessionParams{
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

	sess, _ := s.Create(ctx, store.CreateInvestigationSessionParams{
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
	if err != store.ErrNotFound {
		t.Errorf("GetByID should return ErrNotFound, got %v", err)
	}
}

func TestInvestigationSessionStore_UpdateSubsystemLinks(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, err := s.Create(ctx, store.CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	watcherID := "w-123"
	hcID := "hc-456"
	fingerprint := "fp-abc"
	group := "watcher:w-123"
	prevID := "prev-session"
	count := 2
	durability := 3600

	err = s.Update(ctx, sess.ID, store.UpdateInvestigationSessionParams{
		CreatedWatcherIDs:             []string{watcherID},
		TriggeredByWatcherID:          &watcherID,
		ResolvedErrorGroupIDs:         []string{fingerprint},
		InvestigatedErrorFingerprints: []string{fingerprint},
		CreatedHealthcheckIDs:         []string{hcID},
		TriggeredByHealthcheckID:      &hcID,
		RecurrenceGroup:               &group,
		RecurrenceCount:               &count,
		PreviousSessionID:             &prevID,
		FixDurabilitySeconds:          &durability,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.GetByID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.CreatedWatcherIDs) != 1 || got.CreatedWatcherIDs[0] != watcherID {
		t.Errorf("CreatedWatcherIDs = %v, want [%s]", got.CreatedWatcherIDs, watcherID)
	}
	if got.TriggeredByWatcherID == nil || *got.TriggeredByWatcherID != watcherID {
		t.Errorf("TriggeredByWatcherID = %v, want %s", got.TriggeredByWatcherID, watcherID)
	}
	if len(got.ResolvedErrorGroupIDs) != 1 || got.ResolvedErrorGroupIDs[0] != fingerprint {
		t.Errorf("ResolvedErrorGroupIDs = %v, want [%s]", got.ResolvedErrorGroupIDs, fingerprint)
	}
	if len(got.InvestigatedErrorFingerprints) != 1 || got.InvestigatedErrorFingerprints[0] != fingerprint {
		t.Errorf("InvestigatedErrorFingerprints = %v, want [%s]", got.InvestigatedErrorFingerprints, fingerprint)
	}
	if len(got.CreatedHealthcheckIDs) != 1 || got.CreatedHealthcheckIDs[0] != hcID {
		t.Errorf("CreatedHealthcheckIDs = %v, want [%s]", got.CreatedHealthcheckIDs, hcID)
	}
	if got.TriggeredByHealthcheckID == nil || *got.TriggeredByHealthcheckID != hcID {
		t.Errorf("TriggeredByHealthcheckID = %v, want %s", got.TriggeredByHealthcheckID, hcID)
	}
	if got.RecurrenceGroup == nil || *got.RecurrenceGroup != group {
		t.Errorf("RecurrenceGroup = %v, want %s", got.RecurrenceGroup, group)
	}
	if got.RecurrenceCount != count {
		t.Errorf("RecurrenceCount = %d, want %d", got.RecurrenceCount, count)
	}
	if got.PreviousSessionID == nil || *got.PreviousSessionID != prevID {
		t.Errorf("PreviousSessionID = %v, want %s", got.PreviousSessionID, prevID)
	}
	if got.FixDurabilitySeconds == nil || *got.FixDurabilitySeconds != durability {
		t.Errorf("FixDurabilitySeconds = %v, want %d", got.FixDurabilitySeconds, durability)
	}
}

func TestInvestigationSessionStore_FindByCreatedWatcher(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, _ := s.Create(ctx, store.CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})

	// Update with a created watcher.
	err := s.Update(ctx, sess.ID, store.UpdateInvestigationSessionParams{
		CreatedWatcherIDs: []string{"w-abc"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	found, err := s.FindByCreatedWatcher(ctx, "w-abc")
	if err != nil {
		t.Fatalf("FindByCreatedWatcher: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find session")
	}
	if found.ID != sess.ID {
		t.Errorf("found wrong session: %s, want %s", found.ID, sess.ID)
	}

	// Non-existent watcher should return nil.
	notFound, err := s.FindByCreatedWatcher(ctx, "w-nonexistent")
	if err != nil {
		t.Fatalf("FindByCreatedWatcher: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for non-existent watcher")
	}
}

func TestInvestigationSessionStore_FindByResolvedError(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, _ := s.Create(ctx, store.CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})

	err := s.Update(ctx, sess.ID, store.UpdateInvestigationSessionParams{
		ResolvedErrorGroupIDs: []string{"fp-resolved"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	found, err := s.FindByResolvedError(ctx, "fp-resolved")
	if err != nil {
		t.Fatalf("FindByResolvedError: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find session")
	}
	if found.ID != sess.ID {
		t.Errorf("found wrong session: %s, want %s", found.ID, sess.ID)
	}

	notFound, err := s.FindByResolvedError(ctx, "fp-nonexistent")
	if err != nil {
		t.Fatalf("FindByResolvedError: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for non-existent fingerprint")
	}
}

func TestInvestigationSessionStore_FindByCreatedHealthcheck(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, _ := s.Create(ctx, store.CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})

	err := s.Update(ctx, sess.ID, store.UpdateInvestigationSessionParams{
		CreatedHealthcheckIDs: []string{"hc-abc"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	found, err := s.FindByCreatedHealthcheck(ctx, "hc-abc")
	if err != nil {
		t.Fatalf("FindByCreatedHealthcheck: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find session")
	}
	if found.ID != sess.ID {
		t.Errorf("found wrong session: %s, want %s", found.ID, sess.ID)
	}

	notFound, err := s.FindByCreatedHealthcheck(ctx, "hc-nonexistent")
	if err != nil {
		t.Fatalf("FindByCreatedHealthcheck: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for non-existent healthcheck")
	}
}

func TestInvestigationSessionStore_FindSimilar(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	// Create a resolved session with tools and service
	sess1, _ := s.Create(ctx, store.CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})
	intent := "investigation"
	service := "api"
	status := store.InvestigationStatusResolved
	_ = s.Update(ctx, sess1.ID, store.UpdateInvestigationSessionParams{
		Intent:         &intent,
		PrimaryService: &service,
		Status:         &status,
	})
	_ = s.RecordStep(ctx, sess1.ID, "diagnose", false)
	_ = s.RecordStep(ctx, sess1.ID, "error_groups", false)
	_ = s.RecordStep(ctx, sess1.ID, "log_search", false)

	// Create current session
	sess2, _ := s.Create(ctx, store.CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})

	// FindSimilar should return sess1
	similar, err := s.FindSimilar(ctx, store.FindSimilarParams{
		Service:          "api",
		Intent:           "investigation",
		ExcludeSessionID: sess2.ID,
		MaxResults:       3,
		MinSteps:         2,
		OnlyResolved:     true,
	})
	if err != nil {
		t.Fatalf("FindSimilar: %v", err)
	}
	if len(similar) != 1 {
		t.Fatalf("expected 1 similar session, got %d", len(similar))
	}
	if similar[0].ID != sess1.ID {
		t.Errorf("expected session %s, got %s", sess1.ID, similar[0].ID)
	}
}

func TestInvestigationSessionStore_FindSimilar_ExcludesCurrent(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, _ := s.Create(ctx, store.CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})
	intent := "investigation"
	status := store.InvestigationStatusResolved
	_ = s.Update(ctx, sess.ID, store.UpdateInvestigationSessionParams{
		Intent: &intent,
		Status: &status,
	})
	_ = s.RecordStep(ctx, sess.ID, "diagnose", false)
	_ = s.RecordStep(ctx, sess.ID, "error_groups", false)

	// FindSimilar excluding self should return nothing
	similar, err := s.FindSimilar(ctx, store.FindSimilarParams{
		Intent:           "investigation",
		ExcludeSessionID: sess.ID,
		MaxResults:       3,
		MinSteps:         1,
		OnlyResolved:     true,
	})
	if err != nil {
		t.Fatalf("FindSimilar: %v", err)
	}
	if len(similar) != 0 {
		t.Errorf("expected 0 similar sessions when excluding self, got %d", len(similar))
	}
}

func TestInvestigationSessionStore_FindSimilar_RespectsMinSteps(t *testing.T) {
	db := setupTestDB(t)
	s := NewInvestigationSessionStore(db)
	ctx := context.Background()

	sess, _ := s.Create(ctx, store.CreateInvestigationSessionParams{
		UserID:    "user-1",
		Transport: "stdio",
	})
	intent := "investigation"
	status := store.InvestigationStatusResolved
	_ = s.Update(ctx, sess.ID, store.UpdateInvestigationSessionParams{
		Intent: &intent,
		Status: &status,
	})
	_ = s.RecordStep(ctx, sess.ID, "diagnose", false) // only 1 step

	similar, err := s.FindSimilar(ctx, store.FindSimilarParams{
		Intent:           "investigation",
		ExcludeSessionID: "other",
		MaxResults:       3,
		MinSteps:         3, // requires 3 steps
		OnlyResolved:     true,
	})
	if err != nil {
		t.Fatalf("FindSimilar: %v", err)
	}
	if len(similar) != 0 {
		t.Errorf("expected 0 sessions with minSteps=3, got %d", len(similar))
	}
}
