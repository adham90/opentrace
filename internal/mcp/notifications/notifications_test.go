package notifications

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
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

// ===========================================================================
// Mock: LogStore (for DeployWatcher)
// ===========================================================================

type mockLogStoreForDeploy struct {
	store.LogStore // embed for unused methods
	countByLevel   map[string]int
	countErr       error
}

func (m *mockLogStoreForDeploy) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) {
	if m.countErr != nil {
		return nil, m.countErr
	}
	return m.countByLevel, nil
}

// ===========================================================================
// Mock: ErrorGroupStore (for DeployWatcher)
// ===========================================================================

type mockErrorGroupStoreForDeploy struct {
	store.ErrorGroupStore // embed for unused methods
	groups                []store.ErrorGroup
	listErr               error
}

func (m *mockErrorGroupStoreForDeploy) List(_ context.Context, _ store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.groups, nil
}

// ===========================================================================
// Tests: ErrorWatcher
// ===========================================================================

func TestNewErrorWatcher(t *testing.T) {
	d := NewDispatcher(&mockSender{})
	w := NewErrorWatcher(d)
	if w == nil {
		t.Fatal("NewErrorWatcher returned nil")
	}
}

func TestErrorWatcher_OnError_NilDispatcher(t *testing.T) {
	w := NewErrorWatcher(nil)
	// Must not panic with nil dispatcher.
	w.OnError(ErrorEvent{
		Fingerprint:    "fp1",
		ExceptionClass: "RuntimeError",
		Message:        "boom",
		Service:        "api",
	})
}

func TestErrorWatcher_FirstTwoOccurrences_NoNotification(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)
	w := NewErrorWatcher(d)

	for i := 0; i < 2; i++ {
		w.OnError(ErrorEvent{
			Fingerprint:    "fp-abc",
			ExceptionClass: "RuntimeError",
			Message:        "something broke",
			Service:        "api",
		})
	}

	if len(ms.calls) != 0 {
		t.Fatalf("expected 0 notifications after 2 occurrences, got %d", len(ms.calls))
	}
}

func TestErrorWatcher_ThirdOccurrence_Notification(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)
	w := NewErrorWatcher(d)

	for i := 0; i < 3; i++ {
		w.OnError(ErrorEvent{
			Fingerprint:    "fp-abc",
			ExceptionClass: "RuntimeError",
			Message:        "something broke",
			Service:        "api",
		})
	}

	if len(ms.calls) != 1 {
		t.Fatalf("expected 1 notification after 3 occurrences, got %d", len(ms.calls))
	}

	params := ms.calls[0].params
	if params["type"] != string(NotifyNewErrorGroup) {
		t.Errorf("type = %v, want %q", params["type"], NotifyNewErrorGroup)
	}
	if params["severity"] != "error" {
		t.Errorf("severity = %v, want %q", params["severity"], "error")
	}
	title, _ := params["title"].(string)
	if !strings.Contains(title, "RuntimeError") {
		t.Errorf("title should contain exception class, got %q", title)
	}
}

func TestErrorWatcher_CooldownPreventsReNotification(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)
	w := NewErrorWatcher(d)

	// Trigger first notification (3 occurrences).
	for i := 0; i < 3; i++ {
		w.OnError(ErrorEvent{
			Fingerprint:    "fp-cool",
			ExceptionClass: "TimeoutError",
			Message:        "timed out",
			Service:        "web",
		})
	}
	if len(ms.calls) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(ms.calls))
	}

	// Additional occurrences within cooldown period should NOT fire again.
	for i := 0; i < 5; i++ {
		w.OnError(ErrorEvent{
			Fingerprint:    "fp-cool",
			ExceptionClass: "TimeoutError",
			Message:        "timed out",
			Service:        "web",
		})
	}
	if len(ms.calls) != 1 {
		t.Fatalf("expected still 1 notification during cooldown, got %d", len(ms.calls))
	}
}

func TestErrorWatcher_MessageTruncation(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)
	w := NewErrorWatcher(d)

	longMsg := strings.Repeat("x", 200)

	for i := 0; i < 3; i++ {
		w.OnError(ErrorEvent{
			Fingerprint:    "fp-long",
			ExceptionClass: "LongError",
			Message:        longMsg,
			Service:        "api",
		})
	}

	if len(ms.calls) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(ms.calls))
	}

	summary, _ := ms.calls[0].params["summary"].(string)
	// The truncated message (120 chars + "...") should appear in the summary.
	if !strings.Contains(summary, "...") {
		t.Errorf("summary should contain truncated message with '...', got %q", summary)
	}
	// The full 200-char message should NOT appear.
	if strings.Contains(summary, longMsg) {
		t.Error("summary should NOT contain the full 200-char message")
	}
}

func TestErrorWatcher_DifferentFingerprints_Independent(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)
	w := NewErrorWatcher(d)

	// 2 occurrences of fp-a (not enough to notify)
	for i := 0; i < 2; i++ {
		w.OnError(ErrorEvent{Fingerprint: "fp-a", ExceptionClass: "ErrA", Message: "a", Service: "svc"})
	}
	// 3 occurrences of fp-b (enough to notify)
	for i := 0; i < 3; i++ {
		w.OnError(ErrorEvent{Fingerprint: "fp-b", ExceptionClass: "ErrB", Message: "b", Service: "svc"})
	}

	if len(ms.calls) != 1 {
		t.Fatalf("expected 1 notification (only fp-b), got %d", len(ms.calls))
	}

	title, _ := ms.calls[0].params["title"].(string)
	if !strings.Contains(title, "ErrB") {
		t.Errorf("notification should be for ErrB, got title %q", title)
	}
}

// ===========================================================================
// Tests: HealthWatcher
// ===========================================================================

func TestNewHealthWatcher(t *testing.T) {
	d := NewDispatcher(&mockSender{})
	w := NewHealthWatcher(d)
	if w == nil {
		t.Fatal("NewHealthWatcher returned nil")
	}
}

func TestHealthWatcher_OnHealthCheckResult_NilDispatcher(t *testing.T) {
	w := NewHealthWatcher(nil)
	// Must not panic.
	w.OnHealthCheckResult(HealthCheckEvent{
		CheckID: "chk-1",
		Name:    "DB",
		URL:     "http://localhost/health",
		Status:  "down",
	})
}

func TestHealthWatcher_FirstDownEvent_Notification(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)
	w := NewHealthWatcher(d)

	w.OnHealthCheckResult(HealthCheckEvent{
		CheckID:    "chk-1",
		Name:       "Database",
		URL:        "http://db:5432/health",
		Status:     "down",
		StatusCode: 500,
	})

	if len(ms.calls) != 1 {
		t.Fatalf("expected 1 notification on first DOWN, got %d", len(ms.calls))
	}

	params := ms.calls[0].params
	if params["type"] != string(NotifyHealthCheckDown) {
		t.Errorf("type = %v, want %q", params["type"], NotifyHealthCheckDown)
	}
	if params["severity"] != "critical" {
		t.Errorf("severity = %v, want %q", params["severity"], "critical")
	}
	title, _ := params["title"].(string)
	if !strings.Contains(title, "Database") {
		t.Errorf("title should contain check name, got %q", title)
	}
	summary, _ := params["summary"].(string)
	if !strings.Contains(summary, "http://db:5432/health") {
		t.Errorf("summary should contain URL, got %q", summary)
	}
}

func TestHealthWatcher_UpToDown_Notification(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)
	w := NewHealthWatcher(d)

	// First event: UP
	w.OnHealthCheckResult(HealthCheckEvent{
		CheckID: "chk-1", Name: "API", URL: "http://api/health",
		Status: "up", StatusCode: 200,
	})
	if len(ms.calls) != 0 {
		t.Fatalf("expected 0 notifications on UP, got %d", len(ms.calls))
	}

	// Second event: DOWN
	w.OnHealthCheckResult(HealthCheckEvent{
		CheckID: "chk-1", Name: "API", URL: "http://api/health",
		Status: "down", StatusCode: 503,
	})
	if len(ms.calls) != 1 {
		t.Fatalf("expected 1 notification on UP→DOWN, got %d", len(ms.calls))
	}
}

func TestHealthWatcher_DownToDown_NoNotification(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)
	w := NewHealthWatcher(d)

	// First DOWN (no previous state → notification fires)
	w.OnHealthCheckResult(HealthCheckEvent{
		CheckID: "chk-1", Name: "API", URL: "http://api/health",
		Status: "down", StatusCode: 503,
	})
	if len(ms.calls) != 1 {
		t.Fatalf("expected 1 notification on first DOWN, got %d", len(ms.calls))
	}

	// Second DOWN (same status → no new notification)
	w.OnHealthCheckResult(HealthCheckEvent{
		CheckID: "chk-1", Name: "API", URL: "http://api/health",
		Status: "down", StatusCode: 503,
	})
	if len(ms.calls) != 1 {
		t.Fatalf("expected still 1 notification on DOWN→DOWN, got %d", len(ms.calls))
	}
}

func TestHealthWatcher_UpToUp_NoNotification(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)
	w := NewHealthWatcher(d)

	w.OnHealthCheckResult(HealthCheckEvent{
		CheckID: "chk-1", Name: "API", URL: "http://api/health",
		Status: "up", StatusCode: 200,
	})
	w.OnHealthCheckResult(HealthCheckEvent{
		CheckID: "chk-1", Name: "API", URL: "http://api/health",
		Status: "up", StatusCode: 200,
	})

	if len(ms.calls) != 0 {
		t.Fatalf("expected 0 notifications on UP→UP, got %d", len(ms.calls))
	}
}

func TestHealthWatcher_DownToUp_NoNotification(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)
	w := NewHealthWatcher(d)

	// DOWN first (triggers notification)
	w.OnHealthCheckResult(HealthCheckEvent{
		CheckID: "chk-1", Name: "API", URL: "http://api/health",
		Status: "down", StatusCode: 503,
	})
	// UP recovery
	w.OnHealthCheckResult(HealthCheckEvent{
		CheckID: "chk-1", Name: "API", URL: "http://api/health",
		Status: "up", StatusCode: 200,
	})

	// Only the first DOWN should have notified.
	if len(ms.calls) != 1 {
		t.Fatalf("expected 1 notification total (only DOWN), got %d", len(ms.calls))
	}
}

func TestHealthWatcher_ErrorMessage_UsedInSummary(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)
	w := NewHealthWatcher(d)

	w.OnHealthCheckResult(HealthCheckEvent{
		CheckID: "chk-1", Name: "DB", URL: "http://db/health",
		Status: "down", StatusCode: 0, ErrorMessage: "connection refused",
	})

	summary, _ := ms.calls[0].params["summary"].(string)
	if !strings.Contains(summary, "connection refused") {
		t.Errorf("summary should contain error message, got %q", summary)
	}
}

func TestHealthWatcher_NoErrorMessage_UsesHTTPCode(t *testing.T) {
	ms := &mockSender{}
	d := NewDispatcher(ms)
	w := NewHealthWatcher(d)

	w.OnHealthCheckResult(HealthCheckEvent{
		CheckID: "chk-1", Name: "DB", URL: "http://db/health",
		Status: "down", StatusCode: 502, ErrorMessage: "",
	})

	summary, _ := ms.calls[0].params["summary"].(string)
	if !strings.Contains(summary, "HTTP 502") {
		t.Errorf("summary should contain 'HTTP 502' when no error message, got %q", summary)
	}
}

// ===========================================================================
// Tests: DeployWatcher
// ===========================================================================

func TestNewDeployWatcher(t *testing.T) {
	d := NewDispatcher(&mockSender{})
	w := NewDeployWatcher(d, &mockErrorGroupStoreForDeploy{}, &mockLogStoreForDeploy{})
	if w == nil {
		t.Fatal("NewDeployWatcher returned nil")
	}
}

func TestDeployWatcher_OnDeploy_NilLogStore(t *testing.T) {
	d := NewDispatcher(&mockSender{})
	w := NewDeployWatcher(d, &mockErrorGroupStoreForDeploy{}, nil)
	// Must not panic.
	w.OnDeploy(context.Background(), store.Deploy{
		Service:    "api",
		DeployedAt: time.Now(),
	})
}

func TestDeployWatcher_OnDeploy_NilDispatcher(t *testing.T) {
	w := NewDeployWatcher(nil, &mockErrorGroupStoreForDeploy{}, &mockLogStoreForDeploy{})
	// Must not panic.
	w.OnDeploy(context.Background(), store.Deploy{
		Service:    "api",
		DeployedAt: time.Now(),
	})
}

func TestDeployWatcher_CountErrors_NilLogStore(t *testing.T) {
	d := NewDispatcher(&mockSender{})
	w := NewDeployWatcher(d, nil, nil)

	now := time.Now()
	result := w.countErrors(context.Background(), "api", now.Add(-30*time.Minute), now)
	if result != 0 {
		t.Fatalf("expected 0 errors with nil logStore, got %d", result)
	}
}

func TestDeployWatcher_CountErrors_SumsErrorAndFatal(t *testing.T) {
	ls := &mockLogStoreForDeploy{
		countByLevel: map[string]int{
			"debug": 100,
			"info":  50,
			"warn":  10,
			"error": 7,
			"fatal": 3,
		},
	}
	d := NewDispatcher(&mockSender{})
	w := NewDeployWatcher(d, nil, ls)

	now := time.Now()
	result := w.countErrors(context.Background(), "api", now.Add(-30*time.Minute), now)
	if result != 10 {
		t.Fatalf("expected 10 (7 error + 3 fatal), got %d", result)
	}
}

func TestDeployWatcher_CountErrors_StoreError_ReturnsZero(t *testing.T) {
	ls := &mockLogStoreForDeploy{
		countErr: fmt.Errorf("db connection lost"),
	}
	d := NewDispatcher(&mockSender{})
	w := NewDeployWatcher(d, nil, ls)

	now := time.Now()
	result := w.countErrors(context.Background(), "api", now.Add(-30*time.Minute), now)
	if result != 0 {
		t.Fatalf("expected 0 on store error, got %d", result)
	}
}

func TestDeployWatcher_CountErrors_NoErrorLevels(t *testing.T) {
	ls := &mockLogStoreForDeploy{
		countByLevel: map[string]int{
			"DEBUG": 100,
			"INFO":  50,
			"WARN":  10,
		},
	}
	d := NewDispatcher(&mockSender{})
	w := NewDeployWatcher(d, nil, ls)

	now := time.Now()
	result := w.countErrors(context.Background(), "api", now.Add(-30*time.Minute), now)
	if result != 0 {
		t.Fatalf("expected 0 when no ERROR/FATAL levels, got %d", result)
	}
}
