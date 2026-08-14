package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	srvpkg "github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

// ---------- API key auth must fail closed ----------

// TestDynamicAPIKeyAuth_StoreErrorFailsClosed pins the security invariant: a
// settings-store read failure must not be read as "auth disabled". Before the
// fix the request was forwarded unauthenticated.
func TestDynamicAPIKeyAuth_StoreErrorFailsClosed(t *testing.T) {
	ms := newMockSettingsStore()
	ms.apiKeyErr = errors.New("database is locked")

	s := &Server{cfg: &config.Config{}, settingsStore: ms}

	var reached bool
	handler := s.DynamicAPIKeyAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/logs", nil) // no Authorization
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if reached {
		t.Fatal("unauthenticated request reached the handler during a settings-store error")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if msg := decodeErrorJSON(t, rec.Body.Bytes()); strings.Contains(msg, "database is locked") {
		t.Fatalf("internal error detail leaked to client: %q", msg)
	}
}

// TestGetEffectiveAPIKey_StoreErrorPropagates ensures the resolver reports the
// error instead of flattening it to "no key configured".
func TestGetEffectiveAPIKey_StoreErrorPropagates(t *testing.T) {
	ms := newMockSettingsStore()
	ms.apiKeyErr = errors.New("disk I/O error")
	s := &Server{cfg: &config.Config{}, settingsStore: ms}

	key, err := s.getEffectiveAPIKey(context.Background())
	if err == nil {
		t.Fatal("expected an error from the settings store")
	}
	if key != "" {
		t.Fatalf("expected empty key, got %q", key)
	}
}

// TestGetEffectiveAPIKey_NoKeyConfigured keeps the genuine "auth disabled"
// case working: empty key, no error.
func TestGetEffectiveAPIKey_NoKeyConfigured(t *testing.T) {
	s := &Server{cfg: &config.Config{}, settingsStore: newMockSettingsStore()}
	key, err := s.getEffectiveAPIKey(context.Background())
	if err != nil || key != "" {
		t.Fatalf("expected (\"\", nil), got (%q, %v)", key, err)
	}
}

// ---------- ProxyAuth role validation ----------

func newProxyAuthServer(users store.UserStore, trusted ...string) *Server {
	return &Server{userStore: users, cfg: &config.Config{TrustedProxies: trusted}}
}

// TestProxyAuth_SpoofedAdminRoleFromUntrustedPeer is the core regression: a
// direct client that bypasses the proxy must never be granted a role from a
// request header.
func TestProxyAuth_SpoofedAdminRoleFromUntrustedPeer(t *testing.T) {
	t.Setenv("OPENTRACE_TRUST_PROXY_AUTH", "true")

	users := newMockUserStore()
	s := newProxyAuthServer(users, "10.0.0.1")

	var got *store.User
	handler := s.ProxyAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:1234" // not the configured proxy
	req.Header.Set("X-Forwarded-User", "attacker@evil.com")
	req.Header.Set("X-Forwarded-User-Role", "admin")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got != nil {
		t.Fatalf("untrusted peer minted an identity: %+v", got)
	}
	if u, _ := users.GetByEmail(context.Background(), "attacker@evil.com"); u != nil {
		t.Fatalf("untrusted peer created a user: %+v", u)
	}
}

// TestProxyAuth_AdminRoleHeaderNotHonouredByDefault covers the proxy that
// forwards inbound X-Forwarded-* headers unchanged: even from the trusted
// peer, "admin" is only honoured with an explicit opt-in.
func TestProxyAuth_AdminRoleHeaderNotHonouredByDefault(t *testing.T) {
	t.Setenv("OPENTRACE_TRUST_PROXY_AUTH", "true")
	os.Unsetenv("OPENTRACE_TRUST_PROXY_AUTH_ADMIN")

	s := newProxyAuthServer(newMockUserStore(), "10.0.0.1")

	var got *store.User
	handler := s.ProxyAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5000"
	req.Header.Set("X-Forwarded-User", "someone@example.com")
	req.Header.Set("X-Forwarded-User-Role", "admin")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got == nil {
		t.Fatal("expected a proxy-authenticated user")
	}
	if got.Role != store.RoleMember {
		t.Fatalf("header-asserted admin was honoured: role=%q", got.Role)
	}
}

func TestProxyAuth_AdminRoleHeaderWithOptIn(t *testing.T) {
	t.Setenv("OPENTRACE_TRUST_PROXY_AUTH", "true")
	t.Setenv("OPENTRACE_TRUST_PROXY_AUTH_ADMIN", "true")

	s := newProxyAuthServer(newMockUserStore(), "10.0.0.1")

	var got *store.User
	handler := s.ProxyAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5000"
	req.Header.Set("X-Forwarded-User", "boss@example.com")
	req.Header.Set("X-Forwarded-User-Role", "admin")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got == nil || got.Role != store.RoleAdmin {
		t.Fatalf("expected admin with explicit opt-in, got %+v", got)
	}
}

func TestResolveProxyRole_UnknownRoleIsMember(t *testing.T) {
	for _, in := range []string{"superadmin", "ADMIN", "root", "'; DROP TABLE users;--", ""} {
		if got := resolveProxyRole(in, true); got != store.RoleMember && in != "" {
			if got == store.RoleAdmin {
				t.Fatalf("role %q resolved to admin", in)
			}
		}
		if got := resolveProxyRole(in, false); got != store.RoleMember {
			t.Fatalf("role %q resolved to %q, want member", in, got)
		}
	}
	if got := resolveProxyRole("member", false); got != store.RoleMember {
		t.Fatalf("member role mangled: %q", got)
	}
}

// TestProxyAuth_LookupErrorDoesNotAutoCreate ensures a store failure is not
// mistaken for "user does not exist".
func TestProxyAuth_LookupErrorDoesNotAutoCreate(t *testing.T) {
	t.Setenv("OPENTRACE_TRUST_PROXY_AUTH", "true")

	users := &erroringUserStore{err: errors.New("connection reset")}
	s := newProxyAuthServer(users, "10.0.0.1")

	handler := s.ProxyAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not run when identity cannot be established")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5000"
	req.Header.Set("X-Forwarded-User", "person@example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if users.created {
		t.Fatal("a store error triggered user auto-creation")
	}
}

// erroringUserStore fails GetByEmail with a non-sentinel error.
type erroringUserStore struct {
	store.UserStore
	err     error
	created bool
}

func (e *erroringUserStore) GetByEmail(context.Context, string) (*store.User, error) {
	return nil, e.err
}

func (e *erroringUserStore) Create(context.Context, store.CreateUserParams) (*store.User, error) {
	e.created = true
	return &store.User{}, nil
}

// ---------- Rate limiter keying and bounds ----------

// TestClientIP_SpoofedLeftmostXFFIgnored proves the limiter can no longer be
// bypassed by prepending a fabricated hop to X-Forwarded-For.
func TestClientIP_SpoofedLeftmostXFFIgnored(t *testing.T) {
	rl := &RateLimiter{trustedProxies: map[string]bool{"10.0.0.1": true}}

	keys := make(map[string]bool)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:8080"
		// Attacker-supplied hop, then the real client IP appended by the proxy.
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("9.9.9.%d, 198.51.100.20", i))
		keys[rl.clientIP(req)] = true
	}
	if len(keys) != 1 {
		t.Fatalf("expected one stable key, got %v", keys)
	}
	if !keys["198.51.100.20"] {
		t.Fatalf("expected the proxy-appended client IP, got %v", keys)
	}
}

// TestClientIP_GarbageXFFFallsBackToPeer ensures non-IP header junk never
// becomes a rate-limit key.
func TestClientIP_GarbageXFFFallsBackToPeer(t *testing.T) {
	rl := &RateLimiter{trustedProxies: map[string]bool{"10.0.0.1": true}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:8080"
	req.Header.Set("X-Forwarded-For", "not-an-ip")
	if got := rl.clientIP(req); got != "10.0.0.1" {
		t.Fatalf("expected peer fallback 10.0.0.1, got %q", got)
	}
}

// TestRateLimiter_BoundedEntries proves the map stays bounded without the old
// O(n^2) oldest-entry scan.
func TestRateLimiter_BoundedEntries(t *testing.T) {
	rl := NewRateLimiter(1000, time.Minute, nil)
	defer rl.Stop()

	handler := rl.Middleware(noReadHandler())
	for i := 0; i < maxRateLimitEntries+2000; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:1", i>>16&0xff, i>>8&0xff, i&0xff)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	rl.mu.Lock()
	n := len(rl.entries)
	orderLen := len(rl.order)
	rl.mu.Unlock()

	if n > maxRateLimitEntries {
		t.Fatalf("entries map unbounded: %d > %d", n, maxRateLimitEntries)
	}
	if orderLen > 2*maxRateLimitEntries {
		t.Fatalf("eviction FIFO unbounded: %d records", orderLen)
	}
}

// TestRateLimiter_StillLimitsAfterEviction guards against the bounded map
// breaking the actual limiting behaviour.
func TestRateLimiter_StillLimitsAfterEviction(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute, nil)
	defer rl.Stop()
	handler := rl.Middleware(noReadHandler())

	var codes []int
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "198.51.100.7:9000"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 || codes[3] != 429 {
		t.Fatalf("unexpected status sequence %v", codes)
	}
}

// ---------- Prometheus label cardinality ----------

// TestMetricPath_UsesRoutePattern proves arbitrary paths collapse to a bounded
// set of labels.
func TestMetricPath_UsesRoutePattern(t *testing.T) {
	r := chi.NewRouter()
	r.Use(PrometheusMiddleware)

	var labels []string
	capture := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		labels = append(labels, metricPath(req))
		w.WriteHeader(http.StatusOK)
	})
	r.Handle("/api/logs/{id}", capture)
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		labels = append(labels, metricPath(req))
		w.WriteHeader(http.StatusNotFound)
	})

	for _, p := range []string{"/api/logs/abc", "/api/logs/def", "/scan-1", "/scan-2", "/scan-3"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}

	uniq := map[string]bool{}
	for _, l := range labels {
		uniq[l] = true
	}
	if len(uniq) != 2 {
		t.Fatalf("expected 2 distinct labels (route pattern + unmatched), got %v", uniq)
	}
	if !uniq["/api/logs/{id}"] || !uniq[unmatchedPathLabel] {
		t.Fatalf("unexpected labels: %v", uniq)
	}
}

// TestMetricPath_NoChiContextFallsBack keeps non-chi handlers (the SSE mux)
// labelled with the normalized path.
func TestMetricPath_NoChiContextFallsBack(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/mcp-sse", nil)
	if got := metricPath(req); got != "/mcp-sse" {
		t.Fatalf("expected /mcp-sse, got %q", got)
	}
}

// ---------- SSE init gate ----------

// TestSSEInitGate_ConcurrentInitializeNoPanic reproduces the double-close
// panic: before the sync.Once fix, concurrent initialize POSTs for one session
// could both close initDone ("close of closed channel").
func TestSSEInitGate_ConcurrentInitializeNoPanic(t *testing.T) {
	g := newSSEInitGate()
	defer g.Stop()

	for round := 0; round < 200; round++ {
		sessionID := fmt.Sprintf("SID-%d", round)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				g.markInitialized(sessionID)
			}()
		}
		close(start)
		wg.Wait()
	}
}

// TestSSEInitGate_LargeBodyNotTruncated proves POST bodies bigger than the
// method-probe buffer survive the gate intact.
func TestSSEInitGate_LargeBodyNotTruncated(t *testing.T) {
	g := newSSEInitGate()
	defer g.Stop()

	var gotLen int
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		gotLen = len(b)
		w.WriteHeader(http.StatusAccepted)
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"pad":"` +
		strings.Repeat("x", 3*maxGateBody) + `"}}`

	srv := httptest.NewServer(g.wrap(inner))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/mcp-sse?sessionid=BIG", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	if gotLen != len(body) {
		t.Fatalf("body truncated: forwarded %d of %d bytes", gotLen, len(body))
	}
}

// TestProbeMethod_TruncatedPrefix proves the method is still recognised when
// only the first maxGateBody bytes of a big body are available — otherwise
// every large request would be classified as unknown and wait out the gate.
func TestProbeMethod_TruncatedPrefix(t *testing.T) {
	full := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"pad":"` +
		strings.Repeat("y", 4*maxGateBody) + `"}}`
	if got := probeMethod([]byte(full[:maxGateBody])); got != "tools/call" {
		t.Fatalf("probeMethod on truncated prefix = %q, want tools/call", got)
	}
	if got := probeMethod([]byte(`{"params":{"a":[1,2,{"b":3}]},"method":"initialize"}`)); got != "initialize" {
		t.Fatalf("probeMethod with a nested leading object = %q", got)
	}
	if got := probeMethod([]byte("not json")); got != "" {
		t.Fatalf("probeMethod on garbage = %q, want empty", got)
	}
}

// TestSSEInitGate_LargeInitializeNotGated proves a >64KB initialize is
// recognised as initialize (and so is neither delayed nor mis-gated).
func TestSSEInitGate_LargeInitializeNotGated(t *testing.T) {
	g := newSSEInitGate()
	defer g.Stop()

	srv := httptest.NewServer(g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusAccepted)
	})))
	defer srv.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"pad":"` +
		strings.Repeat("z", 3*maxGateBody) + `"}}`

	start := time.Now()
	resp, err := http.Post(srv.URL+"/mcp-sse?sessionid=BIGINIT", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if dt := time.Since(start); dt > initWaitTimeout/2 {
		t.Fatalf("large initialize was gated: took %v", dt)
	}
}

// TestSSEInitGate_StopEndsSweepLoop proves the sweep goroutine is stoppable.
func TestSSEInitGate_StopEndsSweepLoop(t *testing.T) {
	g := newSSEInitGate()
	g.Stop()
	g.Stop() // idempotent
	select {
	case <-g.done:
	default:
		t.Fatal("done channel not closed by Stop")
	}
}

// TestSSEInitGate_SessionBoundToUser proves a leaked sessionid cannot be
// replayed by a different authenticated user.
func TestSSEInitGate_SessionBoundToUser(t *testing.T) {
	g := newSSEInitGate()
	defer g.Stop()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	h := g.wrap(inner)

	post := func(userID string) int {
		req := httptest.NewRequest(http.MethodPost, "/mcp-sse?sessionid=SHARED",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
		req = req.WithContext(srvpkg.WithUser(req.Context(), &store.User{ID: userID}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := post("owner"); code != http.StatusAccepted {
		t.Fatalf("owner request rejected: %d", code)
	}
	if code := post("owner"); code != http.StatusAccepted {
		t.Fatalf("owner second request rejected: %d", code)
	}
	if code := post("intruder"); code != http.StatusForbidden {
		t.Fatalf("expected 403 for a different user on the same session, got %d", code)
	}
}

// ---------- MCP transport wiring ----------

func TestIsMCPPath_ExactStreamablePath(t *testing.T) {
	cases := map[string]bool{
		"/mcp":          true,
		"/mcp/":         true,
		"/mcp/anything": true,
		"/mcp-sse":      true,
		"/mcp-sse/x":    true,
		"/api/logs":     false,
		"/mcpfoo":       false,
	}
	for path, want := range cases {
		if got := isMCPPath(path); got != want {
			t.Errorf("isMCPPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestMCPTokenInfoBridge_SetsUserID proves the SDK's session-hijack guard is
// no longer inert: TokenInfo carries the authenticated user's ID.
func TestMCPTokenInfoBridge_SetsUserID(t *testing.T) {
	var seen string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ti := auth.TokenInfoFromContext(r.Context()); ti != nil {
			seen = ti.UserID
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer tok")
	req = req.WithContext(srvpkg.WithUser(req.Context(), &store.User{ID: "user-1"}))
	rec := httptest.NewRecorder()
	mcpTokenInfoBridge(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if seen != "user-1" {
		t.Fatalf("TokenInfo.UserID = %q, want user-1", seen)
	}
}

// TestMCPTokenInfoBridge_NoUserRejected keeps the bridge failing closed.
func TestMCPTokenInfoBridge_NoUserRejected(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler ran without an authenticated user")
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	mcpTokenInfoBridge(inner).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// TestMCPRoute_MountsTokenInfoBridge pins the *wiring*, not just the helper:
// it builds the real router via NewServerWithDeps, pulls the middleware chain
// chi actually mounted on the /mcp route, and runs a probe through it. Deleting
// `r.Use(mcpTokenInfoBridge)` from server.go makes this fail, because the SDK's
// session-hijack guard is inert without TokenInfo in the context.
func TestMCPRoute_MountsTokenInfoBridge(t *testing.T) {
	mockUsers := newMockUserStore()
	token := "valid-mcp-token"
	user := &store.User{
		ID:         "mcp-user-1",
		Email:      "mcp@test.com",
		IsActive:   true,
		MCPEnabled: true,
		MCPToken:   &token,
	}
	mockUsers.mu.Lock()
	mockUsers.users[user.ID] = user
	mockUsers.mu.Unlock()

	srv := NewServerWithDeps(ServerDeps{
		Stores:   store.Stores{UserStore: mockUsers},
		Registry: connector.NewRegistry(),
		Cfg:      &config.Config{},
	})
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	chain := mcpRouteMiddlewares(t, srv.Router)
	if chain == nil {
		t.Fatal("no /mcp route found on the router")
	}

	var seen *auth.TokenInfo
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = auth.TokenInfoFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	chain.Handler(probe).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("mounted /mcp chain returned %d, want 200", rec.Code)
	}
	if seen == nil {
		t.Fatal("the /mcp route runs without mcpTokenInfoBridge: no auth.TokenInfo reached the MCP handler, so the SDK's session-user check is inert")
	}
	if seen.UserID != user.ID {
		t.Fatalf("TokenInfo.UserID = %q, want %q", seen.UserID, user.ID)
	}
}

// mcpRouteMiddlewares returns the middleware chain chi mounted on the /mcp
// subtree, as registered by the production router setup.
func mcpRouteMiddlewares(t *testing.T, router chi.Router) chi.Middlewares {
	t.Helper()
	var found chi.Middlewares
	walk := func(_ string, route string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		if found == nil && strings.HasPrefix(route, "/mcp/") {
			found = chi.Middlewares(mws)
		}
		return nil
	}
	if err := chi.Walk(router, walk); err != nil {
		t.Fatalf("walking router: %v", err)
	}
	return found
}

// ---------- audit drain ----------

// TestAuditWorker_DrainsAfterContextCancel proves queued audit entries are
// still written when the app lifecycle context is already canceled — the exact
// ordering main.go uses at shutdown.
func TestAuditWorker_DrainsAfterContextCancel(t *testing.T) {
	as := &recordingAuditStore{}
	ctx, cancel := context.WithCancel(context.Background())

	s := &Server{auditStore: as, auditCh: make(chan auditEntry, auditChannelBuffer), auditCtx: ctx}
	s.auditWg.Add(1)
	go s.auditWorker()

	for i := 0; i < 3; i++ {
		s.auditCh <- auditEntry{action: fmt.Sprintf("action-%d", i)}
	}

	cancel() // main cancels the app context before Shutdown
	close(s.auditCh)
	s.auditWg.Wait()

	if got := as.count(); got != 3 {
		t.Fatalf("expected 3 audit entries persisted after cancel, got %d", got)
	}
}

type recordingAuditStore struct {
	store.AuditStore
	mu      sync.Mutex
	entries []store.LogAuditParams
}

func (r *recordingAuditStore) Log(ctx context.Context, p store.LogAuditParams) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, p)
	return nil
}

func (r *recordingAuditStore) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}
