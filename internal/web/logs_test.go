package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

func setupTestServerWithLogStore() (*Server, *mockLogStore) {
	ms := newMockStore()
	ls := newMockLogStore()
	reg := connector.NewRegistry()
	srv := NewServer(ms, ls, reg, nil)
	return srv, ls
}

func setupTestServerFull() (*Server, *mockDataSourceStore, *mockLogStore, *connector.Registry) {
	ms := newMockStore()
	ls := newMockLogStore()
	reg := connector.NewRegistry()
	srv := NewServer(ms, ls, reg, nil)
	return srv, ms, ls, reg
}

func setupTestServerWithAPIKey(apiKey string) (*Server, *mockLogStore) {
	ms := newMockStore()
	ls := newMockLogStore()
	reg := connector.NewRegistry()
	cfg := &config.Config{APIKey: apiKey}
	srv := NewServer(ms, ls, reg, cfg)
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
	if params.Limit != 51 {
		t.Errorf("limit = %d, want %d", params.Limit, 51)
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

func TestIngestLogs_BatchDedup(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	body := `[{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","service":"api","message":"dedup test"}]`

	// First request with X-Batch-ID — should insert
	req1 := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-Batch-ID", "batch-uuid-123")
	w1 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusCreated {
		t.Fatalf("first request: status = %d, want %d. Body: %s", w1.Code, http.StatusCreated, w1.Body.String())
	}
	if len(ls.entries) != 1 {
		t.Fatalf("first request: stored entries = %d, want 1", len(ls.entries))
	}

	// Second request with same X-Batch-ID — should be deduplicated
	req2 := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Batch-ID", "batch-uuid-123")
	w2 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("second request: status = %d, want %d. Body: %s", w2.Code, http.StatusOK, w2.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w2.Body).Decode(&resp)
	if resp["deduplicated"] != true {
		t.Fatalf("expected deduplicated=true, got %v", resp["deduplicated"])
	}

	// Should still have only 1 entry (not duplicated)
	if len(ls.entries) != 1 {
		t.Fatalf("after dedup: stored entries = %d, want 1", len(ls.entries))
	}
}

func TestIngestLogs_NoBatchID(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	body := `[{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","service":"api","message":"no batch id"}]`

	// Two requests without X-Batch-ID — both should insert (no dedup)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.Router.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("request %d: status = %d, want %d", i+1, w.Code, http.StatusCreated)
		}
	}

	if len(ls.entries) != 2 {
		t.Fatalf("stored entries = %d, want 2", len(ls.entries))
	}
}

func TestIngestLogs_GzipCompressed(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	body := `[
		{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","service":"api","message":"gzip test 1"},
		{"timestamp":"2024-01-01T00:00:01Z","level":"WARN","service":"api","message":"gzip test 2"}
	]`

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte(body))
	gz.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/logs", &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp map[string]int
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"] != 2 {
		t.Fatalf("count = %d, want 2", resp["count"])
	}

	if len(ls.entries) != 2 {
		t.Fatalf("stored entries = %d, want 2", len(ls.entries))
	}
	if ls.entries[0].Message != "gzip test 1" {
		t.Fatalf("message = %q, want %q", ls.entries[0].Message, "gzip test 1")
	}
}

func TestIngestLogs_InvalidGzip(t *testing.T) {
	srv, _ := setupTestServerWithLogStore()

	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewBufferString("not gzip data"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestIngestLogs_WithRequestSummary(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	body := `{
		"timestamp":"2024-01-01T00:00:00Z",
		"level":"INFO",
		"service":"billing-api",
		"message":"GET /invoices 200 45.2ms",
		"metadata":{"request_id":"req-abc-123","user_id":42},
		"request_summary":{
			"controller":"InvoicesController",
			"action":"index",
			"method":"GET",
			"path":"/invoices",
			"status":200,
			"duration_ms":45.2,
			"sql_count":3,
			"sql_total_ms":12.1,
			"sql_slowest_ms":8.2,
			"sql_slowest_name":"Invoice Load",
			"n_plus_one":false,
			"view_count":2,
			"view_total_ms":28.3,
			"cache_reads":1,
			"cache_hits":1,
			"cache_hit_ratio":1.0,
			"timeline":[{"t":"sql","n":"Invoice Load","ms":8.2,"at":2.0}]
		}
	}`
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

	entry := ls.entries[0]
	if entry.RequestSummary == nil {
		t.Fatal("expected request_summary to be set on stored entry")
	}

	rs := entry.RequestSummary
	if rs.Controller != "InvoicesController" {
		t.Errorf("controller = %q, want %q", rs.Controller, "InvoicesController")
	}
	if rs.Action != "index" {
		t.Errorf("action = %q, want %q", rs.Action, "index")
	}
	if rs.Method != "GET" {
		t.Errorf("method = %q, want %q", rs.Method, "GET")
	}
	if rs.SQLCount != 3 {
		t.Errorf("sql_count = %d, want 3", rs.SQLCount)
	}
	if rs.DurationMs != 45.2 {
		t.Errorf("duration_ms = %f, want 45.2", rs.DurationMs)
	}
	if rs.CacheHitRatio != 1.0 {
		t.Errorf("cache_hit_ratio = %f, want 1.0", rs.CacheHitRatio)
	}
	if rs.Timeline == "" {
		t.Error("expected timeline to be set")
	}

	// Metadata should still have request_id and user_id, but NOT controller/action
	meta := entry.Metadata
	if meta["request_id"] != "req-abc-123" {
		t.Errorf("metadata.request_id = %v, want req-abc-123", meta["request_id"])
	}
	if meta["user_id"] != float64(42) {
		t.Errorf("metadata.user_id = %v, want 42", meta["user_id"])
	}
}

func TestIngestLogs_WithoutRequestSummary(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	body := `{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","message":"no summary"}`
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
	if ls.entries[0].RequestSummary != nil {
		t.Fatal("expected request_summary to be nil when not provided")
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

func TestHandleVersion_IncludesAPIVersionFields(t *testing.T) {
	srv, _ := setupTestServerWithLogStore()

	req := httptest.NewRequest("GET", "/api/version", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["api_version"] == nil {
		t.Error("expected api_version in response")
	}
	if int(resp["api_version"].(float64)) != APIVersion {
		t.Errorf("api_version = %v, want %d", resp["api_version"], APIVersion)
	}

	if resp["min_client_api_version"] == nil {
		t.Error("expected min_client_api_version in response")
	}
	if int(resp["min_client_api_version"].(float64)) != MinClientAPIVersion {
		t.Errorf("min_client_api_version = %v, want %d", resp["min_client_api_version"], MinClientAPIVersion)
	}

	caps, ok := resp["capabilities"].([]any)
	if !ok || len(caps) == 0 {
		t.Fatal("expected non-empty capabilities array")
	}

	capStrings := make([]string, len(caps))
	for i, c := range caps {
		capStrings[i] = c.(string)
	}

	expected := []string{"batch_ingest", "gzip_request", "batch_dedup", "request_summaries"}
	for _, e := range expected {
		found := false
		for _, c := range capStrings {
			if c == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected capability %q in response, got %v", e, capStrings)
		}
	}
}

func TestIngestLogs_RejectsOldAPIVersion(t *testing.T) {
	srv, _ := setupTestServerWithLogStore()

	body := `[{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","service":"api","message":"test"}]`
	req := httptest.NewRequest("POST", "/api/logs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("X-API-Version", "0") // Below minimum

	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}

	if w.Header().Get("X-API-Version") == "" {
		t.Error("expected X-API-Version in error response headers")
	}
	if w.Header().Get("X-Min-Client-API-Version") == "" {
		t.Error("expected X-Min-Client-API-Version in error response headers")
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse error response: %v", err)
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "below minimum") {
		t.Errorf("error message %q should contain 'below minimum'", errMsg)
	}
}

func TestIngestLogs_InvalidAPIVersion(t *testing.T) {
	srv, _ := setupTestServerWithLogStore()

	body := `[{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","service":"api","message":"test"}]`
	req := httptest.NewRequest("POST", "/api/logs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("X-API-Version", "abc")

	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestIngestLogs_AcceptsValidAPIVersion(t *testing.T) {
	srv, _ := setupTestServerWithLogStore()

	body := `[{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","service":"api","message":"test"}]`
	req := httptest.NewRequest("POST", "/api/logs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("X-API-Version", "1")

	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201. Body: %s", w.Code, w.Body.String())
	}
}

func TestIngestLogs_AcceptsNoAPIVersion(t *testing.T) {
	srv, _ := setupTestServerWithLogStore()

	body := `[{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","service":"api","message":"test"}]`
	req := httptest.NewRequest("POST", "/api/logs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	// No X-API-Version header — old clients should still work

	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 for old client without version header. Body: %s", w.Code, w.Body.String())
	}
}

func TestIngestLogs_WithTraceContext(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	body := `[{
		"timestamp":"2024-01-01T00:00:00Z",
		"level":"INFO",
		"service":"api",
		"message":"traced request",
		"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736",
		"span_id":"00f067aa0ba902b7",
		"parent_span_id":"e5f678901234abcd"
	}]`
	req := httptest.NewRequest("POST", "/api/logs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("X-API-Version", "1")

	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201. Body: %s", w.Code, w.Body.String())
	}

	if len(ls.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ls.entries))
	}

	entry := ls.entries[0]
	if entry.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace_id = %q, want %q", entry.TraceID, "4bf92f3577b34da6a3ce929d0e0e4736")
	}
	if entry.SpanID != "00f067aa0ba902b7" {
		t.Errorf("span_id = %q, want %q", entry.SpanID, "00f067aa0ba902b7")
	}
	if entry.ParentSpanID != "e5f678901234abcd" {
		t.Errorf("parent_span_id = %q, want %q", entry.ParentSpanID, "e5f678901234abcd")
	}
}

func TestIngestLogs_WithoutTraceContext(t *testing.T) {
	srv, ls := setupTestServerWithLogStore()

	body := `[{"timestamp":"2024-01-01T00:00:00Z","level":"INFO","service":"api","message":"no trace"}]`
	req := httptest.NewRequest("POST", "/api/logs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")

	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}

	if len(ls.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ls.entries))
	}

	entry := ls.entries[0]
	if entry.SpanID != "" {
		t.Errorf("span_id should be empty, got %q", entry.SpanID)
	}
	if entry.ParentSpanID != "" {
		t.Errorf("parent_span_id should be empty, got %q", entry.ParentSpanID)
	}
}
