package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestWatcherStore_CRUD(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	// Create
	params := CreateWatcherParams{
		Title:       "Test Watcher",
		Description: "Watch for errors in payment service",
		Severity:    SeverityCritical,
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
	if w.Severity != SeverityCritical {
		t.Errorf("severity = %q, want %q", w.Severity, SeverityCritical)
	}
	if w.Status != WatcherActive {
		t.Errorf("status = %q, want %q", w.Status, WatcherActive)
	}
	if w.TimeRange != "15m" {
		t.Errorf("time_range = %q, want %q", w.TimeRange, "15m")
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
	if err != ErrNotFound {
		t.Errorf("GetByID unknown = %v, want ErrNotFound", err)
	}

	// List
	list, err := s.List(ctx, ListWatcherParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List len = %d, want 1", len(list))
	}

	// Update
	newTitle := "Updated Title"
	newSeverity := SeverityWarning
	updated, err := s.Update(ctx, w.ID, UpdateWatcherParams{
		Title:    &newTitle,
		Severity: &newSeverity,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Title != newTitle {
		t.Errorf("updated title = %q, want %q", updated.Title, newTitle)
	}
	if updated.Severity != SeverityWarning {
		t.Errorf("updated severity = %q, want %q", updated.Severity, SeverityWarning)
	}

	// Update not found
	_, err = s.Update(ctx, uuid.New(), UpdateWatcherParams{Title: &newTitle})
	if err != ErrNotFound {
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
	if err != ErrNotFound {
		t.Errorf("Delete again = %v, want ErrNotFound", err)
	}
}

func TestWatcherStore_Defaults(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	w, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Minimal Watcher",
		Description: "Just a test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if w.Severity != SeverityWarning {
		t.Errorf("default severity = %q, want %q", w.Severity, SeverityWarning)
	}
	if w.TimeRange != "15m" {
		t.Errorf("default time_range = %q, want %q", w.TimeRange, "15m")
	}
}

func TestWatcherStore_ModelField(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	// Create with model
	w, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Model Watcher",
		Description: "Has a specific model",
		Model:       "anthropic-sonnet",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.Model != "anthropic-sonnet" {
		t.Errorf("model = %q, want %q", w.Model, "anthropic-sonnet")
	}

	// GetByID should preserve model
	got, err := s.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Model != "anthropic-sonnet" {
		t.Errorf("GetByID model = %q, want %q", got.Model, "anthropic-sonnet")
	}

	// List should include model
	list, err := s.List(ctx, ListWatcherParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Model != "anthropic-sonnet" {
		t.Errorf("List model = %q, want %q", list[0].Model, "anthropic-sonnet")
	}

	// Update model
	newModel := "openai-gpt4o"
	updated, err := s.Update(ctx, w.ID, UpdateWatcherParams{Model: &newModel})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Model != "openai-gpt4o" {
		t.Errorf("updated model = %q, want %q", updated.Model, "openai-gpt4o")
	}

	// Create without model — defaults to empty
	w2, err := s.Create(ctx, CreateWatcherParams{
		Title:       "No Model",
		Description: "Uses default",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w2.Model != "" {
		t.Errorf("default model = %q, want empty", w2.Model)
	}
}

func TestWatcherStore_EffortField(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	// Create with effort
	w, err := s.Create(ctx, CreateWatcherParams{
		Title:       "High Effort Watcher",
		Description: "Deep analysis",
		Effort:      EffortHigh,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.Effort != EffortHigh {
		t.Errorf("effort = %q, want %q", w.Effort, EffortHigh)
	}

	// GetByID preserves effort
	got, err := s.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Effort != EffortHigh {
		t.Errorf("GetByID effort = %q, want %q", got.Effort, EffortHigh)
	}

	// List includes effort
	list, err := s.List(ctx, ListWatcherParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Effort != EffortHigh {
		t.Errorf("List effort = %q, want %q", list[0].Effort, EffortHigh)
	}

	// Update effort
	newEffort := EffortLow
	updated, err := s.Update(ctx, w.ID, UpdateWatcherParams{Effort: &newEffort})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Effort != EffortLow {
		t.Errorf("updated effort = %q, want %q", updated.Effort, EffortLow)
	}

	// Default effort is medium
	w2, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Default Effort",
		Description: "Uses default",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w2.Effort != EffortMedium {
		t.Errorf("default effort = %q, want %q", w2.Effort, EffortMedium)
	}
}

func TestWatcherStore_WatcherType(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	// Default watcher_type is "ai"
	w, err := s.Create(ctx, CreateWatcherParams{
		Title:       "AI Watcher",
		Description: "Uses AI evaluation",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.WatcherType != WatcherTypeAI {
		t.Errorf("default watcher_type = %q, want %q", w.WatcherType, WatcherTypeAI)
	}
	if w.RuleConfig != nil {
		t.Error("expected nil RuleConfig for AI watcher")
	}
	if w.DataSourceID != nil {
		t.Error("expected nil DataSourceID for AI watcher")
	}

	// Create a rule watcher with rule_config
	dsID := "ds-123"
	ruleW, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Query Watcher",
		Description: "Checks connection count",
		WatcherType: WatcherTypeRule,
		RuleConfig: &RuleConfig{
			Source:    RuleSourceQuery,
			Query:     "SELECT count(*) FROM pg_stat_activity",
			Metric:    "value",
			Operator:  OpGreaterThan,
			Threshold: 80,
		},
		DataSourceID: &dsID,
	})
	if err != nil {
		t.Fatalf("Create rule watcher: %v", err)
	}
	if ruleW.WatcherType != WatcherTypeRule {
		t.Errorf("watcher_type = %q, want %q", ruleW.WatcherType, WatcherTypeRule)
	}
	if ruleW.RuleConfig == nil {
		t.Fatal("expected non-nil RuleConfig")
	}
	if ruleW.RuleConfig.Source != RuleSourceQuery {
		t.Errorf("rule source = %q, want %q", ruleW.RuleConfig.Source, RuleSourceQuery)
	}
	if ruleW.RuleConfig.Operator != OpGreaterThan {
		t.Errorf("operator = %q, want %q", ruleW.RuleConfig.Operator, OpGreaterThan)
	}
	if ruleW.RuleConfig.Threshold != 80 {
		t.Errorf("threshold = %f, want 80", ruleW.RuleConfig.Threshold)
	}
	if ruleW.DataSourceID == nil || *ruleW.DataSourceID != dsID {
		t.Errorf("data_source_id = %v, want %q", ruleW.DataSourceID, dsID)
	}

	// GetByID preserves rule_config
	got, err := s.GetByID(ctx, ruleW.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.RuleConfig == nil {
		t.Fatal("GetByID: expected non-nil RuleConfig")
	}
	if got.RuleConfig.Query != "SELECT count(*) FROM pg_stat_activity" {
		t.Errorf("GetByID rule query = %q", got.RuleConfig.Query)
	}

	// List returns watcher_type
	list, err := s.List(ctx, ListWatcherParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}
	// Check both types are present (order depends on timestamp resolution)
	types := map[WatcherType]bool{}
	for _, w := range list {
		types[w.WatcherType] = true
	}
	if !types[WatcherTypeAI] || !types[WatcherTypeRule] {
		t.Errorf("expected both ai and rule in list, got %v", types)
	}

	// Update rule_config
	newType := WatcherTypeRule
	updated, err := s.Update(ctx, w.ID, UpdateWatcherParams{
		WatcherType: &newType,
		RuleConfig: &RuleConfig{
			Source:    RuleSourceLogs,
			Metric:    "count",
			Operator:  OpGreaterThan,
			Threshold: 10,
			Filter: &LogFilter{
				Service: "payments",
				Level:   "ERROR",
			},
			TimeWindow: "5m",
		},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.WatcherType != WatcherTypeRule {
		t.Errorf("updated watcher_type = %q, want rule", updated.WatcherType)
	}
	if updated.RuleConfig == nil {
		t.Fatal("expected updated RuleConfig")
	}
	if updated.RuleConfig.Source != RuleSourceLogs {
		t.Errorf("updated rule source = %q, want logs", updated.RuleConfig.Source)
	}
	if updated.RuleConfig.Filter == nil || updated.RuleConfig.Filter.Service != "payments" {
		t.Error("expected filter service = payments")
	}
}

func TestWatcherStore_GetDueWatchers(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	// Create a watcher with next_run_at in the past (should be due)
	w1, err := s.Create(ctx, CreateWatcherParams{
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
	w2, err := s.Create(ctx, CreateWatcherParams{
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

func TestWatcherStore_UpdateAdaptiveState(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	w, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Adaptive Watcher",
		Description: "Test adaptive",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify defaults
	if w.AdaptiveState != AdaptiveNormal {
		t.Errorf("default adaptive_state = %q, want normal", w.AdaptiveState)
	}
	if w.ConsecutiveCleanRuns != 0 {
		t.Errorf("default consecutive_clean_runs = %d, want 0", w.ConsecutiveCleanRuns)
	}

	// Update to escalated
	escAt := time.Now().UTC().Truncate(time.Second)
	err = s.UpdateAdaptiveState(ctx, w.ID, UpdateAdaptiveParams{
		AdaptiveState:        AdaptiveEscalated,
		ConsecutiveCleanRuns: 0,
		ConsecutiveErrors:    0,
		EscalatedAt:          &escAt,
		TimeRange:            "1m",
		BaseTimeRange:        "15m",
	})
	if err != nil {
		t.Fatalf("UpdateAdaptiveState: %v", err)
	}

	got, err := s.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.AdaptiveState != AdaptiveEscalated {
		t.Errorf("adaptive_state = %q, want escalated", got.AdaptiveState)
	}
	if got.TimeRange != "1m" {
		t.Errorf("time_range = %q, want 1m", got.TimeRange)
	}
	if got.BaseTimeRange != "15m" {
		t.Errorf("base_time_range = %q, want 15m", got.BaseTimeRange)
	}
	if got.EscalatedAt == nil {
		t.Fatal("escalated_at should be set")
	}

	// Update counters
	err = s.UpdateAdaptiveState(ctx, w.ID, UpdateAdaptiveParams{
		AdaptiveState:        AdaptiveEscalated,
		ConsecutiveCleanRuns: 2,
		ConsecutiveErrors:    0,
	})
	if err != nil {
		t.Fatalf("UpdateAdaptiveState (counters): %v", err)
	}
	got, _ = s.GetByID(ctx, w.ID)
	if got.ConsecutiveCleanRuns != 2 {
		t.Errorf("consecutive_clean_runs = %d, want 2", got.ConsecutiveCleanRuns)
	}

	// Not found
	err = s.UpdateAdaptiveState(ctx, uuid.New(), UpdateAdaptiveParams{AdaptiveState: AdaptiveNormal})
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestWatcherStore_ResumeWatcher(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	w, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Error Watcher",
		Description: "Will be paused",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Put into adaptive error state
	err = s.UpdateAdaptiveState(ctx, w.ID, UpdateAdaptiveParams{
		AdaptiveState:     AdaptiveError,
		ConsecutiveErrors: 5,
	})
	if err != nil {
		t.Fatalf("UpdateAdaptiveState: %v", err)
	}
	s.UpdateStatus(ctx, w.ID, WatcherError)

	// Resume it
	err = s.ResumeWatcher(ctx, w.ID)
	if err != nil {
		t.Fatalf("ResumeWatcher: %v", err)
	}

	got, _ := s.GetByID(ctx, w.ID)
	if got.AdaptiveState != AdaptiveNormal {
		t.Errorf("adaptive_state = %q, want normal", got.AdaptiveState)
	}
	if got.ConsecutiveErrors != 0 {
		t.Errorf("consecutive_errors = %d, want 0", got.ConsecutiveErrors)
	}
	if got.Status != WatcherActive {
		t.Errorf("status = %q, want active", got.Status)
	}

	// Resume a non-error watcher should fail
	err = s.ResumeWatcher(ctx, w.ID)
	if err != ErrNotFound {
		t.Errorf("resume non-error: expected ErrNotFound, got %v", err)
	}
}

func TestWatcherStore_ExpiresAt(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	// Create watcher with expires_at
	future := time.Now().Add(1 * time.Hour).UTC().Truncate(time.Second)
	w, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Expiring Watcher",
		Description: "Expires in 1 hour",
		ExpiresAt:   &future,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.ExpiresAt == nil {
		t.Fatal("expected expires_at to be set")
	}
	if !w.ExpiresAt.Equal(future) {
		t.Errorf("expires_at = %v, want %v", w.ExpiresAt, future)
	}

	// GetByID preserves expires_at
	got, err := s.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ExpiresAt == nil {
		t.Fatal("GetByID: expected expires_at to be set")
	}

	// Create without expires_at — should be nil
	w2, err := s.Create(ctx, CreateWatcherParams{
		Title:       "No Expiry",
		Description: "Runs forever",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w2.ExpiresAt != nil {
		t.Errorf("expected nil expires_at, got %v", w2.ExpiresAt)
	}

	// Update expires_at
	newExpiry := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	updated, err := s.Update(ctx, w2.ID, UpdateWatcherParams{
		ExpiresAt: &newExpiry,
	})
	if err != nil {
		t.Fatalf("Update expires_at: %v", err)
	}
	if updated.ExpiresAt == nil {
		t.Fatal("expected updated expires_at to be set")
	}
	if !updated.ExpiresAt.Equal(newExpiry) {
		t.Errorf("updated expires_at = %v, want %v", updated.ExpiresAt, newExpiry)
	}

	// Clear expires_at
	cleared, err := s.Update(ctx, w2.ID, UpdateWatcherParams{
		ClearExpiresAt: true,
	})
	if err != nil {
		t.Fatalf("Clear expires_at: %v", err)
	}
	if cleared.ExpiresAt != nil {
		t.Errorf("expected nil expires_at after clear, got %v", cleared.ExpiresAt)
	}
}

func TestWatcherStore_ExpireWatchers(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	// Create a watcher with expires_at in the past
	past := time.Now().Add(-1 * time.Minute).UTC().Truncate(time.Second)
	w1, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Past Expiry",
		Description: "Should be expired",
		ExpiresAt:   &past,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Create a watcher with expires_at in the future (should NOT expire)
	future := time.Now().Add(1 * time.Hour).UTC().Truncate(time.Second)
	w2, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Future Expiry",
		Description: "Should stay active",
		ExpiresAt:   &future,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Create a watcher without expires_at (should NOT expire)
	w3, err := s.Create(ctx, CreateWatcherParams{
		Title:       "No Expiry",
		Description: "Runs forever",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Expire watchers
	n, err := s.ExpireWatchers(ctx)
	if err != nil {
		t.Fatalf("ExpireWatchers: %v", err)
	}
	if n != 1 {
		t.Errorf("expired count = %d, want 1", n)
	}

	// Verify w1 is expired
	got1, _ := s.GetByID(ctx, w1.ID)
	if got1.Status != WatcherExpired {
		t.Errorf("w1 status = %q, want expired", got1.Status)
	}

	// Verify w2 and w3 are still active
	got2, _ := s.GetByID(ctx, w2.ID)
	if got2.Status != WatcherActive {
		t.Errorf("w2 status = %q, want active", got2.Status)
	}
	got3, _ := s.GetByID(ctx, w3.ID)
	if got3.Status != WatcherActive {
		t.Errorf("w3 status = %q, want active", got3.Status)
	}

	// Calling ExpireWatchers again should expire 0 (already expired)
	n, err = s.ExpireWatchers(ctx)
	if err != nil {
		t.Fatalf("ExpireWatchers (2nd): %v", err)
	}
	if n != 0 {
		t.Errorf("second expire count = %d, want 0", n)
	}
}

func TestWatcherStore_GetDueWatchers_SkipsExpired(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	// Create a due watcher with expires_at in the past
	past := time.Now().Add(-1 * time.Minute).UTC().Truncate(time.Second)
	w1, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Due But Expired",
		Description: "Should not be fetched",
		ExpiresAt:   &past,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Set next_run_at in the past so it's "due"
	pastRun := time.Now().Add(-2 * time.Minute)
	s.UpdateRunTime(ctx, w1.ID, pastRun, pastRun)

	// Create a due watcher with no expiry
	w2, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Due No Expiry",
		Description: "Should be fetched",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.UpdateRunTime(ctx, w2.ID, pastRun, pastRun)

	due, err := s.GetDueWatchers(ctx)
	if err != nil {
		t.Fatalf("GetDueWatchers: %v", err)
	}

	// Only w2 should be returned (w1 has past expires_at)
	if len(due) != 1 {
		t.Fatalf("GetDueWatchers len = %d, want 1", len(due))
	}
	if due[0].ID != w2.ID {
		t.Errorf("due watcher ID = %v, want %v", due[0].ID, w2.ID)
	}
}

func TestWatcherStore_ResumeExpiredWatcher(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	// Create a watcher with past expiry
	past := time.Now().Add(-1 * time.Minute).UTC().Truncate(time.Second)
	w, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Expired Watcher",
		Description: "Will be resumed",
		ExpiresAt:   &past,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Expire it
	n, _ := s.ExpireWatchers(ctx)
	if n != 1 {
		t.Fatalf("expected 1 expired, got %d", n)
	}

	// Resume it
	err = s.ResumeWatcher(ctx, w.ID)
	if err != nil {
		t.Fatalf("ResumeWatcher: %v", err)
	}

	got, _ := s.GetByID(ctx, w.ID)
	if got.Status != WatcherActive {
		t.Errorf("status = %q, want active", got.Status)
	}
	if got.ExpiresAt != nil {
		t.Errorf("expires_at should be nil after resume, got %v", got.ExpiresAt)
	}
	if got.AdaptiveState != AdaptiveNormal {
		t.Errorf("adaptive_state = %q, want normal", got.AdaptiveState)
	}
}

func TestWatcherStore_AdaptiveConfig_RoundTrip(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	w, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Config Watcher",
		Description: "Test config",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Set adaptive_config via raw SQL (simulating what the API would do)
	cfg := AdaptiveConfig{
		Enabled:           true,
		EscalatedInterval: "1m",
		CooldownRuns:      3,
		BackoffMultiplier: 2.0,
		MaxConsecErrors:   5,
	}
	cfgJSON, _ := json.Marshal(cfg)
	_, err = db.ExecContext(ctx,
		`UPDATE watchers SET adaptive_config = ? WHERE id = ?`,
		string(cfgJSON), w.ID.String(),
	)
	if err != nil {
		t.Fatalf("setting adaptive_config: %v", err)
	}

	got, err := s.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.AdaptiveConfig == nil {
		t.Fatal("adaptive_config should not be nil")
	}
	if !got.AdaptiveConfig.Enabled {
		t.Error("adaptive_config.enabled = false, want true")
	}
	if got.AdaptiveConfig.EscalatedInterval != "1m" {
		t.Errorf("escalated_interval = %q, want 1m", got.AdaptiveConfig.EscalatedInterval)
	}
	if got.AdaptiveConfig.CooldownRuns != 3 {
		t.Errorf("cooldown_runs = %d, want 3", got.AdaptiveConfig.CooldownRuns)
	}
}
