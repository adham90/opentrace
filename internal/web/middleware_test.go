package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSMiddleware_NoOrigins(t *testing.T) {
	handler := CORSMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no CORS header when allowedOrigins is nil")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestCORSMiddleware_AllowAll(t *testing.T) {
	handler := CORSMiddleware([]string{"*"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	req.Header.Set("Origin", "https://anything.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://anything.example.com" {
		t.Errorf("expected origin echo, got %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got != "POST, GET, OPTIONS" {
		t.Errorf("unexpected methods: %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("expected allow-headers to be set")
	}
}

func TestCORSMiddleware_SpecificOrigins(t *testing.T) {
	handler := CORSMiddleware([]string{"https://app.example.com", "https://staging.example.com"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	tests := []struct {
		name          string
		origin        string
		expectAllowed bool
	}{
		{"allowed origin", "https://app.example.com", true},
		{"allowed staging", "https://staging.example.com", true},
		{"disallowed origin", "https://evil.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/logs", nil)
			req.Header.Set("Origin", tt.origin)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			got := rr.Header().Get("Access-Control-Allow-Origin")
			if tt.expectAllowed && got != tt.origin {
				t.Errorf("expected %q, got %q", tt.origin, got)
			}
			if !tt.expectAllowed && got != "" {
				t.Errorf("expected no CORS header for disallowed origin, got %q", got)
			}
		})
	}
}

func TestCORSMiddleware_Preflight(t *testing.T) {
	innerCalled := false
	handler := CORSMiddleware([]string{"*"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/logs", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204 for preflight, got %d", rr.Code)
	}
	if innerCalled {
		t.Error("inner handler should not be called for preflight")
	}
	if got := rr.Header().Get("Access-Control-Max-Age"); got != "86400" {
		t.Errorf("expected max-age 86400, got %q", got)
	}
}

func TestCORSMiddleware_NoOriginHeader(t *testing.T) {
	innerCalled := false
	handler := CORSMiddleware([]string{"*"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	// No Origin header — same-origin request
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !innerCalled {
		t.Error("inner handler should be called for same-origin requests")
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no CORS header for same-origin, got %q", got)
	}
}

func TestCORSMiddleware_VaryHeader(t *testing.T) {
	handler := CORSMiddleware([]string{"https://app.example.com"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Vary"); got != "Origin" {
		t.Errorf("expected Vary: Origin, got %q", got)
	}
}
