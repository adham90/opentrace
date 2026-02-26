package healthcheck

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

func TestChecker_Up(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc1",
		URL:            srv.URL,
		Method:         "GET",
		TimeoutSecs:    5,
		ExpectedStatus: 200,
	}

	result := c.Check(context.Background(), hc)
	if result.Status != store.HealthCheckUp {
		t.Errorf("Status = %q, want up", result.Status)
	}
	if result.StatusCode == nil || *result.StatusCode != 200 {
		t.Errorf("StatusCode = %v, want 200", result.StatusCode)
	}
	if result.ResponseMs == nil || *result.ResponseMs < 0 {
		t.Error("expected non-negative ResponseMs")
	}
}

func TestChecker_Down_WrongStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc2",
		URL:            srv.URL,
		Method:         "GET",
		TimeoutSecs:    5,
		ExpectedStatus: 200,
	}

	result := c.Check(context.Background(), hc)
	if result.Status != store.HealthCheckDown {
		t.Errorf("Status = %q, want down", result.Status)
	}
}

func TestChecker_Down_ConnectionRefused(t *testing.T) {
	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc3",
		URL:            "http://127.0.0.1:1", // port 1 should refuse
		Method:         "GET",
		TimeoutSecs:    2,
		ExpectedStatus: 200,
	}

	result := c.Check(context.Background(), hc)
	if result.Status != store.HealthCheckDown {
		t.Errorf("Status = %q, want down", result.Status)
	}
	if result.Error == "" {
		t.Error("expected non-empty error for connection refused")
	}
}

func TestChecker_Degraded_WrongSuccessCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(301)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc4",
		URL:            srv.URL,
		Method:         "GET",
		TimeoutSecs:    5,
		ExpectedStatus: 200,
	}

	result := c.Check(context.Background(), hc)
	if result.Status != store.HealthCheckDegraded {
		t.Errorf("Status = %q, want degraded", result.Status)
	}
}

func TestChecker_HeadMethod(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(204)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc5",
		URL:            srv.URL,
		Method:         "HEAD",
		TimeoutSecs:    5,
		ExpectedStatus: 204,
	}

	result := c.Check(context.Background(), hc)
	if gotMethod != "HEAD" {
		t.Errorf("method sent = %q, want HEAD", gotMethod)
	}
	if result.Status != store.HealthCheckUp {
		t.Errorf("Status = %q, want up", result.Status)
	}
}

// --- Retry tests ---

func TestChecker_RetryOnFailure_SecondAttemptSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc-retry-success",
		URL:            srv.URL,
		Method:         "GET",
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		Retries:        2,
	}

	result := c.Check(context.Background(), hc)
	if result.Status != store.HealthCheckUp {
		t.Errorf("Status = %q, want up (retry should have succeeded)", result.Status)
	}
	if got := calls.Load(); got < 2 {
		t.Errorf("expected at least 2 calls, got %d", got)
	}
}

func TestChecker_RetryAllFail(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc-retry-fail",
		URL:            srv.URL,
		Method:         "GET",
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		Retries:        2,
	}

	result := c.Check(context.Background(), hc)
	if result.Status != store.HealthCheckDown {
		t.Errorf("Status = %q, want down (all retries failed)", result.Status)
	}
	// Should have called 1 original + 2 retries = 3 times
	if got := calls.Load(); got != 3 {
		t.Errorf("call count = %d, want 3", got)
	}
}

func TestChecker_NoRetries_Default(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc-no-retry",
		URL:            srv.URL,
		Method:         "GET",
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		// Retries not set — defaults to 0
	}

	result := c.Check(context.Background(), hc)
	if result.Status != store.HealthCheckDown {
		t.Errorf("Status = %q, want down", result.Status)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("call count = %d, want 1 (no retries)", got)
	}
}

// --- Response body matching tests ---

func TestChecker_BodyMatch_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprint(w, `{"status":"healthy","version":"1.0"}`)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc-body-ok",
		URL:            srv.URL,
		Method:         "GET",
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		ExpectedBody:   "healthy",
	}

	result := c.Check(context.Background(), hc)
	if result.Status != store.HealthCheckUp {
		t.Errorf("Status = %q, want up (body contains expected string)", result.Status)
	}
	if result.Error != "" {
		t.Errorf("Error = %q, want empty", result.Error)
	}
}

func TestChecker_BodyMatch_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprint(w, `{"status":"degraded","version":"1.0"}`)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc-body-fail",
		URL:            srv.URL,
		Method:         "GET",
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		ExpectedBody:   "healthy",
	}

	result := c.Check(context.Background(), hc)
	if result.Status != store.HealthCheckDown {
		t.Errorf("Status = %q, want down (body did not contain expected string)", result.Status)
	}
	if result.Error != "response body did not contain expected string" {
		t.Errorf("Error = %q, want 'response body did not contain expected string'", result.Error)
	}
}

func TestChecker_BodyMatch_SkippedOnNonMatchingStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `internal error`)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc-body-skip",
		URL:            srv.URL,
		Method:         "GET",
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		ExpectedBody:   "healthy",
	}

	result := c.Check(context.Background(), hc)
	if result.Status != store.HealthCheckDown {
		t.Errorf("Status = %q, want down", result.Status)
	}
	// Error should NOT be about body matching — it's a status code issue
	if result.Error == "response body did not contain expected string" {
		t.Error("body match should not run when status code doesn't match")
	}
}

func TestChecker_BodyMatch_NoBodyConfig_Ignored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprint(w, `anything`)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc-body-none",
		URL:            srv.URL,
		Method:         "GET",
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		// ExpectedBody not set — body matching should be skipped
	}

	result := c.Check(context.Background(), hc)
	if result.Status != store.HealthCheckUp {
		t.Errorf("Status = %q, want up (no body matching configured)", result.Status)
	}
}

func TestChecker_BodyReadLimit(t *testing.T) {
	// Generate a response body larger than maxBodyReadBytes (1MB).
	// Use a handler that returns a very large body but the expected string is NOT in the first 1MB.
	bigBody := strings.Repeat("x", maxBodyReadBytes+1000)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, bigBody)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc-body-limit",
		URL:            srv.URL,
		Method:         "GET",
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		ExpectedBody:   "needle-that-doesnt-exist",
	}

	result := c.Check(context.Background(), hc)
	// Should be down because the expected string is not in the first 1MB
	if result.Status != store.HealthCheckDown {
		t.Errorf("Status = %q, want down (expected string not in limited body)", result.Status)
	}
	if result.Error != "response body did not contain expected string" {
		t.Errorf("Error = %q, want body mismatch error", result.Error)
	}
}

func TestChecker_BodyMatch_WithRetry(t *testing.T) {
	// First call returns wrong body, second returns correct body.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.WriteHeader(200)
		if n == 1 {
			fmt.Fprint(w, `{"status":"starting"}`)
			return
		}
		fmt.Fprint(w, `{"status":"healthy"}`)
	}))
	defer srv.Close()

	c := NewChecker()
	hc := store.HealthCheck{
		ID:             "hc-body-retry",
		URL:            srv.URL,
		Method:         "GET",
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		ExpectedBody:   "healthy",
		Retries:        2,
	}

	result := c.Check(context.Background(), hc)
	if result.Status != store.HealthCheckUp {
		t.Errorf("Status = %q, want up (second attempt has correct body)", result.Status)
	}
	if got := calls.Load(); got < 2 {
		t.Errorf("expected at least 2 calls, got %d", got)
	}
}

// --- Backoff tests ---

func TestScheduler_BackoffAfterConsecutiveFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500) // always failing
	}))
	defer srv.Close()

	hc := store.HealthCheck{
		ID:             "hc-backoff",
		Name:           "backoff-test",
		URL:            srv.URL,
		Method:         "GET",
		IntervalSecs:   60,
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		Enabled:        true,
	}

	ms := &mockHCStore{checks: []store.HealthCheck{hc}}
	notifier := &mockNotifier{}
	sched := NewScheduler(ms, 15*time.Second, notifier)
	ctx := context.Background()

	// Normal interval before any failures.
	normalInterval := sched.EffectiveInterval(hc)
	if normalInterval != 60*time.Second {
		t.Errorf("initial interval = %v, want 60s", normalInterval)
	}

	// Run 3 failures (backoffThreshold).
	for i := 0; i < backoffThreshold; i++ {
		sched.runCheck(ctx, hc)
	}

	// After exactly backoffThreshold failures, backoff should kick in (2x).
	backedOff := sched.EffectiveInterval(hc)
	expected := 2 * 60 * time.Second
	if backedOff != expected {
		t.Errorf("interval after %d failures = %v, want %v", backoffThreshold, backedOff, expected)
	}

	// One more failure → 4x.
	sched.runCheck(ctx, hc)
	backedOff = sched.EffectiveInterval(hc)
	expected = 4 * 60 * time.Second
	if backedOff != expected {
		t.Errorf("interval after %d failures = %v, want %v", backoffThreshold+1, backedOff, expected)
	}

	// More failures shouldn't exceed 4x (maxBackoffMultiplier).
	for i := 0; i < 5; i++ {
		sched.runCheck(ctx, hc)
	}
	backedOff = sched.EffectiveInterval(hc)
	maxExpected := 4 * 60 * time.Second
	if backedOff != maxExpected {
		t.Errorf("interval should cap at %v, got %v", maxExpected, backedOff)
	}
}

func TestScheduler_BackoffResetOnSuccess(t *testing.T) {
	var returnStatus atomic.Int32
	returnStatus.Store(500)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(returnStatus.Load()))
	}))
	defer srv.Close()

	hc := store.HealthCheck{
		ID:             "hc-backoff-reset",
		Name:           "backoff-reset-test",
		URL:            srv.URL,
		Method:         "GET",
		IntervalSecs:   60,
		TimeoutSecs:    5,
		ExpectedStatus: 200,
		Enabled:        true,
	}

	ms := &mockHCStore{checks: []store.HealthCheck{hc}}
	notifier := &mockNotifier{}
	sched := NewScheduler(ms, 15*time.Second, notifier)
	ctx := context.Background()

	// Accumulate failures past the threshold.
	for i := 0; i < backoffThreshold+1; i++ {
		sched.runCheck(ctx, hc)
	}

	// Verify interval is increased.
	backedOff := sched.EffectiveInterval(hc)
	if backedOff <= 60*time.Second {
		t.Fatalf("expected backoff interval > 60s, got %v", backedOff)
	}

	// Now succeed.
	returnStatus.Store(200)
	sched.runCheck(ctx, hc)

	// Interval should reset to normal.
	reset := sched.EffectiveInterval(hc)
	if reset != 60*time.Second {
		t.Errorf("interval after success = %v, want 60s (should reset)", reset)
	}
}

// --- Reliability calculation tests ---

func TestReliability_Calculation(t *testing.T) {
	sched := NewScheduler(nil, 15*time.Second)

	checkID := "hc-reliability"

	// Initially 0%.
	if r := sched.Reliability(checkID); r != 0 {
		t.Errorf("initial reliability = %f, want 0", r)
	}

	// Manually add results to the ring.
	sched.recentMu.Lock()
	ring := &resultRing{}
	sched.recent[checkID] = ring
	sched.recentMu.Unlock()

	// Add 3 up, 2 down = 60% reliability.
	ring.add(store.HealthCheckUp)
	ring.add(store.HealthCheckUp)
	ring.add(store.HealthCheckDown)
	ring.add(store.HealthCheckUp)
	ring.add(store.HealthCheckDown)

	r := sched.Reliability(checkID)
	if r != 60.0 {
		t.Errorf("reliability = %f, want 60.0", r)
	}
}

func TestReliability_AllUp(t *testing.T) {
	sched := NewScheduler(nil, 15*time.Second)
	checkID := "hc-all-up"

	sched.recentMu.Lock()
	ring := &resultRing{}
	sched.recent[checkID] = ring
	sched.recentMu.Unlock()

	for i := 0; i < recentResultsWindow; i++ {
		ring.add(store.HealthCheckUp)
	}

	r := sched.Reliability(checkID)
	if r != 100.0 {
		t.Errorf("reliability = %f, want 100.0", r)
	}
}

func TestReliability_AllDown(t *testing.T) {
	sched := NewScheduler(nil, 15*time.Second)
	checkID := "hc-all-down"

	sched.recentMu.Lock()
	ring := &resultRing{}
	sched.recent[checkID] = ring
	sched.recentMu.Unlock()

	for i := 0; i < recentResultsWindow; i++ {
		ring.add(store.HealthCheckDown)
	}

	r := sched.Reliability(checkID)
	if r != 0.0 {
		t.Errorf("reliability = %f, want 0.0", r)
	}
}

func TestReliability_PartialWindow(t *testing.T) {
	sched := NewScheduler(nil, 15*time.Second)
	checkID := "hc-partial"

	sched.recentMu.Lock()
	ring := &resultRing{}
	sched.recent[checkID] = ring
	sched.recentMu.Unlock()

	// Only 2 results: 1 up, 1 down = 50%.
	ring.add(store.HealthCheckUp)
	ring.add(store.HealthCheckDown)

	r := sched.Reliability(checkID)
	if r != 50.0 {
		t.Errorf("reliability = %f, want 50.0", r)
	}
}

func TestResultRing_CircularOverwrite(t *testing.T) {
	ring := &resultRing{}

	// Fill the ring completely with Up.
	for i := 0; i < recentResultsWindow; i++ {
		ring.add(store.HealthCheckUp)
	}
	if ring.reliability() != 100.0 {
		t.Fatalf("expected 100%% reliability, got %f", ring.reliability())
	}

	// Overwrite all with Down (window rotates).
	for i := 0; i < recentResultsWindow; i++ {
		ring.add(store.HealthCheckDown)
	}
	if ring.reliability() != 0.0 {
		t.Fatalf("expected 0%% reliability after full overwrite, got %f", ring.reliability())
	}
}

func TestRecentResults_Order(t *testing.T) {
	ring := &resultRing{}
	ring.add(store.HealthCheckUp)
	ring.add(store.HealthCheckDown)
	ring.add(store.HealthCheckUp)

	results := ring.recentResults()
	if len(results) != 3 {
		t.Fatalf("len = %d, want 3", len(results))
	}
	// Oldest first
	if results[0] != store.HealthCheckUp {
		t.Errorf("results[0] = %q, want up", results[0])
	}
	if results[1] != store.HealthCheckDown {
		t.Errorf("results[1] = %q, want down", results[1])
	}
	if results[2] != store.HealthCheckUp {
		t.Errorf("results[2] = %q, want up", results[2])
	}
}

// --- Scheduler existing tests ---

func TestScheduler_SemaphoreCapacity(t *testing.T) {
	s := NewScheduler(nil, 15*time.Second)
	if cap(s.sem) != 16 {
		t.Errorf("semaphore capacity = %d, want 16", cap(s.sem))
	}
}

func TestScheduler_IsDue(t *testing.T) {
	s := &Scheduler{
		lastRun: make(map[string]time.Time),
		sem:     make(chan struct{}, 16),
		backoff: make(map[string]*backoffState),
	}

	hc := store.HealthCheck{ID: "hc1", IntervalSecs: 60}
	now := time.Now()

	// Never run -> due
	if !s.isDue(hc, now) {
		t.Error("expected isDue = true for never-run check")
	}

	// Just ran -> not due
	s.mu.Lock()
	s.lastRun["hc1"] = now
	s.mu.Unlock()
	if s.isDue(hc, now.Add(30*time.Second)) {
		t.Error("expected isDue = false 30s after last run (interval=60)")
	}

	// After interval -> due
	if !s.isDue(hc, now.Add(61*time.Second)) {
		t.Error("expected isDue = true 61s after last run (interval=60)")
	}
}
