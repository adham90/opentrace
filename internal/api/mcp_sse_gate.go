package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// sseInitGate holds non-initialize POSTs to /mcp-sse?sessionid=... until
// the same session's `initialize` message has been pushed into the SDK's
// message queue.
//
// Why: the MCP Go SDK's SSE transport pushes every POST body onto a shared
// channel in whatever goroutine order wins scheduling. If the client sends
// `initialize` and `tools/call` in parallel (e.g. during the handshake or
// after an SSE reconnect), the SDK's per-session handler may read
// `tools/call` first and reject it with:
//
//	method "tools/call" is invalid during session initialization
//
// The MCP spec says the client SHOULD wait for initialize's response before
// sending other requests, but real clients often don't — particularly after
// transient SSE disconnects. Gating non-init POSTs behind a per-session
// "initialize-seen" signal makes the server tolerant of the race without
// touching the SDK.
type sseInitGate struct {
	mu       sync.Mutex
	sessions map[string]*sseSessionState
}

type sseSessionState struct {
	initDone chan struct{} // closed once initialize has been forwarded
	lastTouch time.Time
}

func newSSEInitGate() *sseInitGate {
	g := &sseInitGate{sessions: make(map[string]*sseSessionState)}
	go g.sweepLoop()
	return g
}

// stateFor returns (or creates) the gate state for sessionID.
func (g *sseInitGate) stateFor(sessionID string) *sseSessionState {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.sessions[sessionID]
	if !ok {
		s = &sseSessionState{
			initDone:  make(chan struct{}),
			lastTouch: time.Now(),
		}
		g.sessions[sessionID] = s
	} else {
		s.lastTouch = time.Now()
	}
	return s
}

// markInitialized closes the initDone channel (idempotent).
func (g *sseInitGate) markInitialized(sessionID string) {
	s := g.stateFor(sessionID)
	select {
	case <-s.initDone:
		// already closed
	default:
		close(s.initDone)
	}
}

// waitForInit blocks until the session has seen an initialize POST, or the
// timeout elapses. It does not hold g.mu during the wait.
func (g *sseInitGate) waitForInit(ctx context.Context, sessionID string, timeout time.Duration) {
	s := g.stateFor(sessionID)
	select {
	case <-s.initDone:
	case <-time.After(timeout):
	case <-ctx.Done():
	}
}

// sweepLoop drops state for sessions that haven't been touched in a while.
// Sessions are short-lived; this keeps the map bounded without needing the
// SDK to call us when sessions close.
func (g *sseInitGate) sweepLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-1 * time.Hour)
		g.mu.Lock()
		for id, s := range g.sessions {
			if s.lastTouch.Before(cutoff) {
				delete(g.sessions, id)
			}
		}
		g.mu.Unlock()
	}
}

// maxGateBody caps how much of the POST body we buffer to extract `method`.
// JSON-RPC initialize / tools/call requests are a few hundred bytes typically;
// giving 64 KB is plenty and matches reasonable MCP payload limits.
const maxGateBody = 64 * 1024

// initWaitTimeout is how long a non-init POST waits for `initialize` to
// arrive before being forwarded anyway. Real clients typically send both
// within a few ms; 2s leaves generous headroom and still fails fast.
const initWaitTimeout = 2 * time.Second

// peekMethod reads up to maxGateBody bytes of a JSON-RPC request body,
// extracts the `method` field, and returns both the method and a new io.Reader
// replayable as the request body for downstream handlers.
func peekMethod(body io.Reader) (method string, replay io.Reader, err error) {
	buf, err := io.ReadAll(io.LimitReader(body, maxGateBody))
	if err != nil {
		return "", nil, err
	}
	// JSON-RPC requests have a top-level `method` string. We don't need to fully
	// parse — a minimal struct is enough. Malformed bodies are passed through so
	// the SDK can return the right error.
	var probe struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(buf, &probe)
	return probe.Method, bytes.NewReader(buf), nil
}

// sseSerializeInit is the handler wrapper. It only gates POSTs to the SSE
// endpoint that carry a sessionid.
func (g *sseInitGate) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		sessionID := r.URL.Query().Get("sessionid")
		if sessionID == "" {
			next.ServeHTTP(w, r)
			return
		}

		method, replay, err := peekMethod(r.Body)
		if err != nil {
			// Let the SDK handle malformed bodies — it returns a proper JSON-RPC error.
			r.Body = io.NopCloser(bytes.NewReader(nil))
			next.ServeHTTP(w, r)
			return
		}
		r.Body = io.NopCloser(replay)

		switch method {
		case "initialize":
			// Forward first, so the SDK enqueues it, then mark init-seen.
			next.ServeHTTP(w, r)
			g.markInitialized(sessionID)
		case "ping", "notifications/initialized", "notifications/cancelled":
			// Always allowed per MCP spec; do not gate.
			next.ServeHTTP(w, r)
		default:
			// Any other method must wait for initialize to land for this session.
			g.waitForInit(r.Context(), sessionID, initWaitTimeout)
			next.ServeHTTP(w, r)
		}
	})
}
