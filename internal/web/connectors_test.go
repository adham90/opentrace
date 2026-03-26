package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/domains/connectors"
	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
)

func setupTestServer() (*Server, *mockDataSourceStore) {
	ms := newMockStore()
	ls := newMockLogStore()
	us := newMockUserStore()
	us.Create(nil, store.CreateUserParams{
		Email: "admin@test.com", PasswordHash: "hash",
		DisplayName: "Admin", Role: store.RoleAdmin,
	})
	reg := connector.NewRegistry()
	deps := &server.Deps{
		Stores: store.Stores{
			DSStore:   ms,
			LogStore:  ls,
			UserStore: us,
		},
		Registry: reg,
	}
	srv := NewServerWithDeps(ServerDeps{
		Stores: store.Stores{
			DSStore:   ms,
			LogStore:  ls,
			UserStore: us,
		},
		Registry:   reg,
		SharedDeps: deps,
		Modules:    []server.Module{connectors.Module},
	})
	return srv, ms
}

func TestCreateConnector_Success(t *testing.T) {
	srv, _ := setupTestServer()

	body := `{"type":"logs","name":"Test Logs","config":{"endpoint":"http://localhost"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	req = withAdmin(req)
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
	withCSRF(req)
	req = withAdmin(req)
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
	withCSRF(req)
	req = withAdmin(req)
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
	req = withAdmin(req)
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
	req = withAdmin(req)
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
	withCSRF(req)
	req = withAdmin(req)
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
	withCSRF(req)
	req = withAdmin(req)
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
	withCSRF(req)
	req = withAdmin(req)
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
	withCSRF(req)
	req = withAdmin(req)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGetConnector_Success(t *testing.T) {
	srv, ms := setupTestServer()

	ds, _ := ms.Create(nil, store.CreateDataSourceParams{
		Type:   store.ConnectorDatabase,
		Name:   "Prod DB",
		Config: map[string]any{"connection_string": "postgres://localhost/db"},
	})

	url := fmt.Sprintf("/api/connectors/%s", ds.ID)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req = withAdmin(req)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got store.DataSource
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.ID != ds.ID {
		t.Errorf("ID = %v, want %v", got.ID, ds.ID)
	}
	if got.Name != "Prod DB" {
		t.Errorf("Name = %q, want %q", got.Name, "Prod DB")
	}
}

func TestGetConnector_NotFound(t *testing.T) {
	srv, _ := setupTestServer()

	url := fmt.Sprintf("/api/connectors/%s", uuid.New())
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req = withAdmin(req)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestUpdateConnector_Success(t *testing.T) {
	srv, ms := setupTestServer()

	ds, _ := ms.Create(nil, store.CreateDataSourceParams{
		Type:   store.ConnectorDatabase,
		Name:   "Old Name",
		Config: map[string]any{"connection_string": "postgres://old"},
	})

	body := `{"name":"New Name","config":{"connection_string":"postgres://new"}}`
	url := fmt.Sprintf("/api/connectors/%s", ds.ID)
	req := httptest.NewRequest(http.MethodPut, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	req = withAdmin(req)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var updated store.DataSource
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if updated.Name != "New Name" {
		t.Errorf("Name = %q, want %q", updated.Name, "New Name")
	}
}

func TestUpdateConnector_NotFound(t *testing.T) {
	srv, _ := setupTestServer()

	body := `{"name":"New Name"}`
	url := fmt.Sprintf("/api/connectors/%s", uuid.New())
	req := httptest.NewRequest(http.MethodPut, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	req = withAdmin(req)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestUpdateConnector_EmptyName(t *testing.T) {
	srv, ms := setupTestServer()

	ds, _ := ms.Create(nil, store.CreateDataSourceParams{
		Type:   store.ConnectorDatabase,
		Name:   "DB",
		Config: map[string]any{},
	})

	body := `{"name":""}`
	url := fmt.Sprintf("/api/connectors/%s", ds.ID)
	req := httptest.NewRequest(http.MethodPut, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	req = withAdmin(req)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateConnector_InvalidBody(t *testing.T) {
	srv, _ := setupTestServer()

	url := fmt.Sprintf("/api/connectors/%s", uuid.New())
	req := httptest.NewRequest(http.MethodPut, url, bytes.NewBufferString("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	req = withAdmin(req)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDeleteConnector_InvalidUUID(t *testing.T) {
	srv, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodDelete, "/api/connectors/not-a-uuid", nil)
	withCSRF(req)
	req = withAdmin(req)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
