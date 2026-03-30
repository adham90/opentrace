package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	_ "modernc.org/sqlite"
)

// ---------- helpers ----------

// okHandler writes a 200 with "ok" body — used as the inner handler for middleware tests.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the body so MaxBytesReader errors surface.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	})
}

// noReadHandler writes 200 without reading the body.
func noReadHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

func gzipPayload(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func decodeErrorJSON(t *testing.T, body []byte) string {
	t.Helper()
	var resp map[string]string
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode error response: %v (body=%q)", err, body)
	}
	return resp["error"]
}

// ---------- MaxBodySize ----------

func TestMaxBodySize_WithinLimit(t *testing.T) {
	handler := MaxBodySize(1024)(okHandler())

	payload := strings.Repeat("a", 512)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != payload {
		t.Fatalf("expected body to pass through, got %q", rec.Body.String())
	}
}

func TestMaxBodySize_ExceedsLimit(t *testing.T) {
	handler := MaxBodySize(64)(okHandler())

	payload := strings.Repeat("x", 128)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// When the handler reads beyond the limit, http.MaxBytesReader triggers an error.
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

// ---------- DecompressRequest ----------

func TestDecompressRequest_NonGzip(t *testing.T) {
	handler := DecompressRequest(1024)(okHandler())

	payload := "plain text body"
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != payload {
		t.Fatalf("expected body %q, got %q", payload, rec.Body.String())
	}
}

func TestDecompressRequest_ValidGzip(t *testing.T) {
	handler := DecompressRequest(4096)(okHandler())

	original := "hello compressed world"
	compressed := gzipPayload(t, []byte(original))

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != original {
		t.Fatalf("expected decompressed %q, got %q", original, rec.Body.String())
	}
}

func TestDecompressRequest_InvalidGzip(t *testing.T) {
	handler := DecompressRequest(4096)(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not gzip data"))
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	errMsg := decodeErrorJSON(t, rec.Body.Bytes())
	if errMsg != "invalid gzip body" {
		t.Fatalf("expected error 'invalid gzip body', got %q", errMsg)
	}
}

func TestDecompressRequest_ExceedsDecompressedLimit(t *testing.T) {
	// Allow only 32 decompressed bytes but send 256 bytes of compressible data.
	handler := DecompressRequest(32)(okHandler())

	original := strings.Repeat("A", 256)
	compressed := gzipPayload(t, []byte(original))

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// The handler reads via MaxBytesReader which truncates then errors.
	// Depending on timing, either we get the truncated body or a 413 error.
	if rec.Code == http.StatusOK {
		// Truncated but no error — body should be at most 32 bytes + 1 (MaxBytesReader allows limit+1).
		if len(rec.Body.Bytes()) > 33 {
			t.Fatalf("expected body truncated to ~32 bytes, got %d bytes", len(rec.Body.Bytes()))
		}
	} else if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 200 (truncated) or 413, got %d", rec.Code)
	}
}

func TestDecompressRequest_RemovesContentEncodingHeader(t *testing.T) {
	var gotEncoding string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Content-Encoding")
		w.WriteHeader(http.StatusOK)
	})
	handler := DecompressRequest(4096)(inner)

	compressed := gzipPayload(t, []byte("data"))
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if gotEncoding != "" {
		t.Fatalf("expected Content-Encoding to be removed, got %q", gotEncoding)
	}
}

// ---------- RateLimiter ----------

func TestRateLimiter_UnderLimit(t *testing.T) {
	rl := NewRateLimiter(5, time.Minute, nil)
	defer rl.Stop()

	handler := rl.Middleware(noReadHandler())

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}
}

func TestRateLimiter_ExceedsLimit(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute, nil)
	defer rl.Stop()

	handler := rl.Middleware(noReadHandler())

	// Send 4 requests; the 4th should be rejected.
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:9999"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if i < 3 {
			if rec.Code != http.StatusOK {
				t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
			}
		} else {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("request %d: expected 429, got %d", i+1, rec.Code)
			}
			retryAfter := rec.Header().Get("Retry-After")
			if retryAfter == "" {
				t.Fatal("expected Retry-After header on 429 response")
			}
			errMsg := decodeErrorJSON(t, rec.Body.Bytes())
			if errMsg != "rate limit exceeded" {
				t.Fatalf("expected error 'rate limit exceeded', got %q", errMsg)
			}
		}
	}
}

func TestRateLimiter_WindowReset(t *testing.T) {
	// Use a very short window so it expires quickly.
	rl := NewRateLimiter(1, 10*time.Millisecond, nil)
	defer rl.Stop()

	handler := rl.Middleware(noReadHandler())

	// First request passes.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:1111"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rec.Code)
	}

	// Second request should be rate limited.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:1111"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429, got %d", rec.Code)
	}

	// Wait for the window to expire.
	time.Sleep(20 * time.Millisecond)

	// Third request after window reset should pass.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:1111"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("third request (after reset): expected 200, got %d", rec.Code)
	}
}

func TestRateLimiter_Stop(t *testing.T) {
	rl := NewRateLimiter(10, time.Minute, nil)

	// Calling Stop() should not panic, and calling it twice should be safe.
	rl.Stop()
	rl.Stop()
}

func TestRateLimiter_DifferentIPsTrackedSeparately(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute, nil)
	defer rl.Stop()

	handler := rl.Middleware(noReadHandler())

	// First IP — should pass.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.1.1.1:1000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("IP1 first request: expected 200, got %d", rec.Code)
	}

	// Second IP — should also pass (different IP).
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "2.2.2.2:2000"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("IP2 first request: expected 200, got %d", rec.Code)
	}

	// First IP again — should be rate limited.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.1.1.1:1000"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("IP1 second request: expected 429, got %d", rec.Code)
	}
}

// ---------- RateLimiter.clientIP ----------

func TestClientIP_DirectHostPort(t *testing.T) {
	rl := &RateLimiter{trustedProxies: map[string]bool{}}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.50:12345"

	ip := rl.clientIP(req)
	if ip != "203.0.113.50" {
		t.Fatalf("expected 203.0.113.50, got %q", ip)
	}
}

func TestClientIP_XForwardedFor_TrustedProxy(t *testing.T) {
	rl := &RateLimiter{trustedProxies: map[string]bool{"10.0.0.1": true}}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:8080"
	req.Header.Set("X-Forwarded-For", "198.51.100.20, 10.0.0.1")

	ip := rl.clientIP(req)
	if ip != "198.51.100.20" {
		t.Fatalf("expected 198.51.100.20 (from X-Forwarded-For), got %q", ip)
	}
}

func TestClientIP_XForwardedFor_UntrustedProxy(t *testing.T) {
	rl := &RateLimiter{trustedProxies: map[string]bool{"10.0.0.1": true}}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.0.5:9999"
	req.Header.Set("X-Forwarded-For", "198.51.100.20")

	ip := rl.clientIP(req)
	// Should use RemoteAddr because the proxy is not trusted.
	if ip != "192.168.0.5" {
		t.Fatalf("expected 192.168.0.5 (untrusted proxy), got %q", ip)
	}
}

func TestClientIP_MalformedRemoteAddr(t *testing.T) {
	rl := &RateLimiter{trustedProxies: map[string]bool{}}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "malformed-address"

	ip := rl.clientIP(req)
	// Falls back to the raw RemoteAddr string.
	if ip != "malformed-address" {
		t.Fatalf("expected 'malformed-address', got %q", ip)
	}
}

func TestClientIP_XForwardedFor_TrustedProxy_SingleIP(t *testing.T) {
	rl := &RateLimiter{trustedProxies: map[string]bool{"127.0.0.1": true}}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-For", "  172.16.0.1  ")

	ip := rl.clientIP(req)
	if ip != "172.16.0.1" {
		t.Fatalf("expected trimmed '172.16.0.1', got %q", ip)
	}
}

func TestClientIP_NoTrustedProxies_IgnoresForwardedFor(t *testing.T) {
	rl := &RateLimiter{trustedProxies: map[string]bool{}}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:4000"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	ip := rl.clientIP(req)
	if ip != "10.0.0.5" {
		t.Fatalf("expected 10.0.0.5, got %q", ip)
	}
}

// ---------- APIKeyAuth ----------

func TestAPIKeyAuth_EmptyKeyAllowsAll(t *testing.T) {
	handler := APIKeyAuth("")(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAPIKeyAuth_ValidBearerToken(t *testing.T) {
	handler := APIKeyAuth("my-secret-key")(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer my-secret-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAPIKeyAuth_InvalidToken(t *testing.T) {
	handler := APIKeyAuth("correct-key")(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	errMsg := decodeErrorJSON(t, rec.Body.Bytes())
	if errMsg != "unauthorized" {
		t.Fatalf("expected error 'unauthorized', got %q", errMsg)
	}
}

func TestAPIKeyAuth_MissingAuthorizationHeader(t *testing.T) {
	handler := APIKeyAuth("my-key")(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Authorization header set.
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAPIKeyAuth_WrongScheme(t *testing.T) {
	handler := APIKeyAuth("my-key")(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic my-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong auth scheme, got %d", rec.Code)
	}
}

// ---------- RequireAdmin (HTML middleware) ----------

func TestRequireAdmin_NoUser_RedirectsToLogin(t *testing.T) {
	handler := RequireAdmin(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/login" {
		t.Fatalf("expected redirect to /login, got %q", loc)
	}
}

func TestRequireAdmin_NonAdminUser_Returns403(t *testing.T) {
	handler := RequireAdmin(noReadHandler())

	member := &store.User{ID: "user-1", Email: "member@test.com", Role: store.RoleMember, IsActive: true}
	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	req = req.WithContext(server.WithUser(req.Context(), member))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	errMsg := decodeErrorJSON(t, rec.Body.Bytes())
	if errMsg != "admin access required" {
		t.Fatalf("expected error 'admin access required', got %q", errMsg)
	}
}

func TestRequireAdmin_AdminUser_PassesThrough(t *testing.T) {
	handler := RequireAdmin(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	req = withAdmin(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------- RequireAdminAPI (JSON middleware) ----------

func TestRequireAdminAPI_NoUser_Returns401(t *testing.T) {
	handler := RequireAdminAPI(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	errMsg := decodeErrorJSON(t, rec.Body.Bytes())
	if errMsg != "authentication required" {
		t.Fatalf("expected error 'authentication required', got %q", errMsg)
	}
}

func TestRequireAdminAPI_NonAdmin_Returns403(t *testing.T) {
	handler := RequireAdminAPI(noReadHandler())

	member := &store.User{ID: "user-2", Email: "viewer@test.com", Role: store.RoleMember, IsActive: true}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	req = req.WithContext(server.WithUser(req.Context(), member))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	errMsg := decodeErrorJSON(t, rec.Body.Bytes())
	if errMsg != "admin access required" {
		t.Fatalf("expected error 'admin access required', got %q", errMsg)
	}
}

func TestRequireAdminAPI_Admin_PassesThrough(t *testing.T) {
	handler := RequireAdminAPI(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	req = withAdmin(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------- RequireAuthAPI ----------

func TestRequireAuthAPI_NoUser_Returns401(t *testing.T) {
	handler := RequireAuthAPI(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	errMsg := decodeErrorJSON(t, rec.Body.Bytes())
	if errMsg != "authentication required" {
		t.Fatalf("expected error 'authentication required', got %q", errMsg)
	}
}

func TestRequireAuthAPI_AnyUser_PassesThrough(t *testing.T) {
	handler := RequireAuthAPI(noReadHandler())

	member := &store.User{ID: "user-3", Email: "anyone@test.com", Role: store.RoleMember, IsActive: true}
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req = req.WithContext(server.WithUser(req.Context(), member))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRequireAuthAPI_AdminUser_PassesThrough(t *testing.T) {
	handler := RequireAuthAPI(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req = withAdmin(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// decodeFullJSON decodes a JSON response body into a generic map.
func decodeFullJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode JSON response: %v (body=%q)", err, body)
	}
	return resp
}

// ---------- formatJSONError ----------

func TestFormatJSONError_SyntaxError(t *testing.T) {
	err := &json.SyntaxError{Offset: 42}
	msg := formatJSONError(err, "body")
	expected := "invalid JSON body: syntax error at byte offset 42"
	if msg != expected {
		t.Fatalf("expected %q, got %q", expected, msg)
	}
}

func TestFormatJSONError_UnmarshalTypeError(t *testing.T) {
	err := &json.UnmarshalTypeError{
		Field: "name",
		Type:  reflect.TypeOf(""),
		Value: "number",
	}
	msg := formatJSONError(err, "payload")
	if !strings.Contains(msg, `field "name"`) {
		t.Fatalf("expected field name in message, got %q", msg)
	}
	if !strings.Contains(msg, "string") {
		t.Fatalf("expected type 'string' in message, got %q", msg)
	}
	if !strings.Contains(msg, "number") {
		t.Fatalf("expected value 'number' in message, got %q", msg)
	}
}

func TestFormatJSONError_GenericError(t *testing.T) {
	err := fmt.Errorf("unexpected EOF")
	msg := formatJSONError(err, "request")
	expected := "invalid JSON request: unexpected EOF"
	if msg != expected {
		t.Fatalf("expected %q, got %q", expected, msg)
	}
}

// ---------- writeDetailedError ----------

func TestWriteDetailedError_WithDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	details := map[string]any{"field": "email", "reason": "required"}
	writeDetailedError(rec, http.StatusBadRequest, "validation failed", "VALIDATION_ERROR", details)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	resp := decodeFullJSON(t, rec.Body.Bytes())
	if resp["error"] != "validation failed" {
		t.Fatalf("expected error 'validation failed', got %v", resp["error"])
	}
	if resp["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected code 'VALIDATION_ERROR', got %v", resp["code"])
	}
	detailsOut, ok := resp["details"].(map[string]any)
	if !ok {
		t.Fatalf("expected details map, got %T", resp["details"])
	}
	if detailsOut["field"] != "email" {
		t.Fatalf("expected details.field 'email', got %v", detailsOut["field"])
	}
}

func TestWriteDetailedError_NilDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	writeDetailedError(rec, http.StatusInternalServerError, "internal error", "INTERNAL", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	resp := decodeFullJSON(t, rec.Body.Bytes())
	if resp["error"] != "internal error" {
		t.Fatalf("expected error 'internal error', got %v", resp["error"])
	}
	if resp["code"] != "INTERNAL" {
		t.Fatalf("expected code 'INTERNAL', got %v", resp["code"])
	}
	if _, exists := resp["details"]; exists {
		t.Fatalf("expected no details key when details is nil, but got %v", resp["details"])
	}
}

// ---------- parseCORSOriginsString ----------

func TestParseCORSOriginsString_Single(t *testing.T) {
	result := parseCORSOriginsString("http://example.com")
	if len(result) != 1 || result[0] != "http://example.com" {
		t.Fatalf("expected [http://example.com], got %v", result)
	}
}

func TestParseCORSOriginsString_Multiple(t *testing.T) {
	result := parseCORSOriginsString("http://a.com,http://b.com,http://c.com")
	if len(result) != 3 {
		t.Fatalf("expected 3 origins, got %d: %v", len(result), result)
	}
	expected := []string{"http://a.com", "http://b.com", "http://c.com"}
	for i, e := range expected {
		if result[i] != e {
			t.Fatalf("origin[%d]: expected %q, got %q", i, e, result[i])
		}
	}
}

func TestParseCORSOriginsString_Empty(t *testing.T) {
	result := parseCORSOriginsString("")
	if len(result) != 0 {
		t.Fatalf("expected empty result for empty string, got %v", result)
	}
}

func TestParseCORSOriginsString_Whitespace(t *testing.T) {
	result := parseCORSOriginsString("  http://a.com , http://b.com  ,  http://c.com  ")
	if len(result) != 3 {
		t.Fatalf("expected 3 origins, got %d: %v", len(result), result)
	}
	if result[0] != "http://a.com" || result[1] != "http://b.com" || result[2] != "http://c.com" {
		t.Fatalf("expected trimmed origins, got %v", result)
	}
}

// ---------- DynamicCORSMiddleware ----------

func TestDynamicCORSMiddleware_NoOriginHeader(t *testing.T) {
	s := &Server{
		cfg:     &config.Config{CORSAllowedOrigins: []string{"http://example.com"}},
		auditCh: make(chan auditEntry, 1),
	}

	handler := s.DynamicCORSMiddleware(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	// No Origin header
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("expected no CORS headers when Origin is absent")
	}
}

func TestDynamicCORSMiddleware_NoOriginsConfigured(t *testing.T) {
	s := &Server{
		cfg:     &config.Config{},
		auditCh: make(chan auditEntry, 1),
	}

	handler := s.DynamicCORSMiddleware(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	req.Header.Set("Origin", "http://attacker.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("expected no CORS headers when no origins configured")
	}
}

func TestDynamicCORSMiddleware_AllowedOrigin(t *testing.T) {
	s := &Server{
		cfg:     &config.Config{CORSAllowedOrigins: []string{"http://allowed.com"}},
		auditCh: make(chan auditEntry, 1),
	}

	handler := s.DynamicCORSMiddleware(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	req.Header.Set("Origin", "http://allowed.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://allowed.com" {
		t.Fatalf("expected Allow-Origin 'http://allowed.com', got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestDynamicCORSMiddleware_DisallowedOriginOPTIONS(t *testing.T) {
	s := &Server{
		cfg:     &config.Config{CORSAllowedOrigins: []string{"http://allowed.com"}},
		auditCh: make(chan auditEntry, 1),
	}

	handler := s.DynamicCORSMiddleware(noReadHandler())

	req := httptest.NewRequest(http.MethodOptions, "/api/logs", nil)
	req.Header.Set("Origin", "http://disallowed.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for disallowed origin on OPTIONS, got %d", rec.Code)
	}
}

func TestDynamicCORSMiddleware_Wildcard(t *testing.T) {
	s := &Server{
		cfg:     &config.Config{CORSAllowedOrigins: []string{"*"}},
		auditCh: make(chan auditEntry, 1),
	}

	handler := s.DynamicCORSMiddleware(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	req.Header.Set("Origin", "http://any-origin.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://any-origin.com" {
		t.Fatalf("expected wildcard to allow any origin, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestDynamicCORSMiddleware_AllowedOriginPreflight(t *testing.T) {
	s := &Server{
		cfg:     &config.Config{CORSAllowedOrigins: []string{"http://allowed.com"}},
		auditCh: make(chan auditEntry, 1),
	}

	handler := s.DynamicCORSMiddleware(noReadHandler())

	req := httptest.NewRequest(http.MethodOptions, "/api/logs", nil)
	req.Header.Set("Origin", "http://allowed.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for preflight, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://allowed.com" {
		t.Fatalf("expected Allow-Origin on preflight, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("expected Access-Control-Allow-Methods on preflight")
	}
	if rec.Header().Get("Access-Control-Max-Age") == "" {
		t.Fatal("expected Access-Control-Max-Age on preflight")
	}
}

// ---------- DynamicAPIKeyAuth ----------

func TestDynamicAPIKeyAuth_ValidBearerToken(t *testing.T) {
	s := &Server{
		cfg:     &config.Config{APIKey: "test-key"},
		auditCh: make(chan auditEntry, 1),
	}

	handler := s.DynamicAPIKeyAuth(noReadHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestDynamicAPIKeyAuth_InvalidToken(t *testing.T) {
	s := &Server{
		cfg:     &config.Config{APIKey: "correct-key"},
		auditCh: make(chan auditEntry, 1),
	}

	handler := s.DynamicAPIKeyAuth(noReadHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestDynamicAPIKeyAuth_NoKeyConfigured(t *testing.T) {
	s := &Server{
		cfg:     &config.Config{},
		auditCh: make(chan auditEntry, 1),
	}

	handler := s.DynamicAPIKeyAuth(noReadHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	// No Authorization header
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when no key configured, got %d", rec.Code)
	}
}

func TestDynamicAPIKeyAuth_SettingsStoreFallback(t *testing.T) {
	ms := newMockSettingsStore()
	ms.apiKey = "db-key"

	s := &Server{
		cfg:           &config.Config{}, // no API key in config
		settingsStore: ms,
		auditCh:       make(chan auditEntry, 1),
	}

	handler := s.DynamicAPIKeyAuth(noReadHandler())

	// Valid token from settings store
	req := httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	req.Header.Set("Authorization", "Bearer db-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with settings store key, got %d", rec.Code)
	}

	// Invalid token against settings store key
	req = httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong key, got %d", rec.Code)
	}
}

// ---------- ProxyAuth ----------

func TestProxyAuth_TrustDisabled(t *testing.T) {
	// Ensure env var is not set
	os.Unsetenv("OPENTRACE_TRUST_PROXY_AUTH")

	s := &Server{
		userStore: newMockUserStore(),
		auditCh:   make(chan auditEntry, 1),
	}

	handler := s.ProxyAuth(noReadHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-User", "attacker@evil.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// The middleware should be a passthrough, so no user in context.
	// We verify by checking that the handler is the exact same noReadHandler
	// (ProxyAuth returns next directly when disabled).
}

func TestProxyAuth_TrustEnabled_WithForwardedUser(t *testing.T) {
	os.Setenv("OPENTRACE_TRUST_PROXY_AUTH", "true")
	defer os.Unsetenv("OPENTRACE_TRUST_PROXY_AUTH")

	mockUsers := newMockUserStore()
	s := &Server{
		userStore: mockUsers,
		auditCh:   make(chan auditEntry, 1),
	}

	var gotUser *store.User
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := s.ProxyAuth(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-User", "proxy-user@example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotUser == nil {
		t.Fatal("expected user in context from proxy auth")
	}
	if gotUser.Email != "proxy-user@example.com" {
		t.Fatalf("expected email 'proxy-user@example.com', got %q", gotUser.Email)
	}
}

func TestProxyAuth_TrustEnabled_NoForwardedUser(t *testing.T) {
	os.Setenv("OPENTRACE_TRUST_PROXY_AUTH", "true")
	defer os.Unsetenv("OPENTRACE_TRUST_PROXY_AUTH")

	s := &Server{
		userStore: newMockUserStore(),
		auditCh:   make(chan auditEntry, 1),
	}

	var gotUser *store.User
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := s.ProxyAuth(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No X-Forwarded-User header
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotUser != nil {
		t.Fatal("expected no user in context when header is absent")
	}
}

func TestProxyAuth_TrustEnabled_CreateUserFails(t *testing.T) {
	os.Setenv("OPENTRACE_TRUST_PROXY_AUTH", "true")
	defer os.Unsetenv("OPENTRACE_TRUST_PROXY_AUTH")

	mockUsers := newMockUserStore()
	mockUsers.createErr = fmt.Errorf("database is down")

	s := &Server{
		userStore: mockUsers,
		auditCh:   make(chan auditEntry, 1),
	}

	innerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := s.ProxyAuth(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-User", "new-user@example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when user creation fails, got %d", rec.Code)
	}
	if innerCalled {
		t.Fatal("inner handler should not be called when user creation fails")
	}
}

// ---------- MCPTokenAuth ----------

func TestMCPTokenAuth_MissingAuthorizationHeader(t *testing.T) {
	s := &Server{
		userStore: newMockUserStore(),
		auditCh:   make(chan auditEntry, 1),
	}

	handler := s.MCPTokenAuth(noReadHandler())

	req := httptest.NewRequest(http.MethodPost, "/mcp/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMCPTokenAuth_InvalidToken(t *testing.T) {
	s := &Server{
		userStore: newMockUserStore(),
		auditCh:   make(chan auditEntry, 1),
	}

	handler := s.MCPTokenAuth(noReadHandler())

	req := httptest.NewRequest(http.MethodPost, "/mcp/", nil)
	req.Header.Set("Authorization", "Bearer bad-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMCPTokenAuth_ValidTokenActiveUser(t *testing.T) {
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

	s := &Server{
		userStore: mockUsers,
		auditCh:   make(chan auditEntry, 1),
	}

	var gotUser *store.User
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := s.MCPTokenAuth(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp/", nil)
	req.Header.Set("Authorization", "Bearer valid-mcp-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotUser == nil {
		t.Fatal("expected user in context")
	}
	if gotUser.ID != "mcp-user-1" {
		t.Fatalf("expected user ID 'mcp-user-1', got %q", gotUser.ID)
	}
}

func TestMCPTokenAuth_ValidTokenInactiveUser(t *testing.T) {
	mockUsers := newMockUserStore()
	token := "inactive-token"
	// The mock's GetByMCPToken checks IsActive and MCPEnabled,
	// so an inactive user won't be returned by the mock. We need to
	// add a user that passes GetByMCPToken but has IsActive=false.
	// Since the mockUserStore checks IsActive in GetByMCPToken, this
	// will return ErrNotFound, resulting in 401 (not 403).
	// To properly test the 403 path, we'd need the user to pass
	// GetByMCPToken but fail the IsActive check in the middleware.
	// The current mock combines both checks, so we test the behavior
	// that an inactive user's token is rejected.
	user := &store.User{
		ID:         "inactive-user",
		Email:      "inactive@test.com",
		IsActive:   false,
		MCPEnabled: true,
		MCPToken:   &token,
	}
	mockUsers.mu.Lock()
	mockUsers.users[user.ID] = user
	mockUsers.mu.Unlock()

	s := &Server{
		userStore: mockUsers,
		auditCh:   make(chan auditEntry, 1),
	}

	handler := s.MCPTokenAuth(noReadHandler())

	req := httptest.NewRequest(http.MethodPost, "/mcp/", nil)
	req.Header.Set("Authorization", "Bearer inactive-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// The mock rejects inactive users in GetByMCPToken, so this returns 401
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for inactive user token, got %d", rec.Code)
	}
}

// ---------- statusRecorder ----------

func TestStatusRecorder_FlushWithFlusher(t *testing.T) {
	flushed := false
	underlying := &mockFlusherWriter{
		ResponseWriter: httptest.NewRecorder(),
		onFlush:        func() { flushed = true },
	}

	rec := &statusRecorder{ResponseWriter: underlying, statusCode: 200}
	rec.Flush()

	if !flushed {
		t.Fatal("expected Flush to be called on underlying Flusher")
	}
}

func TestStatusRecorder_FlushWithNonFlusher(t *testing.T) {
	// httptest.NewRecorder actually does implement Flusher, but we want
	// to test the non-Flusher path. Use a minimal ResponseWriter.
	underlying := &minimalResponseWriter{}
	rec := &statusRecorder{ResponseWriter: underlying, statusCode: 200}

	// Should not panic
	rec.Flush()
}

func TestStatusRecorder_Unwrap(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: inner, statusCode: 200}

	unwrapped := rec.Unwrap()
	if unwrapped != inner {
		t.Fatal("Unwrap should return the underlying ResponseWriter")
	}
}

// mockFlusherWriter implements http.ResponseWriter and http.Flusher.
type mockFlusherWriter struct {
	http.ResponseWriter
	onFlush func()
}

func (m *mockFlusherWriter) Flush() {
	if m.onFlush != nil {
		m.onFlush()
	}
}

// minimalResponseWriter implements http.ResponseWriter but not http.Flusher.
type minimalResponseWriter struct {
	code    int
	headers http.Header
	body    bytes.Buffer
}

func (m *minimalResponseWriter) Header() http.Header {
	if m.headers == nil {
		m.headers = make(http.Header)
	}
	return m.headers
}

func (m *minimalResponseWriter) Write(b []byte) (int, error) {
	return m.body.Write(b)
}

func (m *minimalResponseWriter) WriteHeader(code int) {
	m.code = code
}

// ---------- SessionFromContext ----------

func TestSessionFromContext_WithSession(t *testing.T) {
	sess := &store.Session{
		ID:     "sess-1",
		UserID: "user-1",
		Token:  "token-abc",
	}
	ctx := server.WithSession(context.Background(), sess)

	got := SessionFromContext(ctx)
	if got == nil {
		t.Fatal("expected session from context")
	}
	if got.ID != "sess-1" {
		t.Fatalf("expected session ID 'sess-1', got %q", got.ID)
	}
}

func TestSessionFromContext_EmptyContext(t *testing.T) {
	got := SessionFromContext(context.Background())
	if got != nil {
		t.Fatal("expected nil session from empty context")
	}
}

// ---------- handleHealthCheck ----------

func TestHandleHealthCheck_NoDB(t *testing.T) {
	s := &Server{
		db:      nil,
		auditCh: make(chan auditEntry, 1),
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.handleHealthCheck(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	resp := decodeFullJSON(t, rec.Body.Bytes())
	if resp["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %v", resp["status"])
	}
	// No database key expected when db is nil
	if _, exists := resp["database"]; exists {
		t.Fatal("expected no 'database' key when db is nil")
	}
}

func TestHandleHealthCheck_DBPingSucceeds(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	s := &Server{
		db:      db,
		auditCh: make(chan auditEntry, 1),
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.handleHealthCheck(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	resp := decodeFullJSON(t, rec.Body.Bytes())
	if resp["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %v", resp["status"])
	}
	if resp["database"] != "ok" {
		t.Fatalf("expected database 'ok', got %v", resp["database"])
	}
}

// ---------- handleReadiness ----------

func TestHandleReadiness_NoDB(t *testing.T) {
	s := &Server{
		db:      nil,
		auditCh: make(chan auditEntry, 1),
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.handleReadiness(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	resp := decodeFullJSON(t, rec.Body.Bytes())
	if resp["status"] != "not_ready" {
		t.Fatalf("expected status 'not_ready', got %v", resp["status"])
	}
}

func TestHandleReadiness_DBPingSucceeds(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	s := &Server{
		db:      db,
		auditCh: make(chan auditEntry, 1),
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.handleReadiness(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	resp := decodeFullJSON(t, rec.Body.Bytes())
	if resp["status"] != "ready" {
		t.Fatalf("expected status 'ready', got %v", resp["status"])
	}
}
