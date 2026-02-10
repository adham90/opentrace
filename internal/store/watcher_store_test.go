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

func TestWatcherStore_MonitorType(t *testing.T) {
	db := setupTestDB(t)
	s := NewWatcherStore(db)
	ctx := context.Background()

	// Default monitor_type is "ai"
	w, err := s.Create(ctx, CreateWatcherParams{
		Title:       "AI Watcher",
		Description: "Uses AI evaluation",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.MonitorType != MonitorTypeAI {
		t.Errorf("default monitor_type = %q, want %q", w.MonitorType, MonitorTypeAI)
	}
	if w.RuleConfig != nil {
		t.Error("expected nil RuleConfig for AI watcher")
	}
	if w.DataSourceID != nil {
		t.Error("expected nil DataSourceID for AI watcher")
	}

	// Create a rule monitor with rule_config
	dsID := "ds-123"
	ruleW, err := s.Create(ctx, CreateWatcherParams{
		Title:       "Query Monitor",
		Description: "Checks connection count",
		MonitorType: MonitorTypeRule,
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
	if ruleW.MonitorType != MonitorTypeRule {
		t.Errorf("monitor_type = %q, want %q", ruleW.MonitorType, MonitorTypeRule)
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

	// List returns monitor_type
	list, err := s.List(ctx, ListWatcherParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}
	// Check both types are present (order depends on timestamp resolution)
	types := map[MonitorType]bool{}
	for _, w := range list {
		types[w.MonitorType] = true
	}
	if !types[MonitorTypeAI] || !types[MonitorTypeRule] {
		t.Errorf("expected both ai and rule in list, got %v", types)
	}

	// Update rule_config
	newType := MonitorTypeRule
	updated, err := s.Update(ctx, w.ID, UpdateWatcherParams{
		MonitorType: &newType,
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
	if updated.MonitorType != MonitorTypeRule {
		t.Errorf("updated monitor_type = %q, want rule", updated.MonitorType)
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
