package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// condJSON builds a simple threshold condition JSON for tests.
func condJSON(metric, op string, value float64) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"type":   "threshold",
		"metric": metric,
		"op":     op,
		"value":  value,
	})
	return raw
}

func TestWatchStore_CRUD(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatchStore(db)
	ctx := context.Background()

	// Create
	w, err := ws.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSON("error_rate", "gt", 0.05),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if w.Status != store.WatchStatusActive {
		t.Errorf("status = %q, want active", w.Status)
	}
	if w.ExpiresAt == nil {
		t.Error("expected expires_at to be set for duration=1h")
	}
	// Verify conditions round-trip
	if len(w.ConditionsJSON) == 0 {
		t.Error("expected conditions_json to be populated")
	}

	// GetByID
	got, err := ws.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.ConditionsJSON) == 0 {
		t.Error("expected conditions_json in fetched watch")
	}

	// List
	list, err := ws.List(ctx, store.ListWatchParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}

	// List with filter
	filtered, err := ws.List(ctx, store.ListWatchParams{Service: "other"})
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(filtered) != 0 {
		t.Errorf("filtered len = %d, want 0", len(filtered))
	}

	// UpdateStatus
	err = ws.UpdateStatus(ctx, w.ID, store.WatchStatusTriggered)
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = ws.GetByID(ctx, w.ID)
	if got.Status != store.WatchStatusTriggered {
		t.Errorf("status = %q, want triggered", got.Status)
	}

	// Delete
	err = ws.Delete(ctx, w.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = ws.GetByID(ctx, w.ID)
	if err != store.ErrNotFound {
		t.Errorf("GetByID after delete: err = %v, want ErrNotFound", err)
	}
}

func TestWatchStore_Defaults(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatchStore(db)
	ctx := context.Background()

	w, err := ws.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSON("log_count", "gt", 100),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.Duration != "1h" {
		t.Errorf("duration = %q, want 1h", w.Duration)
	}
	if w.Urgency != store.WatchUrgencyNormal {
		t.Errorf("urgency = %q, want normal", w.Urgency)
	}
	if w.CheckInterval != "30s" {
		t.Errorf("check_interval = %q, want 30s", w.CheckInterval)
	}
	if w.MinConsecutive != 1 {
		t.Errorf("min_consecutive = %d, want 1", w.MinConsecutive)
	}
}

func TestWatchStore_GetDueWatches(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatchStore(db)
	ctx := context.Background()

	// Create a watch with short check interval
	w, err := ws.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSON("error_rate", "gt", 0.1),
		CheckInterval:  "1ms",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Wait briefly so it becomes due
	time.Sleep(5 * time.Millisecond)

	due, err := ws.GetDueWatches(ctx)
	if err != nil {
		t.Fatalf("GetDueWatches: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("due len = %d, want 1", len(due))
	}
	if due[0].ID != w.ID {
		t.Errorf("due watch ID = %q, want %q", due[0].ID, w.ID)
	}
}

func TestWatchStore_ExpireWatches(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatchStore(db)
	ctx := context.Background()

	// Create a watch with very short duration
	_, err := ws.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSON("error_rate", "gt", 0.1),
		Duration:       "1ms",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	n, err := ws.ExpireWatches(ctx)
	if err != nil {
		t.Fatalf("ExpireWatches: %v", err)
	}
	if n != 1 {
		t.Errorf("expired = %d, want 1", n)
	}

	list, _ := ws.List(ctx, store.ListWatchParams{Status: store.WatchStatusExpired})
	if len(list) != 1 {
		t.Errorf("expired list len = %d, want 1", len(list))
	}
}

func TestWatchStore_UpdateAfterCheck(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatchStore(db)
	ctx := context.Background()

	w, err := ws.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSON("error_rate", "gt", 0.05),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	nextCheck := time.Now().UTC().Add(30 * time.Second)
	err = ws.UpdateAfterCheck(ctx, w.ID, 0.08, 2, nextCheck)
	if err != nil {
		t.Fatalf("UpdateAfterCheck: %v", err)
	}

	got, _ := ws.GetByID(ctx, w.ID)
	if got.CurrentValue == nil || *got.CurrentValue != 0.08 {
		t.Errorf("current_value = %v, want 0.08", got.CurrentValue)
	}
	if got.ConsecutiveBreaches != 2 {
		t.Errorf("consecutive_breaches = %d, want 2", got.ConsecutiveBreaches)
	}
}

func TestWatchStore_BaselineRoundTrip(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatchStore(db)
	ctx := context.Background()

	w, err := ws.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSON("response_time", "gt", 200),
		Service:        "api",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	baseline := &store.WatchBaseline{
		ErrorRate:        0.02,
		AvgResponseMs:    150,
		P95ResponseMs:    300,
		LogCount:         1000,
		ErrorCount:       20,
		ExceptionClasses: []string{"NoMethodError", "ActiveRecord::RecordNotFound"},
		Endpoints: []store.WatchEndpointBaseline{
			{Path: "/api/users", AvgDurationMs: 120, AvgSQLCount: 5, RequestCount: 100},
		},
		CapturedAt:     time.Now().UTC(),
		WindowDuration: "1h",
	}

	err = ws.UpdateBaseline(ctx, w.ID, baseline)
	if err != nil {
		t.Fatalf("UpdateBaseline: %v", err)
	}

	got, _ := ws.GetByID(ctx, w.ID)
	if got.BaselineJSON == nil {
		t.Fatal("baseline is nil after update")
	}
	if got.BaselineJSON.AvgResponseMs != 150 {
		t.Errorf("baseline avg_response_ms = %v, want 150", got.BaselineJSON.AvgResponseMs)
	}
	if len(got.BaselineJSON.ExceptionClasses) != 2 {
		t.Errorf("exception classes len = %d, want 2", len(got.BaselineJSON.ExceptionClasses))
	}
	if len(got.BaselineJSON.Endpoints) != 1 {
		t.Errorf("endpoints len = %d, want 1", len(got.BaselineJSON.Endpoints))
	}
}

func TestWatchStore_RunCRUD(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatchStore(db)
	ctx := context.Background()

	w, _ := ws.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSON("error_rate", "gt", 0.1),
	})

	// Create run
	run, err := ws.CreateRun(ctx, w.ID)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.Status != "running" {
		t.Errorf("run status = %q, want running", run.Status)
	}

	// Complete run
	err = ws.CompleteRun(ctx, run.ID, 0.15, true, "error rate above threshold")
	if err != nil {
		t.Fatalf("CompleteRun: %v", err)
	}

	// List runs
	runs, err := ws.ListRuns(ctx, w.ID, 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs len = %d, want 1", len(runs))
	}
	if runs[0].Status != "completed" {
		t.Errorf("run status = %q, want completed", runs[0].Status)
	}
	if !runs[0].Breached {
		t.Error("expected breached = true")
	}

	// Fail a run
	run2, _ := ws.CreateRun(ctx, w.ID)
	err = ws.FailRun(ctx, run2.ID, "connection timeout")
	if err != nil {
		t.Fatalf("FailRun: %v", err)
	}
	runs, _ = ws.ListRuns(ctx, w.ID, 10)
	if len(runs) != 2 {
		t.Fatalf("runs len = %d, want 2", len(runs))
	}
}

func TestWatchStore_AlertCRUD(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatchStore(db)
	ctx := context.Background()

	w, _ := ws.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSON("error_rate", "gt", 0.05),
	})

	// Create alert
	alert, err := ws.CreateAlert(ctx, store.CreateWatchAlertParams{
		WatchID:        w.ID,
		Urgency:        store.WatchUrgencyHigh,
		Summary:        "error rate spike",
		ConditionsSnapshot: store.BuildConditionsSnapshot("error_rate", 0.12, 0.05),
		Evidence: &store.WatchEvidenceBundle{
			RecentErrors: []store.WatchEvidenceError{
				{ExceptionClass: "NoMethodError", Message: "undefined method", Count: 5, IsNew: true},
			},
			SourceFiles: []string{"app/controllers/users_controller.rb"},
		},
	})
	if err != nil {
		t.Fatalf("CreateAlert: %v", err)
	}
	if alert.Status != "pending" {
		t.Errorf("alert status = %q, want pending", alert.Status)
	}

	// Get alert
	got, err := ws.GetAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if got.EvidenceJSON == nil {
		t.Fatal("evidence is nil")
	}
	if len(got.EvidenceJSON.RecentErrors) != 1 {
		t.Errorf("recent_errors len = %d, want 1", len(got.EvidenceJSON.RecentErrors))
	}

	// List alerts
	alerts, err := ws.ListAlerts(ctx, w.ID, "", 10)
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts len = %d, want 1", len(alerts))
	}

	// Count pending
	count, err := ws.CountPendingAlerts(ctx)
	if err != nil {
		t.Fatalf("CountPendingAlerts: %v", err)
	}
	if count != 1 {
		t.Errorf("pending = %d, want 1", count)
	}

	// Acknowledge
	err = ws.AcknowledgeAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("AcknowledgeAlert: %v", err)
	}
	got, _ = ws.GetAlert(ctx, alert.ID)
	if got.Status != "acknowledged" {
		t.Errorf("status = %q, want acknowledged", got.Status)
	}

	// Dismiss
	err = ws.DismissAlert(ctx, alert.ID, "false positive")
	if err != nil {
		t.Fatalf("DismissAlert: %v", err)
	}
	got, _ = ws.GetAlert(ctx, alert.ID)
	if got.Status != "dismissed" {
		t.Errorf("status = %q, want dismissed", got.Status)
	}
	if got.DismissReason != "false positive" {
		t.Errorf("dismiss_reason = %q, want 'false positive'", got.DismissReason)
	}

	// Count pending after dismiss
	count, _ = ws.CountPendingAlerts(ctx)
	if count != 0 {
		t.Errorf("pending after dismiss = %d, want 0", count)
	}
}

func TestWatchStore_List_Pagination(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatchStore(db)
	ctx := context.Background()

	// Create 4 watches
	for _, svc := range []string{"api", "web", "worker", "scheduler"} {
		_, err := ws.Create(ctx, store.CreateWatchParams{
			ConditionsJSON: condJSON("error_rate", "gt", 5.0),
			Service:        svc,
			Duration:       "24h",
		})
		if err != nil {
			t.Fatalf("Create %s: %v", svc, err)
		}
	}

	// No limit returns all
	all, err := ws.List(ctx, store.ListWatchParams{})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("List all len = %d, want 4", len(all))
	}

	// Limit=2 returns 2
	page1, err := ws.List(ctx, store.ListWatchParams{Limit: 2})
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}

	// Limit=2, Offset=2 returns next 2
	page2, err := ws.List(ctx, store.ListWatchParams{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(page2))
	}

	// Pages should not overlap
	if page1[0].ID == page2[0].ID {
		t.Error("page1 and page2 should not overlap")
	}

	// Limit with filter
	filtered, err := ws.List(ctx, store.ListWatchParams{Service: "api", Limit: 10})
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("filtered len = %d, want 1", len(filtered))
	}
}

func TestWatchStore_NotFound(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatchStore(db)
	ctx := context.Background()

	_, err := ws.GetByID(ctx, "nonexistent")
	if err != store.ErrNotFound {
		t.Errorf("GetByID: err = %v, want ErrNotFound", err)
	}

	err = ws.Delete(ctx, "nonexistent")
	if err != store.ErrNotFound {
		t.Errorf("Delete: err = %v, want ErrNotFound", err)
	}

	err = ws.UpdateStatus(ctx, "nonexistent", store.WatchStatusTriggered)
	if err != store.ErrNotFound {
		t.Errorf("UpdateStatus: err = %v, want ErrNotFound", err)
	}
}

func TestWatchStore_MissingConditions(t *testing.T) {
	db := setupTestDB(t)
	ws := NewWatchStore(db)
	ctx := context.Background()

	_, err := ws.Create(ctx, store.CreateWatchParams{
		Service: "web",
	})
	if err == nil {
		t.Fatal("expected error when conditions_json is missing")
	}
}
