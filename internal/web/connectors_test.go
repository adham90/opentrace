package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/opentrace/opentrace/internal/connector"
	"github.com/opentrace/opentrace/internal/store"
)

func setupTestServer() (*Server, *mockDataSourceStore) {
	ms := newMockStore()
	reg := connector.NewRegistry()
	srv := NewServer(ms, newMockLogStore(), newMockEmbeddingStore(), reg, nil, nil)
	return srv, ms
}

func TestCreateConnector_Success(t *testing.T) {
	srv, _ := setupTestServer()

	body := `{"type":"logs","name":"Test Logs","config":{"endpoint":"http://localhost"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var ds store.DataSource
	if err := json.NewDecoder(w.Body).Decode(&ds); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if ds.ID == uuid.Nil {
		t.Error("expected non-nil UUID")
	}
	if ds.Type != store.ConnectorLogs {
		t.Errorf("Type = %q, want %q", ds.Type, store.ConnectorLogs)
	}
	if ds.Name != "Test Logs" {
		t.Errorf("Name = %q, want %q", ds.Name, "Test Logs")
	}
}

func TestCreateConnector_InvalidBody(t *testing.T) {
	srv, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodPost, "/api/connectors", bytes.NewBufferString("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateConnector_MissingRequiredFields(t *testing.T) {
	srv, _ := setupTestServer()

	body := `{"config":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestListConnectors_Success(t *testing.T) {
	srv, ms := setupTestServer()

	// Pre-populate
	ms.Create(nil, store.CreateDataSourceParams{Type: store.ConnectorLogs, Name: "Logs", Config: map[string]any{}})
	ms.Create(nil, store.CreateDataSourceParams{Type: store.ConnectorDatabase, Name: "DB", Config: map[string]any{}})

	req := httptest.NewRequest(http.MethodGet, "/api/connectors", nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var list []store.DataSource
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
}

func TestListConnectors_Empty(t *testing.T) {
	srv, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/connectors", nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	// Should be [] not null
	if body != "[]\n" {
		t.Fatalf("body = %q, want %q", body, "[]\n")
	}
}

func TestTestConnector_Success(t *testing.T) {
	srv, ms := setupTestServer()

	ds, _ := ms.Create(nil, store.CreateDataSourceParams{
		Type:   store.ConnectorLogs,
		Name:   "Logs",
		Config: map[string]any{},
	})

	url := fmt.Sprintf("/api/connectors/%s/test", ds.ID)
	req := httptest.NewRequest(http.MethodPost, url, nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestTestConnector_NotFound(t *testing.T) {
	srv, _ := setupTestServer()

	url := fmt.Sprintf("/api/connectors/%s/test", uuid.New())
	req := httptest.NewRequest(http.MethodPost, url, nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDeleteConnector_Success(t *testing.T) {
	srv, ms := setupTestServer()

	ds, _ := ms.Create(nil, store.CreateDataSourceParams{
		Type:   store.ConnectorLogs,
		Name:   "Logs",
		Config: map[string]any{},
	})

	url := fmt.Sprintf("/api/connectors/%s", ds.ID)
	req := httptest.NewRequest(http.MethodDelete, url, nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestDeleteConnector_NotFound(t *testing.T) {
	srv, _ := setupTestServer()

	url := fmt.Sprintf("/api/connectors/%s", uuid.New())
	req := httptest.NewRequest(http.MethodDelete, url, nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDeleteConnector_InvalidUUID(t *testing.T) {
	srv, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodDelete, "/api/connectors/not-a-uuid", nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
