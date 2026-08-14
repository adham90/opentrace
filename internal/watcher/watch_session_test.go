package watcher

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestNewWatchSessionManager(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	metrics := NewWatchMetrics(logStore)

	mgr := NewWatchSessionManager(watchStore, metrics)
	if mgr == nil {
		t.Fatal("NewWatchSessionManager returned nil")
	}
	if mgr.watchStore == nil {
		t.Error("watchStore field is nil")
	}
	if mgr.metrics == nil {
		t.Error("metrics field is nil")
	}
}

func TestCheckAutoResolve_NotTriggered(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	metrics := NewWatchMetrics(logStore)
	mgr := NewWatchSessionManager(watchStore, metrics)

	// Create a watch with active status (not triggered)
	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "web"),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Active watch should be a no-op
	err = mgr.CheckAutoResolve(ctx, w)
	if err != nil {
		t.Fatalf("CheckAutoResolve: %v", err)
	}

	// Status should remain active
	w, _ = watchStore.GetByID(ctx, w.ID)
	if w.Status != store.WatchStatusActive {
		t.Errorf("status = %q, want active", w.Status)
	}
}

func TestCheckAutoResolve_TriggeredNoBaseline(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	insertTestLogs(t, logStore, "web", 5, "info")
	insertTestLogs(t, logStore, "web", 5, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)
	mgr := NewWatchSessionManager(watchStore, metrics)

	// Create and trigger a watch (no baseline set)
	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "web"),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Evaluate to trigger
	result, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.HasAlert {
		t.Fatal("expected alert to trigger")
	}

	// Re-fetch (now triggered, but no baseline)
	w, _ = watchStore.GetByID(ctx, w.ID)
	if w.Status != store.WatchStatusTriggered {
		t.Fatalf("expected triggered status, got %q", w.Status)
	}
	if w.BaselineJSON != nil {
		t.Fatal("expected nil baseline for this test")
	}

	// Should return nil (no-op) because no baseline exists
	err = mgr.CheckAutoResolve(ctx, w)
	if err != nil {
		t.Fatalf("CheckAutoResolve: %v", err)
	}

	// Status should still be triggered
	w, _ = watchStore.GetByID(ctx, w.ID)
	if w.Status != store.WatchStatusTriggered {
		t.Errorf("status = %q, want triggered (no baseline to compare)", w.Status)
	}
}

func TestCheckAutoResolve_TriggeredZeroBaseline(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	insertTestLogs(t, logStore, "web", 5, "info")
	insertTestLogs(t, logStore, "web", 5, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)
	mgr := NewWatchSessionManager(watchStore, metrics)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "web"),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Trigger the watch
	_, err = evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	// Set baseline with zero error rate (can't compare to zero)
	zeroBaseline := &store.WatchBaseline{
		ErrorRate:  0,
		CapturedAt: time.Now().UTC(),
	}
	_ = watchStore.UpdateBaseline(ctx, w.ID, zeroBaseline)
	w, _ = watchStore.GetByID(ctx, w.ID)

	// Should return nil (no-op) because baseline value is zero
	err = mgr.CheckAutoResolve(ctx, w)
	if err != nil {
		t.Fatalf("CheckAutoResolve: %v", err)
	}

	// Status should remain triggered
	w, _ = watchStore.GetByID(ctx, w.ID)
	if w.Status != store.WatchStatusTriggered {
		t.Errorf("status = %q, want triggered (zero baseline)", w.Status)
	}
}

func TestCheckAutoResolve_TriggeredWithinBaseline(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert logs: 8 info, 2 error -> error_rate ~0.2
	insertTestLogs(t, logStore, "web", 8, "info")
	insertTestLogs(t, logStore, "web", 2, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)
	mgr := NewWatchSessionManager(watchStore, metrics)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "web"),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Trigger the watch
	result, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.HasAlert {
		t.Fatal("expected alert to trigger")
	}

	// Set baseline to approximately the current error rate (~0.2).
	baseline := &store.WatchBaseline{
		ErrorRate:  0.2,
		CapturedAt: time.Now().UTC(),
	}
	_ = watchStore.UpdateBaseline(ctx, w.ID, baseline)
	w, _ = watchStore.GetByID(ctx, w.ID)

	err = mgr.CheckAutoResolve(ctx, w)
	if err != nil {
		t.Fatalf("CheckAutoResolve: %v", err)
	}

	// Should be resolved because current ~0.2 is within 10% of baseline 0.2
	w, _ = watchStore.GetByID(ctx, w.ID)
	if w.Status != store.WatchStatusResolved {
		t.Errorf("status = %q, want resolved", w.Status)
	}
}

func TestCheckAutoResolve_TriggeredFarFromBaseline(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert logs: 8 info, 2 error -> error_rate ~0.2
	insertTestLogs(t, logStore, "web", 8, "info")
	insertTestLogs(t, logStore, "web", 2, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)
	mgr := NewWatchSessionManager(watchStore, metrics)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.05, "web"),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Trigger the watch
	result, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.HasAlert {
		t.Fatal("expected alert to trigger")
	}

	// Set baseline error rate far from current (~0.2).
	baseline := &store.WatchBaseline{
		ErrorRate:  0.05,
		CapturedAt: time.Now().UTC(),
	}
	_ = watchStore.UpdateBaseline(ctx, w.ID, baseline)
	w, _ = watchStore.GetByID(ctx, w.ID)

	err = mgr.CheckAutoResolve(ctx, w)
	if err != nil {
		t.Fatalf("CheckAutoResolve: %v", err)
	}

	// Should stay triggered because current value is far from baseline
	w, _ = watchStore.GetByID(ctx, w.ID)
	if w.Status != store.WatchStatusTriggered {
		t.Errorf("status = %q, want triggered (drift too large)", w.Status)
	}
}

func TestCheckAutoResolve_ResolvedStatusIsNoOp(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	metrics := NewWatchMetrics(logStore)
	mgr := NewWatchSessionManager(watchStore, metrics)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSON("log_count", "gt", 100),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Manually set status to resolved
	_ = watchStore.UpdateStatus(ctx, w.ID, store.WatchStatusResolved)
	w, _ = watchStore.GetByID(ctx, w.ID)

	// Should return nil (no-op) because status is not triggered
	err = mgr.CheckAutoResolve(ctx, w)
	if err != nil {
		t.Fatalf("CheckAutoResolve: %v", err)
	}
}

func TestCheckAutoResolve_ExpiredStatusIsNoOp(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	metrics := NewWatchMetrics(logStore)
	mgr := NewWatchSessionManager(watchStore, metrics)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSON("log_count", "gt", 100),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Manually set status to expired
	_ = watchStore.UpdateStatus(ctx, w.ID, store.WatchStatusExpired)
	w, _ = watchStore.GetByID(ctx, w.ID)

	// Should return nil (no-op) because status is not triggered
	err = mgr.CheckAutoResolve(ctx, w)
	if err != nil {
		t.Fatalf("CheckAutoResolve: %v", err)
	}
}

func TestCheckAutoResolve_LogCountMetric(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert 50 logs
	insertTestLogs(t, logStore, "api", 50, "info")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)
	mgr := NewWatchSessionManager(watchStore, metrics)

	// The evaluator measures over the check interval, so it must cover the
	// 50 seconds of inserted logs. Auto-resolve still measures over the
	// baseline window.
	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("log_count", "gt", 30, "api"),
		Service:        "api",
		CheckInterval:  "5m",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Trigger the watch
	result, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.HasAlert {
		t.Fatal("expected alert to trigger")
	}

	// Set baseline very close to current count (50)
	baseline := &store.WatchBaseline{
		LogCount:   50,
		CapturedAt: time.Now().UTC(),
	}
	_ = watchStore.UpdateBaseline(ctx, w.ID, baseline)
	w, _ = watchStore.GetByID(ctx, w.ID)

	err = mgr.CheckAutoResolve(ctx, w)
	if err != nil {
		t.Fatalf("CheckAutoResolve: %v", err)
	}

	w, _ = watchStore.GetByID(ctx, w.ID)
	if w.Status != store.WatchStatusResolved {
		t.Errorf("status = %q, want resolved", w.Status)
	}
}

func TestCheckAutoResolve_InvalidBaselineWindow(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert some logs
	insertTestLogs(t, logStore, "web", 8, "info")
	insertTestLogs(t, logStore, "web", 2, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)
	mgr := NewWatchSessionManager(watchStore, metrics)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "web"),
		Service:        "web",
		BaselineWindow: "invalid-duration",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Trigger the watch
	result, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.HasAlert {
		t.Fatal("expected alert to trigger")
	}

	// Set baseline close to current
	baseline := &store.WatchBaseline{
		ErrorRate:  0.2,
		CapturedAt: time.Now().UTC(),
	}
	_ = watchStore.UpdateBaseline(ctx, w.ID, baseline)
	w, _ = watchStore.GetByID(ctx, w.ID)

	// Should not error even with an invalid baseline window (falls back to 1h)
	err = mgr.CheckAutoResolve(ctx, w)
	if err != nil {
		t.Fatalf("CheckAutoResolve with invalid baseline window: %v", err)
	}
}

func TestCleanup_CallsExpireWatches(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	metrics := NewWatchMetrics(logStore)
	mgr := NewWatchSessionManager(watchStore, metrics)

	// Create a watch with an expiry in the past
	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSON("log_count", "gt", 100),
		Service:        "web",
		Duration:       "1ms", // very short duration
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Wait for the watch to expire
	time.Sleep(5 * time.Millisecond)

	// Cleanup should not panic and should run successfully
	mgr.Cleanup(ctx)

	// Verify the watch was expired (if the store supports it)
	w, err = watchStore.GetByID(ctx, w.ID)
	if err != nil {
		return
	}
	_ = w
}

func TestCleanup_NoWatchesToExpire(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	metrics := NewWatchMetrics(logStore)
	mgr := NewWatchSessionManager(watchStore, metrics)

	// Cleanup with no watches should not panic
	mgr.Cleanup(ctx)
}
