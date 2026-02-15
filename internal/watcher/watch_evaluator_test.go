package watcher

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

func setupWatchTestDB(t *testing.T) (store.WatchStore, store.LogStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	watchStore := store.NewWatchStore(db)
	logStore := store.NewLogStore(db)
	return watchStore, logStore
}

func insertTestLogs(t *testing.T, logStore store.LogStore, service string, count int, level string) {
	t.Helper()
	ctx := context.Background()
	entries := make([]store.LogEntry, count)
	for i := 0; i < count; i++ {
		entries[i] = store.LogEntry{
			Timestamp: time.Now().UTC().Add(-time.Duration(i) * time.Second),
			Level:     level,
			Service:   service,
			Message:   "test log entry",
		}
	}
	_, err := logStore.BatchInsert(ctx, entries)
	if err != nil {
		t.Fatalf("inserting test logs: %v", err)
	}
}

func TestWatchEvaluator_ErrorRate(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert mixed logs: 8 info, 2 error → error_rate = 0.2
	insertTestLogs(t, logStore, "web", 8, "info")
	insertTestLogs(t, logStore, "web", 2, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		Metric:    store.WatchMetricErrorRate,
		Operator:  store.WatchOpGreaterThan,
		Threshold: 0.1,
		Service:   "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if !result.Breached {
		t.Error("expected breached = true")
	}
	if !result.HasAlert {
		t.Error("expected HasAlert = true")
	}
	if result.Value < 0.15 || result.Value > 0.25 {
		t.Errorf("expected error rate ~0.2, got %v", result.Value)
	}
}

func TestWatchEvaluator_LogCount(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	insertTestLogs(t, logStore, "api", 50, "info")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		Metric:    store.WatchMetricLogCount,
		Operator:  store.WatchOpGreaterThan,
		Threshold: 30,
		Service:   "api",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if !result.Breached {
		t.Error("expected breached = true for 50 > 30")
	}
	if result.Value != 50 {
		t.Errorf("value = %v, want 50", result.Value)
	}
}

func TestWatchEvaluator_AlertSuppression(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	insertTestLogs(t, logStore, "web", 8, "info")
	insertTestLogs(t, logStore, "web", 2, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		Metric:    store.WatchMetricErrorRate,
		Operator:  store.WatchOpGreaterThan,
		Threshold: 0.1,
		Service:   "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// First evaluation: should trigger
	result1, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate 1: %v", err)
	}
	if !result1.HasAlert {
		t.Fatal("expected first evaluation to have alert")
	}

	// Re-fetch (status changed to triggered)
	w, _ = watchStore.GetByID(ctx, w.ID)

	// Second evaluation: should NOT re-alert (suppressed)
	result2, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate 2: %v", err)
	}
	if result2.HasAlert {
		t.Error("expected second evaluation to suppress alert")
	}
}

func TestWatchEvaluator_ConsecutiveBreaches(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	insertTestLogs(t, logStore, "web", 8, "info")
	insertTestLogs(t, logStore, "web", 2, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		Metric:         store.WatchMetricErrorRate,
		Operator:       store.WatchOpGreaterThan,
		Threshold:      0.1,
		Service:        "web",
		MinConsecutive: 3,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// First evaluation: breached but no alert (1/3)
	result1, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate 1: %v", err)
	}
	if result1.HasAlert {
		t.Error("expected no alert after 1/3 breaches")
	}
	if !result1.Breached {
		t.Error("expected breached = true")
	}

	// Re-fetch
	w, _ = watchStore.GetByID(ctx, w.ID)

	// Second evaluation: 2/3
	result2, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate 2: %v", err)
	}
	if result2.HasAlert {
		t.Error("expected no alert after 2/3 breaches")
	}

	// Re-fetch
	w, _ = watchStore.GetByID(ctx, w.ID)

	// Third evaluation: 3/3 → alert!
	result3, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate 3: %v", err)
	}
	if !result3.HasAlert {
		t.Error("expected alert after 3/3 breaches")
	}
}

func TestWatchEvaluator_Heartbeat(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert a recent log
	insertTestLogs(t, logStore, "worker", 1, "info")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	// Watch: alert if heartbeat > 60 seconds (i.e., no logs for 60s)
	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		Metric:    store.WatchMetricHeartbeat,
		Operator:  store.WatchOpGreaterThan,
		Threshold: 60,
		Service:   "worker",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	// Recent log exists → seconds since last log should be small
	if result.Breached {
		t.Errorf("expected not breached (recent log exists), value = %v", result.Value)
	}
}

func TestWatchEvidenceBuilder_Build(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	insertTestLogs(t, logStore, "web", 5, "info")
	insertTestLogs(t, logStore, "web", 3, "error")

	metrics := NewWatchMetrics(logStore)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		Metric:    store.WatchMetricErrorRate,
		Operator:  store.WatchOpGreaterThan,
		Threshold: 0.1,
		Service:   "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Capture baseline
	baseline, err := CaptureBaseline(ctx, logStore, metrics, w)
	if err != nil {
		t.Fatalf("CaptureBaseline: %v", err)
	}
	if err := watchStore.UpdateBaseline(ctx, w.ID, baseline); err != nil {
		t.Fatalf("UpdateBaseline: %v", err)
	}

	// Re-fetch
	w, _ = watchStore.GetByID(ctx, w.ID)

	evidenceBuilder := NewWatchEvidenceBuilder(logStore, metrics)
	result := &WatchEvalResult{Value: 0.375, Breached: true, HasAlert: true}

	evidence, err := evidenceBuilder.Build(ctx, w, result)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(evidence.RecentErrors) == 0 {
		t.Error("expected recent errors in evidence")
	}
	if len(evidence.RelevantLogs) == 0 {
		t.Error("expected relevant logs in evidence")
	}
	if len(evidence.Timeline) == 0 {
		t.Error("expected timeline events in evidence")
	}
	if evidence.BaselineDiff == nil {
		t.Error("expected baseline diff in evidence")
	}
}

func TestCaptureBaseline(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	insertTestLogs(t, logStore, "api", 90, "info")
	insertTestLogs(t, logStore, "api", 10, "error")

	metrics := NewWatchMetrics(logStore)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		Metric:    store.WatchMetricErrorRate,
		Operator:  store.WatchOpGreaterThan,
		Threshold: 0.15,
		Service:   "api",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	baseline, err := CaptureBaseline(ctx, logStore, metrics, w)
	if err != nil {
		t.Fatalf("CaptureBaseline: %v", err)
	}

	if baseline.LogCount != 100 {
		t.Errorf("log_count = %d, want 100", baseline.LogCount)
	}
	if baseline.ErrorCount != 10 {
		t.Errorf("error_count = %d, want 10", baseline.ErrorCount)
	}
	if baseline.ErrorRate != 0.1 {
		t.Errorf("error_rate = %v, want 0.1", baseline.ErrorRate)
	}
	if baseline.WindowDuration != "1h" {
		t.Errorf("window_duration = %q, want 1h", baseline.WindowDuration)
	}
}

func TestWatchSessionManager_AutoResolve(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert logs with error rate ~20%
	insertTestLogs(t, logStore, "web", 8, "info")
	insertTestLogs(t, logStore, "web", 2, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		Metric:    store.WatchMetricErrorRate,
		Operator:  store.WatchOpGreaterThan,
		Threshold: 0.1,
		Service:   "web",
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

	// Set baseline to approximately current error rate (so auto-resolve will trigger)
	baseline := &store.WatchBaseline{
		ErrorRate:  0.2,
		CapturedAt: time.Now().UTC(),
	}
	_ = watchStore.UpdateBaseline(ctx, w.ID, baseline)
	w, _ = watchStore.GetByID(ctx, w.ID)

	// Now check auto-resolve — current error rate should be within 10% of baseline
	sessionMgr := NewWatchSessionManager(watchStore, metrics)
	err = sessionMgr.CheckAutoResolve(ctx, w)
	if err != nil {
		t.Fatalf("CheckAutoResolve: %v", err)
	}

	w, _ = watchStore.GetByID(ctx, w.ID)
	if w.Status != store.WatchStatusResolved {
		t.Errorf("status = %q, want resolved", w.Status)
	}
}
