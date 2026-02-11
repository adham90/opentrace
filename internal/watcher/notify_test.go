package watcher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

func TestDashboardNotifier(t *testing.T) {
	n := &DashboardNotifier{}
	err := n.Send(context.Background(), store.Alert{
		ID:      uuid.New(),
		Title:   "Test alert",
		Summary: "Test summary",
	})
	if err != nil {
		t.Fatalf("DashboardNotifier.Send: %v", err)
	}
}

func TestWebhookNotifier(t *testing.T) {
	var received WebhookPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %s, want application/json", ct)
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := NewWebhookNotifier(server.URL)
	alertID := uuid.New()
	now := time.Now()

	err := n.Send(context.Background(), store.Alert{
		ID:        alertID,
		Title:     "Payment spike",
		Summary:   "Found 47 errors",
		Severity:  store.SeverityCritical,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("WebhookNotifier.Send: %v", err)
	}

	if received.AlertID != alertID.String() {
		t.Errorf("alert_id = %s, want %s", received.AlertID, alertID.String())
	}
	if received.WatcherTitle != "Payment spike" {
		t.Errorf("watcher_title = %s, want Payment spike", received.WatcherTitle)
	}
	if received.Severity != "critical" {
		t.Errorf("severity = %s, want critical", received.Severity)
	}
	if received.Summary != "Found 47 errors" {
		t.Errorf("summary = %s, want Found 47 errors", received.Summary)
	}
}

func TestWebhookNotifier_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	n := NewWebhookNotifier(server.URL)
	err := n.Send(context.Background(), store.Alert{
		ID:    uuid.New(),
		Title: "Test",
	})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestParseNotifiers_DashboardOnly(t *testing.T) {
	raw := json.RawMessage(`["dashboard"]`)
	notifiers := ParseNotifiers(raw)
	if len(notifiers) != 1 {
		t.Fatalf("len = %d, want 1", len(notifiers))
	}
	if _, ok := notifiers[0].(*DashboardNotifier); !ok {
		t.Error("expected DashboardNotifier")
	}
}

func TestParseNotifiers_WithWebhook(t *testing.T) {
	raw := json.RawMessage(`["dashboard", {"type":"webhook","url":"https://hooks.example.com/test"}]`)
	notifiers := ParseNotifiers(raw)
	if len(notifiers) != 2 {
		t.Fatalf("len = %d, want 2", len(notifiers))
	}
	if _, ok := notifiers[0].(*DashboardNotifier); !ok {
		t.Error("expected DashboardNotifier first")
	}
	wh, ok := notifiers[1].(*WebhookNotifier)
	if !ok {
		t.Fatal("expected WebhookNotifier second")
	}
	if wh.URL != "https://hooks.example.com/test" {
		t.Errorf("webhook URL = %s, want https://hooks.example.com/test", wh.URL)
	}
}

func TestParseNotifiers_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`invalid`)
	notifiers := ParseNotifiers(raw)
	if len(notifiers) != 1 {
		t.Fatalf("len = %d, want 1 (default dashboard)", len(notifiers))
	}
}

func TestParseNotifiers_Empty(t *testing.T) {
	raw := json.RawMessage(`[]`)
	notifiers := ParseNotifiers(raw)
	if len(notifiers) != 1 {
		t.Fatalf("len = %d, want 1 (default dashboard)", len(notifiers))
	}
}

func TestSendAll(t *testing.T) {
	var called atomic.Int32
	mock := &mockNotifier{onSend: func() { called.Add(1) }}

	alert := store.Alert{
		ID:    uuid.New(),
		Title: "Test",
	}

	SendAll(context.Background(), []Notifier{mock, mock, mock}, alert)

	// SendAll is async — wait briefly for goroutines to complete
	deadline := time.After(2 * time.Second)
	for called.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("called = %d, want 3 (timed out)", called.Load())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got := called.Load(); got != 3 {
		t.Errorf("called = %d, want 3", got)
	}
}

type mockNotifier struct {
	onSend func()
}

func (m *mockNotifier) Send(ctx context.Context, alert store.Alert) error {
	if m.onSend != nil {
		m.onSend()
	}
	return nil
}
