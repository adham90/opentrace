package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/store"
)

func newSettingsTestServer(t *testing.T) *Server {
	t.Helper()
	return NewServerWithDeps(ServerDeps{
		DSStore:       newMockStore(),
		LogStore:      newMockLogStore(),
		UserStore:     newMockUserStore(),
		SessionStore:  newMockSessionStore(),
		SettingsStore: newMockSettingsStore(),
	})
}

func settingsRequest(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	// Inject admin user into context so RequireAdmin passes
	admin := &store.User{ID: "admin1", Role: store.RoleAdmin, IsActive: true}
	ctx := context.WithValue(req.Context(), ctxKeyUser, admin)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	srv.Router.ServeHTTP(rr, req)
	return rr
}

func TestSettings_GetDefault(t *testing.T) {
	srv := newSettingsTestServer(t)

	rr := settingsRequest(t, srv, "GET", "/api/settings/retention", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp store.RetentionSettings
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.RetentionDays != 30 {
		t.Errorf("retention_days = %d, want 30", resp.RetentionDays)
	}
}

func TestSettings_PutAndGet(t *testing.T) {
	srv := newSettingsTestServer(t)

	// Update
	rr := settingsRequest(t, srv, "PUT", "/api/settings/retention", `{"retention_days": 7}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Verify
	rr = settingsRequest(t, srv, "GET", "/api/settings/retention", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp store.RetentionSettings
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.RetentionDays != 7 {
		t.Errorf("retention_days = %d, want 7", resp.RetentionDays)
	}
}

func TestSettings_KeepForever(t *testing.T) {
	srv := newSettingsTestServer(t)

	rr := settingsRequest(t, srv, "PUT", "/api/settings/retention", `{"retention_days": 0}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d", rr.Code, http.StatusOK)
	}

	rr = settingsRequest(t, srv, "GET", "/api/settings/retention", "")
	var resp store.RetentionSettings
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.RetentionDays != 0 {
		t.Errorf("retention_days = %d, want 0", resp.RetentionDays)
	}
}

func TestSettings_NegativeValue(t *testing.T) {
	srv := newSettingsTestServer(t)

	rr := settingsRequest(t, srv, "PUT", "/api/settings/retention", `{"retention_days": -5}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}
