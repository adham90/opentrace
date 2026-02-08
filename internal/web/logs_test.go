package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
