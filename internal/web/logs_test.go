package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opentrace/opentrace/internal/config"
	"github.com/opentrace/opentrace/internal/connector"
	"github.com/opentrace/opentrace/internal/store"
)

func setupTestServerWithLogStore() (*Server, *mockLogStore) {
	ms := newMockStore()
	ls := newMockLogStore()
	es := newMockEmbeddingStore()
	reg := connector.NewRegistry()
	srv := NewServer(ms, ls, es, nil, nil, reg, nil, nil, nil, "")
	return srv, ls
}

func setupTestServerFull() (*Server, *mockDataSourceStore, *mockLogStore, *connector.Registry) {
	ms := newMockStore()
	ls := newMockLogStore()
	es := newMockEmbeddingStore()
	reg := connector.NewRegistry()
	srv := NewServer(ms, ls, es, nil, nil, reg, nil, nil, nil, "")
	return srv, ms, ls, reg
}

func setupTestServerWithAPIKey(apiKey string) (*Server, *mockLogStore) {
	ms := newMockStore()
	ls := newMockLogStore()
	es := newMockEmbeddingStore()
	reg := connector.NewRegistry()
	cfg := &config.Config{APIKey: apiKey}
	srv := NewServer(ms, ls, es, nil, nil, reg, cfg, nil, nil, "")
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

func TestLogsPage_Renders(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	// Seed a log entry
	ls.entries = []store.LogEntry{
		{
			ID:          1,
			Timestamp:   time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
			Level:       "INFO",
			Service:     "api",
			Environment: "production",
			Message:     "request received",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	body := w.Body.String()
	if !strings.Contains(body, "request received") {
		t.Error("expected body to contain log message")
	}
	if !strings.Contains(body, "INFO") {
		t.Error("expected body to contain log level")
	}
	if !strings.Contains(body, "1 entries") {
		t.Error("expected body to contain entry count")
	}
}

func TestLogsPage_WithFilters(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	req := httptest.NewRequest(http.MethodGet, "/logs?level=ERROR&service=api&environment=staging&query=timeout", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	ls.mu.Lock()
	params := ls.lastSearchParams
	ls.mu.Unlock()

	if params.Level != "ERROR" {
		t.Errorf("level = %q, want %q", params.Level, "ERROR")
	}
	if params.Service != "api" {
		t.Errorf("service = %q, want %q", params.Service, "api")
	}
	if params.Environment != "staging" {
		t.Errorf("environment = %q, want %q", params.Environment, "staging")
	}
	if params.Query != "timeout" {
		t.Errorf("query = %q, want %q", params.Query, "timeout")
	}
	if params.Limit != 100 {
		t.Errorf("limit = %d, want %d", params.Limit, 100)
	}
}

func TestIngestLogs_AutoCreatesLogsConnector(t *testing.T) {
	srv, ms, _, reg := setupTestServerFull()

	body := `{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","message":"auto-register test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	// Verify connector is registered in the registry
	if reg.Get(connector.ConnectorLogs) == nil {
		t.Fatal("expected logs connector to be registered in registry")
	}

	// Verify data source row was created in the store
	ms.mu.Lock()
	defer ms.mu.Unlock()
	var found bool
	for _, ds := range ms.sources {
		if ds.Type == store.ConnectorLogs {
			found = true
			if ds.Name != "Log Ingestion" {
				t.Errorf("data source name = %q, want %q", ds.Name, "Log Ingestion")
			}
			if ds.Status != store.StatusConnected {
				t.Errorf("data source status = %q, want %q", ds.Status, store.StatusConnected)
			}
			break
		}
	}
	if !found {
		t.Fatal("expected logs data source row in store")
	}
}

func TestIngestLogs_AutoCreate_AlreadyExists(t *testing.T) {
	srv, ms, _, reg := setupTestServerFull()

	// Pre-register a logs connector
	preExisting := connector.NewLogsConnector(newMockLogStore())
	reg.Register(preExisting)

	body := `{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","message":"already exists test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	// Registry should still have the connector
	if reg.Get(connector.ConnectorLogs) == nil {
		t.Fatal("expected logs connector to remain registered")
	}

	// No data source row should have been created (fast path: already registered)
	ms.mu.Lock()
	defer ms.mu.Unlock()
	for _, ds := range ms.sources {
		if ds.Type == store.ConnectorLogs {
			t.Fatal("expected no new data source row when connector already registered")
		}
	}
}

func TestLogsPage_HTMXFragment(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	ls.entries = []store.LogEntry{
		{
			ID:        1,
			Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
			Level:     "ERROR",
			Service:   "worker",
			Message:   "connection refused",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	body := w.Body.String()
	// HTMX fragment should NOT contain the full layout (no <html> tag)
	if strings.Contains(body, "<html") {
		t.Error("HTMX response should not contain full HTML layout")
	}
	// Should contain the log entry
	if !strings.Contains(body, "connection refused") {
		t.Error("expected HTMX fragment to contain log message")
	}
	if !strings.Contains(body, "ERROR") {
		t.Error("expected HTMX fragment to contain log level")
	}
}
