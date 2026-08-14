package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	srvpkg "github.com/adham90/opentrace/pkg/server"
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
	done     chan struct{}
	stopOnce sync.Once
}

type sseSessionState struct {
	initDone  chan struct{} // closed once initialize has been forwarded
	initOnce  sync.Once     // guards closing initDone exactly once
	lastTouch time.Time
	// userID binds the session to the first authenticated user that used it.
	// The SSE transport routes purely on the sessionid query param, so without
	// this a leaked session id would let any other authenticated caller drive
	// someone else's (possibly admin) session.
	userID string
}

func newSSEInitGate() *sseInitGate {
	g := &sseInitGate{
		sessions: make(map[string]*sseSessionState),
		done:     make(chan struct{}),
	}
	go g.sweepLoop()
	return g
}

// Stop terminates the background sweep goroutine. Safe to call more than once.
func (g *sseInitGate) Stop() {
	g.stopOnce.Do(func() { close(g.done) })
}

// stateFor returns (or creates) the gate state for sessionID.
func (g *sseInitGate) stateFor(sessionID string) *sseSessionState {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.sessions[sessionID]
	if !ok {
		s = newSessionState()
		g.sessions[sessionID] = s
	} else {
		s.lastTouch = time.Now()
	}
	return s
}

func newSessionState() *sseSessionState {
	return &sseSessionState{
		initDone:  make(chan struct{}),
		lastTouch: time.Now(),
	}
}

// markInitialized closes the initDone channel (idempotent).
// Clients re-send initialize after an SSE reconnect, so two POSTs for the same
// sessionid can land concurrently; a select-then-close would race and panic
// with "close of closed channel". sync.Once makes it atomic.
func (g *sseInitGate) markInitialized(sessionID string) {
	s := g.stateFor(sessionID)
	s.initOnce.Do(func() { close(s.initDone) })
}

// bindUser ties sessionID to userID on first use and reports whether the
// caller is allowed to use this session. An empty userID (transport without
// an authenticated user) neither binds nor blocks.
func (g *sseInitGate) bindUser(sessionID, userID string) bool {
	if userID == "" {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.sessions[sessionID]
	if !ok {
		s = newSessionState()
		g.sessions[sessionID] = s
	}
	s.lastTouch = time.Now()
	if s.userID == "" {
		s.userID = userID
		return true
	}
	return s.userID == userID
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
	ticker := time.NewTicker(gateSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-g.done:
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-gateSessionTTL)
			g.mu.Lock()
			for id, s := range g.sessions {
				if s.lastTouch.Before(cutoff) {
					delete(g.sessions, id)
				}
			}
			g.mu.Unlock()
		}
	}
}

const (
	// gateSweepInterval is how often idle session state is swept.
	gateSweepInterval = 10 * time.Minute
	// gateSessionTTL is how long untouched session state is retained.
	gateSessionTTL = 1 * time.Hour
)

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
//
// Only the prefix is buffered — a large tools/call payload can be far bigger
// than we need for the method probe — but the unread remainder is spliced back
// onto the replay reader. Dropping it would hand the SDK a truncated document
// and turn a valid request into an opaque parse error.
func peekMethod(body io.Reader) (method string, replay io.Reader, err error) {
	buf, err := io.ReadAll(io.LimitReader(body, maxGateBody))
	if err != nil {
		return "", nil, err
	}
	replay = io.Reader(bytes.NewReader(buf))
	if len(buf) == maxGateBody {
		replay = io.MultiReader(bytes.NewReader(buf), body)
	}
	return probeMethod(buf), replay, nil
}

// probeMethod extracts the top-level JSON-RPC "method" from a possibly
// truncated document. json.Unmarshal is not usable here: the buffer is only
// the first maxGateBody bytes of a potentially much larger body, and
// Unmarshal rejects the whole (truncated) document — which would classify
// every large request as "unknown method" and make it wait out the init gate.
// Scanning tokens stops as soon as "method" is seen, near the start in
// practice. An unparseable body yields "" and is passed through so the SDK can
// return the proper JSON-RPC error.
func probeMethod(buf []byte) string {
	dec := json.NewDecoder(bytes.NewReader(buf))
	tok, err := dec.Token()
	if err != nil {
		return ""
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return ""
	}
	for {
		keyTok, err := dec.Token()
		if err != nil {
			return ""
		}
		key, ok := keyTok.(string)
		if !ok {
			return "" // closing brace or malformed input
		}
		if key == "method" {
			val, err := dec.Token()
			if err != nil {
				return ""
			}
			s, _ := val.(string)
			return s
		}
		if err := skipJSONValue(dec); err != nil {
			return ""
		}
	}
}

// skipJSONValue consumes the next value from dec, descending through nested
// objects and arrays.
func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok || (d != '{' && d != '[') {
		return nil
	}
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
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

		// The SSE transport routes on the sessionid alone. Pin the session to
		// the user who first used it so a leaked id cannot be replayed by a
		// different (e.g. lower-privileged) authenticated caller.
		var userID string
		if u := srvpkg.UserFromContext(r.Context()); u != nil {
			userID = u.ID
		}
		if !g.bindUser(sessionID, userID) {
			http.Error(w, "session belongs to a different user", http.StatusForbidden)
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
