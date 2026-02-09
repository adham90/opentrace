package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opentrace/opentrace/internal/connector"
	"github.com/opentrace/opentrace/internal/store"
)

func newTestServerWithWatchers() *Server {
	return NewServerWithDeps(ServerDeps{
		DSStore:      newMockStore(),
		LogStore:     newMockLogStore(),
		WatcherStore: newMockWatcherStore(),
		RunStore:     newMockWatcherRunStore(),
		AlertStore:   newMockAlertStore(),
		Registry:     connector.NewRegistry(),
	})
}

func TestHandleCreateWatcher(t *testing.T) {
	srv := newTestServerWithWatchers()

	body := `{"title":"Error watcher","description":"Watch for errors","severity":"critical","time_range":"15m"}`
	req := httptest.NewRequest(http.MethodPost, "/api/watchers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var watcher store.Watcher
	json.NewDecoder(w.Body).Decode(&watcher)
	if watcher.Title != "Error watcher" {
		t.Errorf("title = %q, want %q", watcher.Title, "Error watcher")
	}
	if watcher.Severity != store.SeverityCritical {
		t.Errorf("severity = %q, want %q", watcher.Severity, store.SeverityCritical)
	}
}

func TestHandleCreateWatcher_MissingFields(t *testing.T) {
	srv := newTestServerWithWatchers()

	body := `{"title":"","description":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/watchers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleListWatchers(t *testing.T) {
	srv := newTestServerWithWatchers()

	// Create one first
	body := `{"title":"Test","description":"Test watcher"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/watchers", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(httptest.NewRecorder(), createReq)

	req := httptest.NewRequest(http.MethodGet, "/api/watchers", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var list []store.Watcher
	json.NewDecoder(w.Body).Decode(&list)
	if len(list) != 1 {
		t.Errorf("len = %d, want 1", len(list))
	}
}

func TestHandleGetWatcher(t *testing.T) {
	srv := newTestServerWithWatchers()

	// Create
	body := `{"title":"Get test","description":"For get test"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/watchers", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	srv.Router.ServeHTTP(createW, createReq)

	var created store.Watcher
	json.NewDecoder(createW.Body).Decode(&created)

	// Get
	req := httptest.NewRequest(http.MethodGet, "/api/watchers/"+created.ID.String(), nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleGetWatcher_NotFound(t *testing.T) {
	srv := newTestServerWithWatchers()

	req := httptest.NewRequest(http.MethodGet, "/api/watchers/00000000-0000-0000-0000-000000000001", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleDeleteWatcher(t *testing.T) {
	srv := newTestServerWithWatchers()

	body := `{"title":"Delete me","description":"To be deleted"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/watchers", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	srv.Router.ServeHTTP(createW, createReq)

	var created store.Watcher
	json.NewDecoder(createW.Body).Decode(&created)

	req := httptest.NewRequest(http.MethodDelete, "/api/watchers/"+created.ID.String(), nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandlePauseResumeWatcher(t *testing.T) {
	srv := newTestServerWithWatchers()

	body := `{"title":"Pause test","description":"For pause test"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/watchers", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	srv.Router.ServeHTTP(createW, createReq)

	var created store.Watcher
	json.NewDecoder(createW.Body).Decode(&created)

	// Pause
	pauseReq := httptest.NewRequest(http.MethodPost, "/api/watchers/"+created.ID.String()+"/pause", nil)
	pauseW := httptest.NewRecorder()
	srv.Router.ServeHTTP(pauseW, pauseReq)

	if pauseW.Code != http.StatusOK {
		t.Fatalf("pause status = %d, want %d", pauseW.Code, http.StatusOK)
	}

	var paused store.Watcher
	json.NewDecoder(pauseW.Body).Decode(&paused)
	if paused.Status != store.WatcherPaused {
		t.Errorf("paused status = %q, want %q", paused.Status, store.WatcherPaused)
	}

	// Resume
	resumeReq := httptest.NewRequest(http.MethodPost, "/api/watchers/"+created.ID.String()+"/resume", nil)
	resumeW := httptest.NewRecorder()
	srv.Router.ServeHTTP(resumeW, resumeReq)

	if resumeW.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want %d", resumeW.Code, http.StatusOK)
	}

	var resumed store.Watcher
	json.NewDecoder(resumeW.Body).Decode(&resumed)
	if resumed.Status != store.WatcherActive {
		t.Errorf("resumed status = %q, want %q", resumed.Status, store.WatcherActive)
	}
}

func TestHandleAlerts(t *testing.T) {
	srv := newTestServerWithWatchers()

	// List — should be empty
	req := httptest.NewRequest(http.MethodGet, "/api/alerts", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", w.Code, http.StatusOK)
	}

	// Count
	countReq := httptest.NewRequest(http.MethodGet, "/api/alerts/count", nil)
	countW := httptest.NewRecorder()
	srv.Router.ServeHTTP(countW, countReq)

	if countW.Code != http.StatusOK {
		t.Fatalf("count status = %d, want %d", countW.Code, http.StatusOK)
	}

	var countResp map[string]int
	json.NewDecoder(countW.Body).Decode(&countResp)
	if countResp["count"] != 0 {
		t.Errorf("count = %d, want 0", countResp["count"])
	}
}

func TestHandleMarkAlertRead_NotFound(t *testing.T) {
	srv := newTestServerWithWatchers()

	req := httptest.NewRequest(http.MethodPost, "/api/alerts/00000000-0000-0000-0000-000000000001/read", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleDismissAlert_NotFound(t *testing.T) {
	srv := newTestServerWithWatchers()

	req := httptest.NewRequest(http.MethodPost, "/api/alerts/00000000-0000-0000-0000-000000000001/dismiss", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
