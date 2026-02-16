package healthcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestScheduler_IsDue(t *testing.T) {
	s := &Scheduler{
		lastRun: make(map[string]time.Time),
	}

	hc := store.HealthCheck{ID: "hc1", IntervalSecs: 60}
	now := time.Now()

	// Never run → due
	if !s.isDue(hc, now) {
		t.Error("expected isDue = true for never-run check")
	}

	// Just ran → not due
	s.mu.Lock()
	s.lastRun["hc1"] = now
	s.mu.Unlock()
	if s.isDue(hc, now.Add(30*time.Second)) {
		t.Error("expected isDue = false 30s after last run (interval=60)")
	}

	// After interval → due
	if !s.isDue(hc, now.Add(61*time.Second)) {
		t.Error("expected isDue = true 61s after last run (interval=60)")
	}
}
