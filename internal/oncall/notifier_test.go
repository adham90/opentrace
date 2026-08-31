package oncall

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/healthcheck"
	"github.com/adham90/opentrace/pkg/store"
)

func TestWatchNotifierRunsAgent(t *testing.T) {
	fe := &fakeExec{out: "diagnosis"}
	r := newTestRunner(t, testConfig(), fe)
	n := &WatchNotifier{Runner: r}

	alert := &store.WatchAlert{ID: "a1", WatchID: "w1", Summary: "error rate high", Environment: "production"}
	watch := &store.Watch{Environment: "production"}
	if err := n.NotifyWatchAlert(context.Background(), alert, watch); err != nil {
		t.Fatalf("NotifyWatchAlert: %v", err)
	}
	if fe.calls != 1 {
		t.Errorf("agent ran %d times, want 1", fe.calls)
	}
}

// A failed diagnosis must not look like a failed alert — the chat and webhook
// notifiers have already delivered by the time this runs.
func TestWatchNotifierSwallowsAgentFailure(t *testing.T) {
	fe := &fakeExec{err: context.DeadlineExceeded}
	r := newTestRunner(t, testConfig(), fe)
	n := &WatchNotifier{Runner: r}

	if err := n.NotifyWatchAlert(context.Background(), &store.WatchAlert{ID: "a1"}, nil); err != nil {
		t.Fatalf("NotifyWatchAlert returned %v, want nil", err)
	}
}

func TestWatchNotifierNilRunnerIsNoop(t *testing.T) {
	n := &WatchNotifier{}
	if err := n.NotifyWatchAlert(context.Background(), &store.WatchAlert{ID: "a1"}, nil); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

func TestHealthCheckNotifierRunsOnDown(t *testing.T) {
	fe := &fakeExec{out: "diagnosis"}
	r := newTestRunner(t, testConfig(), fe)
	n := &HealthCheckNotifier{Runner: r}

	alert := &healthcheck.HealthCheckAlert{
		HealthCheckID:   "hc1",
		HealthCheckName: "checkout",
		CurrentStatus:   store.HealthCheckDown,
		Timestamp:       time.Now(),
	}
	if err := n.NotifyHealthCheckAlert(context.Background(), alert); err != nil {
		t.Fatalf("NotifyHealthCheckAlert: %v", err)
	}
	if fe.calls != 1 {
		t.Errorf("agent ran %d times, want 1", fe.calls)
	}
}

// Recovery is good news. Triaging it is how a flapping endpoint burns the
// day's quota by lunchtime.
func TestHealthCheckNotifierSkipsRecovery(t *testing.T) {
	fe := &fakeExec{out: "diagnosis"}
	r := newTestRunner(t, testConfig(), fe)
	n := &HealthCheckNotifier{Runner: r}

	alert := &healthcheck.HealthCheckAlert{
		HealthCheckID:  "hc1",
		PreviousStatus: store.HealthCheckDown,
		CurrentStatus:  store.HealthCheckUp,
		Timestamp:      time.Now(),
	}
	if err := n.NotifyHealthCheckAlert(context.Background(), alert); err != nil {
		t.Fatalf("NotifyHealthCheckAlert: %v", err)
	}
	if fe.calls != 0 {
		t.Errorf("agent ran on recovery (%d calls)", fe.calls)
	}
}
