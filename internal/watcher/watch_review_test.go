package watcher

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/adham90/opentrace/pkg/store"
)

// TestWatchEvaluator_ConcurrentTriggerFiresOnce reproduces the read-status-
// before-write race between the periodic scheduler and the reactive stream
// evaluator. Two evaluations start from the same "active" snapshot and run
// concurrently through the shared evaluator; exactly one must win the
// transition and report HasAlert.
func TestWatchEvaluator_ConcurrentTriggerFiresOnce(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// 50% error rate breaches the 0.1 threshold immediately.
	insertTestLogs(t, logStore, "web", 5, "info")
	insertTestLogs(t, logStore, "web", 5, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "web"),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Two independent "active" snapshots, mirroring the scheduler and the stream
	// evaluator each holding their own copy before either has triggered.
	snapA, _ := watchStore.GetByID(ctx, w.ID)
	snapB, _ := watchStore.GetByID(ctx, w.ID)
	if snapA.Status == store.WatchStatusTriggered || snapB.Status == store.WatchStatusTriggered {
		t.Fatal("precondition: watch should start not-triggered")
	}

	var wg sync.WaitGroup
	results := make([]*WatchEvalResult, 2)
	errs := make([]error, 2)
	snaps := []*store.Watch{snapA, snapB}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = evaluator.Evaluate(ctx, snaps[i])
		}(i)
	}
	wg.Wait()

	alerts := 0
	for i := 0; i < 2; i++ {
		if errs[i] != nil {
			t.Fatalf("Evaluate[%d]: %v", i, errs[i])
		}
		if results[i].HasAlert {
			alerts++
		}
	}
	if alerts != 1 {
		t.Fatalf("expected exactly 1 alert on simultaneous double-evaluation, got %d", alerts)
	}

	// The watch must be left in the triggered state.
	final, _ := watchStore.GetByID(ctx, w.ID)
	if final.Status != store.WatchStatusTriggered {
		t.Errorf("final status = %q, want triggered", final.Status)
	}
}

// TestWatchStreamEvaluator_NotifiesOnAlert proves the stream path invokes the
// notifiers it is given. Before the wiring fix, main.go constructed the stream
// evaluator with nil notifiers, so reactive alerts were never delivered.
func TestWatchStreamEvaluator_NotifiesOnAlert(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	insertTestLogs(t, logStore, "web", 5, "info")
	insertTestLogs(t, logStore, "web", 5, "error")

	metrics := NewWatchMetrics(logStore)
	evaluator := NewWatchEvaluator(metrics, watchStore)
	notifier := &mockNotifier{}

	if _, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "web"),
		Service:        "web",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	stream := NewWatchStreamEvaluator(ctx, watchStore, evaluator, nil, []WatchAlertNotifier{notifier})
	stream.evaluateMatching(map[string]bool{"web": true})
	stream.notify.wait()

	if notifier.getCalls() != 1 {
		t.Errorf("stream notifier calls = %d, want 1", notifier.getCalls())
	}
}

// stubSender is a test double for the message sender used by ChatWatchNotifier.
type stubSender struct {
	mu   sync.Mutex
	msgs []string
	err  error
}

func (s *stubSender) Send(_ context.Context, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, message)
	return s.err
}

func (s *stubSender) last() (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.msgs) == 0 {
		return "", 0
	}
	return s.msgs[len(s.msgs)-1], len(s.msgs)
}

// TestChatWatchNotifier_SendsFormattedMessage proves the Telegram adapter
// (previously never instantiated in the wiring) renders a watch alert and hands
// it to the sender. Uses a stub — no real Telegram API call.
func TestChatWatchNotifier_SendsFormattedMessage(t *testing.T) {
	stub := &stubSender{}
	n := NewChatWatchNotifier(stub)

	if err := n.NotifyWatchAlert(context.Background(), testAlert(), testWatch()); err != nil {
		t.Fatalf("NotifyWatchAlert: %v", err)
	}

	msg, calls := stub.last()
	if calls != 1 {
		t.Fatalf("sender calls = %d, want 1", calls)
	}
	if !strings.Contains(msg, "error_rate") {
		t.Errorf("expected metric in message, got %q", msg)
	}
	if !strings.Contains(msg, testAlert().Summary) {
		t.Errorf("expected summary in message, got %q", msg)
	}
}

// TestNewChatWatchNotifier_NilSender guards the graceful "not configured" path.
func TestNewChatWatchNotifier_NilSender(t *testing.T) {
	n := NewChatWatchNotifier(nil)
	// A nil-backed notifier must be a safe no-op, never a panic.
	if err := n.NotifyWatchAlert(context.Background(), testAlert(), testWatch()); err != nil {
		t.Errorf("expected nil-backed notifier to no-op, got %v", err)
	}
}
