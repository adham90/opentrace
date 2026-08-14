package watcher

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// makeWatchDue sets a watch's next_check_at to the past so GetDueWatches returns it.
// Newly created watches have next_check_at in the future (now + check_interval),
// so the scheduler's tick() won't pick them up without this adjustment.
func makeWatchDue(t *testing.T, ws store.WatchStore, watchID string) {
	t.Helper()
	ctx := context.Background()
	past := time.Now().UTC().Add(-1 * time.Minute)
	if err := ws.UpdateAfterCheck(ctx, watchID, 0, 0, past); err != nil {
		t.Fatalf("UpdateAfterCheck to make watch due: %v", err)
	}
}

func TestWatchScheduler_StartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	watchStore, logStore := setupWatchTestDB(t)
	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	scheduler := NewWatchScheduler(WatchSchedulerOpts{
		WatchStore:   watchStore,
		LogStore:     logStore,
		Evaluator:    evaluator,
		PollInterval: 100 * time.Millisecond,
	})

	ctx := context.Background()
	scheduler.Start(ctx)

	// Give it a moment to run at least one tick
	time.Sleep(50 * time.Millisecond)

	scheduler.Stop()
	// If we get here without panic or hang, the test passes.
}

func TestWatchScheduler_EvaluatesDueWatches(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert logs so the evaluator has data
	insertTestLogs(t, logStore, "web", 10, "info")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("log_count", "gt", 5, "web"),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create watch: %v", err)
	}

	// Make the watch due for evaluation
	makeWatchDue(t, watchStore, w.ID)

	scheduler := NewWatchScheduler(WatchSchedulerOpts{
		WatchStore:   watchStore,
		LogStore:     logStore,
		Evaluator:    evaluator,
		PollInterval: 1 * time.Hour, // Won't auto-tick
	})

	// Call tick() directly to avoid timing issues
	scheduler.tick(ctx)

	// Verify a watch run was created
	runs, err := watchStore.ListRuns(ctx, w.ID, 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) == 0 {
		t.Error("expected at least one watch run after tick()")
	}
}

func TestWatchScheduler_CreatesAlertOnBreach(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert enough error logs to breach the threshold
	insertTestLogs(t, logStore, "api", 8, "info")
	insertTestLogs(t, logStore, "api", 5, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	// Create a watch with low error_rate threshold (should be breached)
	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "api"),
		Service:        "api",
	})
	if err != nil {
		t.Fatalf("Create watch: %v", err)
	}

	makeWatchDue(t, watchStore, w.ID)

	scheduler := NewWatchScheduler(WatchSchedulerOpts{
		WatchStore:   watchStore,
		LogStore:     logStore,
		Evaluator:    evaluator,
		PollInterval: 1 * time.Hour,
	})

	scheduler.tick(ctx)

	// Verify alert was created
	alerts, err := watchStore.ListAlerts(ctx, w.ID, "", 10)
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	if len(alerts) == 0 {
		t.Fatal("expected an alert to be created after breach")
	}

	alert := alerts[0]
	if alert.TriggerMetric() != "error_rate" {
		t.Errorf("trigger_metric = %q, want error_rate", alert.TriggerMetric())
	}
	if alert.TriggerValue() <= 0.1 {
		t.Errorf("trigger_value = %v, expected > 0.1", alert.TriggerValue())
	}
}

func TestWatchScheduler_NotifiesOnAlert(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert logs that will breach the threshold
	insertTestLogs(t, logStore, "web", 5, "info")
	insertTestLogs(t, logStore, "web", 5, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)
	notifier := &mockNotifier{}

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "web"),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create watch: %v", err)
	}

	makeWatchDue(t, watchStore, w.ID)

	scheduler := NewWatchScheduler(WatchSchedulerOpts{
		WatchStore:   watchStore,
		LogStore:     logStore,
		Evaluator:    evaluator,
		Notifiers:    []WatchAlertNotifier{notifier},
		PollInterval: 1 * time.Hour,
	})

	scheduler.tick(ctx)
	// Notifications are dispatched off the tick so a wedged webhook cannot
	// stall watch evaluation; drain the dispatcher before asserting.
	scheduler.notify.wait()

	if notifier.getCalls() != 1 {
		t.Errorf("notifier calls = %d, want 1", notifier.getCalls())
	}
}

func TestWatchScheduler_NoAlertWhenBelowThreshold(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert only info logs -- no errors, so error_rate = 0
	insertTestLogs(t, logStore, "web", 20, "info")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)
	notifier := &mockNotifier{}

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.5, "web"),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create watch: %v", err)
	}

	makeWatchDue(t, watchStore, w.ID)

	scheduler := NewWatchScheduler(WatchSchedulerOpts{
		WatchStore:   watchStore,
		LogStore:     logStore,
		Evaluator:    evaluator,
		Notifiers:    []WatchAlertNotifier{notifier},
		PollInterval: 1 * time.Hour,
	})

	scheduler.tick(ctx)

	// Verify no alert was created
	alerts, err := watchStore.ListAlerts(ctx, w.ID, "", 10)
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts, got %d", len(alerts))
	}

	// Verify notifier was not called
	if notifier.getCalls() != 0 {
		t.Errorf("notifier calls = %d, want 0", notifier.getCalls())
	}
}

func TestWatchScheduler_DefaultNotifier(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	// Create scheduler without explicit notifiers -- should default to WatchLogNotifier
	scheduler := NewWatchScheduler(WatchSchedulerOpts{
		WatchStore:   watchStore,
		LogStore:     logStore,
		Evaluator:    evaluator,
		PollInterval: 1 * time.Hour,
	})

	if len(scheduler.notifiers) != 1 {
		t.Fatalf("expected 1 default notifier, got %d", len(scheduler.notifiers))
	}
	if _, ok := scheduler.notifiers[0].(*WatchLogNotifier); !ok {
		t.Error("expected default notifier to be *WatchLogNotifier")
	}
}

func TestWatchScheduler_DefaultPollInterval(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	scheduler := NewWatchScheduler(WatchSchedulerOpts{
		WatchStore: watchStore,
		LogStore:   logStore,
		Evaluator:  evaluator,
		// PollInterval not set -- should default to 15s
	})

	if scheduler.poll != 15*time.Second {
		t.Errorf("poll = %v, want 15s", scheduler.poll)
	}
}

func TestWatchScheduler_MultipleWatches(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert logs for two services
	insertTestLogs(t, logStore, "svc-a", 10, "info")
	insertTestLogs(t, logStore, "svc-a", 5, "error")
	insertTestLogs(t, logStore, "svc-b", 20, "info")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)
	notifier := &mockNotifier{}

	// Watch 1: svc-a error_rate > 0.1 (should breach -- ~33% error rate)
	w1, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "svc-a"),
		Service:        "svc-a",
	})
	if err != nil {
		t.Fatalf("Create watch 1: %v", err)
	}
	makeWatchDue(t, watchStore, w1.ID)

	// Watch 2: svc-b log_count > 100 (should NOT breach -- only 20 logs)
	w2, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("log_count", "gt", 100, "svc-b"),
		Service:        "svc-b",
	})
	if err != nil {
		t.Fatalf("Create watch 2: %v", err)
	}
	makeWatchDue(t, watchStore, w2.ID)

	scheduler := NewWatchScheduler(WatchSchedulerOpts{
		WatchStore:   watchStore,
		LogStore:     logStore,
		Evaluator:    evaluator,
		Notifiers:    []WatchAlertNotifier{notifier},
		PollInterval: 1 * time.Hour,
	})

	scheduler.tick(ctx)

	// Watch 1 should have an alert
	alerts1, err := watchStore.ListAlerts(ctx, w1.ID, "", 10)
	if err != nil {
		t.Fatalf("ListAlerts w1: %v", err)
	}
	if len(alerts1) == 0 {
		t.Error("expected alert for watch 1 (svc-a error_rate > 0.1)")
	}

	// Watch 2 should NOT have an alert
	alerts2, err := watchStore.ListAlerts(ctx, w2.ID, "", 10)
	if err != nil {
		t.Fatalf("ListAlerts w2: %v", err)
	}
	if len(alerts2) != 0 {
		t.Errorf("expected 0 alerts for watch 2 (svc-b log_count), got %d", len(alerts2))
	}

	// Notifier should have been called exactly once (for watch 1)
	if notifier.getCalls() != 1 {
		t.Errorf("notifier calls = %d, want 1", notifier.getCalls())
	}
}

func TestWatchScheduler_ContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-dependent test in short mode")
	}
	watchStore, logStore := setupWatchTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	scheduler := NewWatchScheduler(WatchSchedulerOpts{
		WatchStore:   watchStore,
		LogStore:     logStore,
		Evaluator:    evaluator,
		PollInterval: 50 * time.Millisecond,
	})

	scheduler.Start(ctx)
	cancel() // Cancel the parent context

	// Stop should return promptly
	done := make(chan struct{})
	go func() {
		scheduler.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler.Stop() did not return within 5 seconds after context cancellation")
	}
}

func TestWatchScheduler_RunRecordedOnNonBreach(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// Insert a single info log so the evaluator has data (error_rate = 0)
	insertTestLogs(t, logStore, "quiet-svc", 5, "info")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.5, "quiet-svc"),
		Service:        "quiet-svc",
	})
	if err != nil {
		t.Fatalf("Create watch: %v", err)
	}

	makeWatchDue(t, watchStore, w.ID)

	scheduler := NewWatchScheduler(WatchSchedulerOpts{
		WatchStore:   watchStore,
		LogStore:     logStore,
		Evaluator:    evaluator,
		PollInterval: 1 * time.Hour,
	})

	scheduler.tick(ctx)

	// Verify run was created even when no alert fires
	runs, err := watchStore.ListRuns(ctx, w.ID, 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) == 0 {
		t.Error("expected a run to be recorded even when threshold is not breached")
	}
}
