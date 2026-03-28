package notifications

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Mock: Sender
// ---------------------------------------------------------------------------

type mockSender struct {
	calls []struct {
		method string
		params map[string]any
	}
}

func (m *mockSender) SendNotificationToAllClients(method string, params map[string]any) {
	m.calls = append(m.calls, struct {
		method string
		params map[string]any
	}{method, params})
}

// Compile-time check.
var _ Sender = (*mockSender)(nil)

// ---------------------------------------------------------------------------
// Tests: NewDispatcher
// ---------------------------------------------------------------------------

func TestNewDispatcher(t *testing.T) {
	d := NewDispatcher(&mockSender{})
	if d == nil {
		t.Fatal("NewDispatcher returned nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: Dispatcher.Notify
// ---------------------------------------------------------------------------

func TestDispatcher_Notify_NilSender(t *testing.T) {
	d := NewDispatcher(nil)
	// Must not panic.
	d.Notify(Notification{
		Type:     NotifyNewErrorGroup,
		Severity: "error",
		Title:    "test",
		Summary:  "should not panic",
	})
}

func TestDispatcher_Notify_CallsSender(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)

	n := Notification{
		Type:     NotifyHealthCheckDown,
		Severity: "critical",
		Title:    "DB is down",
		Summary:  "Primary database unreachable",
	}

	d.Notify(n)

	if len(ms.calls) != 1 {
		t.Fatalf("expected 1 SendNotificationToAllClients call, got %d", len(ms.calls))
	}

	call := ms.calls[0]
	if call.method != MCPMethod {
		t.Errorf("method = %q, want %q", call.method, MCPMethod)
	}
	if call.params["type"] != string(NotifyHealthCheckDown) {
		t.Errorf("params[type] = %v, want %q", call.params["type"], NotifyHealthCheckDown)
	}
	if call.params["severity"] != "critical" {
		t.Errorf("params[severity] = %v, want %q", call.params["severity"], "critical")
	}
	if call.params["title"] != "DB is down" {
		t.Errorf("params[title] = %v, want %q", call.params["title"], "DB is down")
	}
	if call.params["summary"] != "Primary database unreachable" {
		t.Errorf("params[summary] = %v, want %q", call.params["summary"], "Primary database unreachable")
	}
}

func TestDispatcher_Notify_WithContext(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)

	ctx := map[string]any{
		"fingerprint": "abc123",
		"count":       42,
	}

	d.Notify(Notification{
		Type:     NotifyNewErrorGroup,
		Severity: "warn",
		Title:    "New error group",
		Summary:  "Seen 42 times",
		Context:  ctx,
	})

	if len(ms.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(ms.calls))
	}

	params := ms.calls[0].params
	ctxVal, ok := params["context"]
	if !ok {
		t.Fatal("params should include 'context' key when Context is non-nil")
	}
	ctxMap, ok := ctxVal.(map[string]any)
	if !ok {
		t.Fatalf("context value should be map[string]any, got %T", ctxVal)
	}
	if ctxMap["fingerprint"] != "abc123" {
		t.Errorf("context[fingerprint] = %v, want %q", ctxMap["fingerprint"], "abc123")
	}
	if ctxMap["count"] != 42 {
		t.Errorf("context[count] = %v, want %d", ctxMap["count"], 42)
	}
}

func TestDispatcher_Notify_WithoutContext(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)

	d.Notify(Notification{
		Type:     NotifyWatcherTriggered,
		Severity: "info",
		Title:    "Watcher fired",
		Summary:  "Error rate above threshold",
		Context:  nil,
	})

	if len(ms.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(ms.calls))
	}

	params := ms.calls[0].params
	if _, ok := params["context"]; ok {
		t.Error("params should NOT include 'context' key when Context is nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: NewMCPServerSender
// ---------------------------------------------------------------------------

func TestNewMCPServerSender(t *testing.T) {
	s := NewMCPServerSender(nil)
	if s == nil {
		t.Fatal("NewMCPServerSender returned nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: MCPServerSender.SendNotificationToAllClients with nil Server
// ---------------------------------------------------------------------------

func TestMCPServerSender_NilServer(t *testing.T) {
	s := &MCPServerSender{Server: nil}
	// Must not panic.
	s.SendNotificationToAllClients("test/method", map[string]any{"key": "value"})
}
