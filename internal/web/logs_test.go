package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opentrace/opentrace/internal/config"
	"github.com/opentrace/opentrace/internal/connector"
)

func setupTestServerWithLogStore() (*Server, *mockLogStore) {
	ms := newMockStore()
	ls := newMockLogStore()
	es := newMockEmbeddingStore()
	reg := connector.NewRegistry()
	srv := NewServer(ms, ls, es, reg, nil, nil)
	return srv, ls
}

func setupTestServerWithAPIKey(apiKey string) (*Server, *mockLogStore) {
	ms := newMockStore()
	ls := newMockLogStore()
	es := newMockEmbeddingStore()
	reg := connector.NewRegistry()
	cfg := &config.Config{APIKey: apiKey}
	srv := NewServer(ms, ls, es, reg, cfg, nil)
	return srv, ls
}

func TestIngestLogs_Success(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	body := `[
		{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","service":"api","message":"request received"},
		{"timestamp":"2024-01-01T00:00:01Z","level":"ERROR","service":"api","message":"db timeout"},
		{"timestamp":"2024-01-01T00:00:02Z","level":"WARN","service":"worker","message":"slow query"}
	]`
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp map[string]int
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"] != 3 {
		t.Fatalf("count = %d, want 3", resp["count"])
	}

	if len(ls.entries) != 3 {
		t.Fatalf("stored entries = %d, want 3", len(ls.entries))
	}
}

func TestIngestLogs_EmptyArray(t *testing.T) {
	srv, _ := setupTestServerWithLogStore()

	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString("[]"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]int
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"] != 0 {
		t.Fatalf("count = %d, want 0", resp["count"])
	}
}

func TestIngestLogs_InvalidJSON(t *testing.T) {
	srv, _ := setupTestServerWithLogStore()

	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString("{not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestIngestLogs_MissingFields(t *testing.T) {
	srv, _ := setupTestServerWithLogStore()

	// Missing timestamp and level
	body := `[{"message":"test"}]`
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestIngestLogs_SingleObject(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	body := `{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","service":"api","message":"single entry"}`
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp map[string]int
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"] != 1 {
		t.Fatalf("count = %d, want 1", resp["count"])
	}

	if len(ls.entries) != 1 {
		t.Fatalf("stored entries = %d, want 1", len(ls.entries))
	}
	if ls.entries[0].Message != "single entry" {
		t.Fatalf("message = %q, want %q", ls.entries[0].Message, "single entry")
	}
}

func TestIngestLogs_WithEnvironment(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	body := `{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","message":"hello","environment":"production"}`
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	if len(ls.entries) != 1 {
		t.Fatalf("stored entries = %d, want 1", len(ls.entries))
	}
	if ls.entries[0].Environment != "production" {
		t.Fatalf("environment = %q, want %q", ls.entries[0].Environment, "production")
	}
}

func TestAPIKeyAuth_ValidKey(t *testing.T) {
	srv, _ := setupTestServerWithAPIKey("test-secret")

	body := `{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","message":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-secret")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusCreated, w.Body.String())
	}
}

func TestAPIKeyAuth_InvalidKey(t *testing.T) {
	srv, _ := setupTestServerWithAPIKey("test-secret")

	body := `{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","message":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-key")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestAPIKeyAuth_MissingHeader(t *testing.T) {
	srv, _ := setupTestServerWithAPIKey("test-secret")

	body := `{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","message":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestAPIKeyAuth_Disabled(t *testing.T) {
	srv, _ := setupTestServerWithAPIKey("")

	body := `{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","message":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	// No auth header — should pass because APIKey is empty
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusCreated, w.Body.String())
	}
}
