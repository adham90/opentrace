package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSSEInitGate_HoldsToolsCallUntilInitialize posts tools/call and
// initialize in parallel and asserts that the gate delivers initialize to
// the inner handler before tools/call.
func TestSSEInitGate_HoldsToolsCallUntilInitialize(t *testing.T) {
	var mu sync.Mutex
	var order []string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		if strings.Contains(string(b), `"initialize"`) {
			order = append(order, "initialize")
		} else if strings.Contains(string(b), `"tools/call"`) {
			order = append(order, "tools/call")
		}
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})

	h := newSSEInitGate().wrap(inner)
	srv := httptest.NewServer(h)
	defer srv.Close()

	sessionURL := srv.URL + "/mcp-sse?sessionid=SID1"

	// Fire tools/call first; it should block until initialize arrives.
	var wg sync.WaitGroup
	wg.Add(2)
	callDone := make(chan struct{})
	go func() {
		defer wg.Done()
		body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{}}`
		resp, err := http.Post(sessionURL, "application/json", strings.NewReader(body))
		if err != nil {
			t.Errorf("tools/call POST: %v", err)
			return
		}
		resp.Body.Close()
		close(callDone)
	}()

	// Give the tools/call POST time to reach the gate and start waiting.
	time.Sleep(50 * time.Millisecond)

	go func() {
		defer wg.Done()
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
		resp, err := http.Post(sessionURL, "application/json", strings.NewReader(body))
		if err != nil {
			t.Errorf("initialize POST: %v", err)
			return
		}
		resp.Body.Close()
	}()

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 {
		t.Fatalf("expected 2 forwarded requests, got %d: %v", len(order), order)
	}
	if order[0] != "initialize" {
		t.Fatalf("expected initialize first, got order=%v", order)
	}
	if order[1] != "tools/call" {
		t.Fatalf("expected tools/call second, got order=%v", order)
	}
}

// TestSSEInitGate_InitializeForwardedImmediately ensures initialize isn't
// itself subject to any wait.
func TestSSEInitGate_InitializeForwardedImmediately(t *testing.T) {
	var seen atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		w.WriteHeader(http.StatusAccepted)
	})
	h := newSSEInitGate().wrap(inner)
	srv := httptest.NewServer(h)
	defer srv.Close()

	start := time.Now()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	resp, err := http.Post(srv.URL+"/mcp-sse?sessionid=SID2", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if dt := time.Since(start); dt > 500*time.Millisecond {
		t.Errorf("initialize took too long: %v", dt)
	}
	if seen.Load() != 1 {
		t.Errorf("inner handler not invoked: seen=%d", seen.Load())
	}
}

// TestSSEInitGate_NotificationsBypass ensures ping and notifications/initialized
// are not blocked even when no initialize has been seen.
func TestSSEInitGate_NotificationsBypass(t *testing.T) {
	var seen atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		w.WriteHeader(http.StatusAccepted)
	})
	h := newSSEInitGate().wrap(inner)
	srv := httptest.NewServer(h)
	defer srv.Close()

	cases := []string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	}
	for _, body := range cases {
		start := time.Now()
		resp, err := http.Post(srv.URL+"/mcp-sse?sessionid=SID3", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		resp.Body.Close()
		if dt := time.Since(start); dt > 500*time.Millisecond {
			t.Errorf("method should not be gated but took %v (body=%s)", dt, body)
		}
	}
	if got := seen.Load(); got != 2 {
		t.Errorf("expected 2 forwarded, got %d", got)
	}
}

// TestSSEInitGate_TimeoutForwardsAnyway verifies that the gate doesn't
// deadlock: if initialize never arrives, non-init POSTs are forwarded after
// the timeout so the SDK can return its standard error.
func TestSSEInitGate_TimeoutForwardsAnyway(t *testing.T) {
	var seen atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		w.WriteHeader(http.StatusAccepted)
	})
	g := newSSEInitGate()
	// Use a short timeout for the test by wrapping manually.
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("sessionid")
		method, replay, _ := peekMethod(r.Body)
		r.Body = io.NopCloser(replay)
		if method == "initialize" {
			inner.ServeHTTP(w, r)
			g.markInitialized(sessionID)
		} else {
			g.waitForInit(r.Context(), sessionID, 150*time.Millisecond)
			inner.ServeHTTP(w, r)
		}
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{}}`
	start := time.Now()
	resp, err := http.Post(srv.URL+"/mcp-sse?sessionid=NEVERINIT", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	dt := time.Since(start)
	if dt < 150*time.Millisecond || dt > 500*time.Millisecond {
		t.Errorf("expected ~150ms wait, got %v", dt)
	}
	if seen.Load() != 1 {
		t.Errorf("inner handler not invoked after timeout")
	}
}

// TestPeekMethod_Replay ensures the replay reader returns the full original
// body so downstream parsers aren't short-read.
func TestPeekMethod_Replay(t *testing.T) {
	src := bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"x":1}}`))
	method, replay, err := peekMethod(src)
	if err != nil {
		t.Fatal(err)
	}
	if method != "tools/call" {
		t.Errorf("method=%q", method)
	}
	out, _ := io.ReadAll(replay)
	if !strings.Contains(string(out), `"params":{"x":1}`) {
		t.Errorf("replay truncated: %s", out)
	}
}
