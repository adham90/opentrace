package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// testAlert returns a sample WatchAlert for notification tests.
func testAlert() *store.WatchAlert {
	return &store.WatchAlert{
		ID:             "alert-1",
		WatchID:        "watch-1",
		RunID:          "run-1",
		Urgency:        store.WatchUrgencyHigh,
		Summary:        "error_rate = 0.35 exceeds threshold 0.10",
		ConditionsSnapshot: store.BuildConditionsSnapshot("error_rate", 0.35, 0.10),
		Status:         "pending",
		CreatedAt:      time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC),
	}
}

// testWatch returns a sample Watch for notification tests.
func testWatch() *store.Watch {
	return &store.Watch{
		ID:             "watch-1",
		ConditionsJSON: json.RawMessage(`{"type":"threshold","metric":"error_rate","op":"gt","value":0.10,"service":"web"}`),
		Service:        "web",
	}
}

func TestWatchWebhookNotifier_Success(t *testing.T) {
	var receivedBody []byte
	var receivedContentType string
	var receivedEvent string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		receivedEvent = r.Header.Get("X-OpenTrace-Event")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		receivedBody = body
		w.WriteHeader(200)
	}))
	defer ts.Close()

	notifier := &WatchWebhookNotifier{
		URL:    ts.URL,
		Client: &http.Client{Timeout: 5 * time.Second},
	}

	alert := testAlert()
	watch := testWatch()

	err := notifier.NotifyWatchAlert(context.Background(), alert, watch)
	if err != nil {
		t.Fatalf("NotifyWatchAlert: %v", err)
	}

	// Verify headers
	if receivedContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", receivedContentType)
	}
	if receivedEvent != "watch.alert" {
		t.Errorf("X-OpenTrace-Event = %q, want watch.alert", receivedEvent)
	}

	// Verify payload fields
	var payload WatchWebhookPayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("unmarshaling payload: %v", err)
	}
	if payload.AlertID != "alert-1" {
		t.Errorf("alert_id = %q, want alert-1", payload.AlertID)
	}
	if payload.WatchID != "watch-1" {
		t.Errorf("watch_id = %q, want watch-1", payload.WatchID)
	}
	if payload.Metric != "error_rate" {
		t.Errorf("metric = %q, want error_rate", payload.Metric)
	}
	if payload.Urgency != "high" {
		t.Errorf("urgency = %q, want high", payload.Urgency)
	}
	if payload.TriggerValue != 0.35 {
		t.Errorf("trigger_value = %v, want 0.35", payload.TriggerValue)
	}
	if payload.ThresholdValue != 0.10 {
		t.Errorf("threshold_value = %v, want 0.10", payload.ThresholdValue)
	}
	if payload.Service != "web" {
		t.Errorf("service = %q, want web", payload.Service)
	}
	if payload.Timestamp != "2026-02-26T12:00:00Z" {
		t.Errorf("timestamp = %q, want 2026-02-26T12:00:00Z", payload.Timestamp)
	}
}

func TestWatchWebhookNotifier_Retry5xx(t *testing.T) {
	var count atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if count.Add(1) <= 2 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	notifier := &WatchWebhookNotifier{
		URL:    ts.URL,
		Client: &http.Client{Timeout: 5 * time.Second},
	}

	// The retry.Do uses BaseDelay of 1s by default; we override by calling directly
	// but the notifier hardcodes the retry config. For fast tests, we accept the
	// built-in retry config. Instead, we use a short-delay approach by constructing
	// a custom context and verifying the outcome.
	//
	// Since the notifier uses retry.Config{BaseDelay: 1s}, we need this test to be
	// patient. However, the retry delays are 1s then 2s = 3s total for 3 attempts.
	// To keep it fast, we'll create a custom notifier that calls the webhook logic
	// with the same retry behavior but shorter delays. Since we can't override the
	// internal retry config, we test via the lower-level behavior.
	//
	// Actually, looking at the code again, the retry config is hardcoded inside
	// NotifyWatchAlert. The cleanest approach for a fast test is to call the
	// webhook endpoint directly with our own retry.Do. But for a true integration
	// test of the actual notifier, we just need to wait.
	//
	// For practical test speed, let's just verify it works. 3 attempts with 1s + 2s
	// delay is about 3 seconds. That's acceptable for a unit test.

	err := notifier.NotifyWatchAlert(context.Background(), testAlert(), testWatch())
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}

	total := count.Load()
	if total != 3 {
		t.Errorf("expected 3 requests (2 failures + 1 success), got %d", total)
	}
}

func TestWatchWebhookNotifier_NoRetry4xx(t *testing.T) {
	var count atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(400)
	}))
	defer ts.Close()

	notifier := &WatchWebhookNotifier{
		URL:    ts.URL,
		Client: &http.Client{Timeout: 5 * time.Second},
	}

	err := notifier.NotifyWatchAlert(context.Background(), testAlert(), testWatch())
	if err == nil {
		t.Fatal("expected error for 400 response")
	}

	total := count.Load()
	if total != 1 {
		t.Errorf("expected exactly 1 request (no retry on 4xx), got %d", total)
	}
}

func TestWatchLogNotifier_AlwaysSucceeds(t *testing.T) {
	notifier := &WatchLogNotifier{}
	err := notifier.NotifyWatchAlert(context.Background(), testAlert(), testWatch())
	if err != nil {
		t.Errorf("expected nil error from log notifier, got: %v", err)
	}
}

// mockNotifier is a test double for WatchAlertNotifier.
type mockNotifier struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (m *mockNotifier) NotifyWatchAlert(_ context.Context, alert *store.WatchAlert, watch *store.Watch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	return m.err
}

func (m *mockNotifier) getCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func TestNotifyAllWatchAlert_ConcurrentDispatch(t *testing.T) {
	n1 := &mockNotifier{}
	n2 := &mockNotifier{}
	n3 := &mockNotifier{}

	notifiers := []WatchAlertNotifier{n1, n2, n3}
	NotifyAllWatchAlert(context.Background(), notifiers, testAlert(), testWatch())

	if n1.getCalls() != 1 {
		t.Errorf("notifier 1 calls = %d, want 1", n1.getCalls())
	}
	if n2.getCalls() != 1 {
		t.Errorf("notifier 2 calls = %d, want 1", n2.getCalls())
	}
	if n3.getCalls() != 1 {
		t.Errorf("notifier 3 calls = %d, want 1", n3.getCalls())
	}
}

func TestNotifyAllWatchAlert_ErrorDoesNotBlock(t *testing.T) {
	failing := &mockNotifier{err: fmt.Errorf("webhook down")}
	succeeding := &mockNotifier{}

	notifiers := []WatchAlertNotifier{failing, succeeding}
	NotifyAllWatchAlert(context.Background(), notifiers, testAlert(), testWatch())

	if failing.getCalls() != 1 {
		t.Errorf("failing notifier calls = %d, want 1", failing.getCalls())
	}
	if succeeding.getCalls() != 1 {
		t.Errorf("succeeding notifier calls = %d, want 1", succeeding.getCalls())
	}
}

func TestNotifyAllWatchAlert_EmptyNotifiers(t *testing.T) {
	// Should not panic with empty list
	NotifyAllWatchAlert(context.Background(), nil, testAlert(), testWatch())
	NotifyAllWatchAlert(context.Background(), []WatchAlertNotifier{}, testAlert(), testWatch())
}

func TestWatchWebhookNotifier_ContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer ts.Close()

	notifier := &WatchWebhookNotifier{
		URL:    ts.URL,
		Client: &http.Client{Timeout: 5 * time.Second},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := notifier.NotifyWatchAlert(ctx, testAlert(), testWatch())
	if err == nil {
		t.Error("expected error when context is cancelled")
	}
}
