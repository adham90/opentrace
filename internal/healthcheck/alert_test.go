package healthcheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestHealthCheckLogNotifier_Success(t *testing.T) {
	n := &HealthCheckLogNotifier{}
	err := n.NotifyHealthCheckAlert(context.Background(), &HealthCheckAlert{
		HealthCheckID:   "hc1",
		HealthCheckName: "test",
		URL:             "http://example.com",
		PreviousStatus:  store.HealthCheckUp,
		CurrentStatus:   store.HealthCheckDown,
		Timestamp:       time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHealthCheckWebhookNotifier_Success(t *testing.T) {
	var gotPayload HealthCheckWebhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if ev := r.Header.Get("X-OpenTrace-Event"); ev != "healthcheck.status_change" {
			t.Errorf("X-OpenTrace-Event = %q, want healthcheck.status_change", ev)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Errorf("failed to decode payload: %v", err)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n := NewHealthCheckWebhookNotifier(srv.URL)
	code := 500
	respMs := 42
	alert := &HealthCheckAlert{
		HealthCheckID:   "hc1",
		HealthCheckName: "my-api",
		URL:             "http://api.example.com/health",
		PreviousStatus:  store.HealthCheckUp,
		CurrentStatus:   store.HealthCheckDown,
		StatusCode:      &code,
		ResponseMs:      &respMs,
		ErrorMessage:    "connection reset",
		Timestamp:       time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC),
	}
	err := n.NotifyHealthCheckAlert(context.Background(), alert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPayload.HealthCheckID != "hc1" {
		t.Errorf("HealthCheckID = %q, want hc1", gotPayload.HealthCheckID)
	}
	if gotPayload.PreviousStatus != "up" {
		t.Errorf("PreviousStatus = %q, want up", gotPayload.PreviousStatus)
	}
	if gotPayload.CurrentStatus != "down" {
		t.Errorf("CurrentStatus = %q, want down", gotPayload.CurrentStatus)
	}
	if gotPayload.ErrorMessage != "connection reset" {
		t.Errorf("ErrorMessage = %q, want 'connection reset'", gotPayload.ErrorMessage)
	}
}

func TestHealthCheckWebhookNotifier_ServerError_Retries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	n := NewHealthCheckWebhookNotifier(srv.URL)
	err := n.NotifyHealthCheckAlert(context.Background(), &HealthCheckAlert{
		HealthCheckName: "test",
		CurrentStatus:   store.HealthCheckDown,
		Timestamp:       time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for 500 responses")
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("call count = %d, want 3 (initial + 2 retries)", got)
	}
}

func TestHealthCheckWebhookNotifier_ClientError_NoPermanentRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(403)
	}))
	defer srv.Close()

	n := NewHealthCheckWebhookNotifier(srv.URL)
	err := n.NotifyHealthCheckAlert(context.Background(), &HealthCheckAlert{
		HealthCheckName: "test",
		CurrentStatus:   store.HealthCheckDown,
		Timestamp:       time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("call count = %d, want 1 (no retries on 4xx)", got)
	}
}

func TestNotifyAllHealthCheck_ConcurrentDispatch(t *testing.T) {
	var mu sync.Mutex
	var called []string

	n1 := &mockNotifier{fn: func(_ context.Context, a *HealthCheckAlert) error {
		mu.Lock()
		called = append(called, "n1")
		mu.Unlock()
		return nil
	}}
	n2 := &mockNotifier{fn: func(_ context.Context, a *HealthCheckAlert) error {
		mu.Lock()
		called = append(called, "n2")
		mu.Unlock()
		return nil
	}}

	alert := &HealthCheckAlert{
		HealthCheckName: "test",
		CurrentStatus:   store.HealthCheckDown,
		Timestamp:       time.Now(),
	}
	NotifyAllHealthCheck(context.Background(), []HealthCheckAlertNotifier{n1, n2}, alert)

	mu.Lock()
	defer mu.Unlock()
	if len(called) != 2 {
		t.Fatalf("expected 2 notifiers called, got %d", len(called))
	}
}

func TestNotifyAllHealthCheck_ErrorDoesNotBlock(t *testing.T) {
	errNotifier := &mockNotifier{fn: func(_ context.Context, a *HealthCheckAlert) error {
		return context.DeadlineExceeded
	}}
	okNotifier := &mockNotifier{fn: func(_ context.Context, a *HealthCheckAlert) error {
		return nil
	}}

	alert := &HealthCheckAlert{
		HealthCheckName: "test",
		CurrentStatus:   store.HealthCheckDown,
		Timestamp:       time.Now(),
	}
	// Should not panic or hang despite one notifier erroring
	NotifyAllHealthCheck(context.Background(), []HealthCheckAlertNotifier{errNotifier, okNotifier}, alert)

	if !errNotifier.wasCalled() {
		t.Error("expected error notifier to be called")
	}
	if !okNotifier.wasCalled() {
		t.Error("expected ok notifier to be called")
	}
}

// --- Scheduler transition detection tests ---

// mockHCStore implements store.HealthCheckStore for testing.
type mockHCStore struct {
	checks  []store.HealthCheck
	results []store.HealthCheckResult
	mu      sync.Mutex
}

func (m *mockHCStore) Create(_ context.Context, _ store.CreateHealthCheckParams) (*store.HealthCheck, error) {
	return nil, nil
}
func (m *mockHCStore) Get(_ context.Context, _ string) (*store.HealthCheck, error) {
	return nil, nil
}
func (m *mockHCStore) List(_ context.Context, _ store.ListHealthCheckParams) ([]store.HealthCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checks, nil
}
func (m *mockHCStore) Delete(_ context.Context, _ string) error { return nil }
func (m *mockHCStore) SetEnabled(_ context.Context, _ string, _ bool) error {
	return nil
}
func (m *mockHCStore) RecordResult(_ context.Context, result store.HealthCheckResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = append(m.results, result)
	return nil
}
func (m *mockHCStore) LatestResults(_ context.Context, _ string, _ int) ([]store.HealthCheckResult, error) {
	return nil, nil
}
func (m *mockHCStore) UptimeSummaries(_ context.Context, _ time.Time) ([]store.UptimeSummary, error) {
	return nil, nil
}
func (m *mockHCStore) PruneResults(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

// mockNotifier captures calls for testing.
type mockNotifier struct {
	fn     func(context.Context, *HealthCheckAlert) error
	alerts []*HealthCheckAlert
	mu     sync.Mutex
}

func (m *mockNotifier) NotifyHealthCheckAlert(ctx context.Context, alert *HealthCheckAlert) error {
	m.mu.Lock()
	m.alerts = append(m.alerts, alert)
	m.mu.Unlock()
	if m.fn != nil {
		return m.fn(ctx, alert)
	}
	return nil
}

func (m *mockNotifier) wasCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.alerts) > 0
}

func (m *mockNotifier) alertCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.alerts)
}

func (m *mockNotifier) getAlerts() []*HealthCheckAlert {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*HealthCheckAlert, len(m.alerts))
	copy(cp, m.alerts)
	return cp
}

func TestScheduler_DetectsStatusTransition(t *testing.T) {
	// Set up a test HTTP server that changes behavior
	var statusMu sync.Mutex
	returnStatus := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		statusMu.Lock()
		s := returnStatus
		statusMu.Unlock()
		w.WriteHeader(s)
	}))
	defer srv.Close()

	hc := store.HealthCheck{
		ID:             "hc-transition",
		Name:           "transition-test",
		URL:            srv.URL,
		Method:         "GET",
		IntervalSecs:   1,
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		Enabled:        true,
	}

	ms := &mockHCStore{checks: []store.HealthCheck{hc}}
	notifier := &mockNotifier{}

	s := NewScheduler(ms, 15*time.Second, notifier)

	ctx := context.Background()

	// First check: Up (no alert expected — first check, no previous status)
	s.runCheck(ctx, hc)
	if notifier.alertCount() != 0 {
		t.Fatalf("expected 0 alerts after first check, got %d", notifier.alertCount())
	}

	// Second check: still Up (no alert — same status)
	s.runCheck(ctx, hc)
	if notifier.alertCount() != 0 {
		t.Fatalf("expected 0 alerts after second Up check, got %d", notifier.alertCount())
	}

	// Third check: Down (alert expected — transition Up→Down)
	statusMu.Lock()
	returnStatus = 500
	statusMu.Unlock()

	s.runCheck(ctx, hc)
	if notifier.alertCount() != 1 {
		t.Fatalf("expected 1 alert after Up→Down transition, got %d", notifier.alertCount())
	}

	alerts := notifier.getAlerts()
	if alerts[0].PreviousStatus != store.HealthCheckUp {
		t.Errorf("PreviousStatus = %q, want up", alerts[0].PreviousStatus)
	}
	if alerts[0].CurrentStatus != store.HealthCheckDown {
		t.Errorf("CurrentStatus = %q, want down", alerts[0].CurrentStatus)
	}
	if alerts[0].HealthCheckID != "hc-transition" {
		t.Errorf("HealthCheckID = %q, want hc-transition", alerts[0].HealthCheckID)
	}
	if alerts[0].HealthCheckName != "transition-test" {
		t.Errorf("HealthCheckName = %q, want transition-test", alerts[0].HealthCheckName)
	}

	// Fourth check: back to Up (another alert — transition Down→Up)
	statusMu.Lock()
	returnStatus = 200
	statusMu.Unlock()

	s.runCheck(ctx, hc)
	if notifier.alertCount() != 2 {
		t.Fatalf("expected 2 alerts after Down→Up transition, got %d", notifier.alertCount())
	}

	alerts = notifier.getAlerts()
	if alerts[1].PreviousStatus != store.HealthCheckDown {
		t.Errorf("PreviousStatus = %q, want down", alerts[1].PreviousStatus)
	}
	if alerts[1].CurrentStatus != store.HealthCheckUp {
		t.Errorf("CurrentStatus = %q, want up", alerts[1].CurrentStatus)
	}
}

func TestScheduler_NoAlertOnSameStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	hc := store.HealthCheck{
		ID:             "hc-same",
		Name:           "same-status",
		URL:            srv.URL,
		Method:         "GET",
		IntervalSecs:   1,
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		Enabled:        true,
	}

	ms := &mockHCStore{checks: []store.HealthCheck{hc}}
	notifier := &mockNotifier{}

	s := NewScheduler(ms, 15*time.Second, notifier)
	ctx := context.Background()

	// Run multiple checks — all should return Up
	for i := 0; i < 5; i++ {
		s.runCheck(ctx, hc)
	}

	if notifier.alertCount() != 0 {
		t.Fatalf("expected 0 alerts for consistent status, got %d", notifier.alertCount())
	}
}

func TestScheduler_NoAlertOnFirstCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500) // Down from the start
	}))
	defer srv.Close()

	hc := store.HealthCheck{
		ID:             "hc-first",
		Name:           "first-check",
		URL:            srv.URL,
		Method:         "GET",
		IntervalSecs:   1,
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		Enabled:        true,
	}

	ms := &mockHCStore{checks: []store.HealthCheck{hc}}
	notifier := &mockNotifier{}

	s := NewScheduler(ms, 15*time.Second, notifier)
	ctx := context.Background()

	// First check — even though it's Down, no alert because no previous status
	s.runCheck(ctx, hc)

	if notifier.alertCount() != 0 {
		t.Fatalf("expected 0 alerts on first check, got %d", notifier.alertCount())
	}
}

func TestScheduler_DefaultLogNotifier(t *testing.T) {
	// NewScheduler with no notifiers should use a log notifier
	s := NewScheduler(nil, 15*time.Second)
	if len(s.notifiers) != 1 {
		t.Fatalf("expected 1 default notifier, got %d", len(s.notifiers))
	}
	if _, ok := s.notifiers[0].(*HealthCheckLogNotifier); !ok {
		t.Errorf("default notifier type = %T, want *HealthCheckLogNotifier", s.notifiers[0])
	}
}

func TestScheduler_AlertContainsResultFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	hc := store.HealthCheck{
		ID:             "hc-fields",
		Name:           "fields-test",
		URL:            srv.URL,
		Method:         "GET",
		IntervalSecs:   1,
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		Enabled:        true,
	}

	ms := &mockHCStore{checks: []store.HealthCheck{hc}}
	notifier := &mockNotifier{}

	s := NewScheduler(ms, 15*time.Second, notifier)
	ctx := context.Background()

	// Seed initial status as Up
	s.statusMu.Lock()
	s.lastStatus[hc.ID] = store.HealthCheckUp
	s.statusMu.Unlock()

	// Run check — should transition to Down and include status code
	s.runCheck(ctx, hc)

	if notifier.alertCount() != 1 {
		t.Fatalf("expected 1 alert, got %d", notifier.alertCount())
	}

	alert := notifier.getAlerts()[0]
	if alert.StatusCode == nil {
		t.Fatal("expected non-nil StatusCode")
	}
	if *alert.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want 500", *alert.StatusCode)
	}
	if alert.ResponseMs == nil {
		t.Fatal("expected non-nil ResponseMs")
	}
	if *alert.ResponseMs < 0 {
		t.Errorf("ResponseMs = %d, want >= 0", *alert.ResponseMs)
	}
	if alert.URL != srv.URL {
		t.Errorf("URL = %q, want %q", alert.URL, srv.URL)
	}
	if alert.Timestamp.IsZero() {
		t.Error("expected non-zero Timestamp")
	}
}
