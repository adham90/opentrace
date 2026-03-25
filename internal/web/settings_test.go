package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
)

func newSettingsTestServer(t *testing.T) *Server {
	t.Helper()
	us := newMockUserStore()
	// Create a user so the admin middleware doesn't return 503
	us.Create(context.Background(), store.CreateUserParams{
		Email:        "admin@test.com",
		PasswordHash: "hash",
		DisplayName:  "Admin",
		Role:         store.RoleAdmin,
	})
	return NewServerWithDeps(ServerDeps{
		DSStore:       newMockStore(),
		LogStore:      newMockLogStore(),
		UserStore:     us,
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
	// Add CSRF token for unsafe methods
	if method != "GET" && method != "HEAD" && method != "OPTIONS" {
		withCSRF(req)
	}
	// Inject admin user into context so RequireAdmin passes
	admin := &store.User{ID: "admin1", Role: store.RoleAdmin, IsActive: true}
	ctx := server.WithUser(req.Context(), admin)
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

func TestAPIKey_Get(t *testing.T) {
	srv := newSettingsTestServer(t)

	rr := settingsRequest(t, srv, "GET", "/api/settings/api-key", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["env_override"] != false {
		t.Error("expected env_override = false")
	}
}

func TestAPIKey_Regenerate(t *testing.T) {
	srv := newSettingsTestServer(t)

	rr := settingsRequest(t, srv, "POST", "/api/settings/api-key", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	key := resp["api_key"]
	if key == "" {
		t.Fatal("expected non-empty API key")
	}
	if len(key) < 10 || key[:3] != "ot_" {
		t.Fatalf("expected key with ot_ prefix, got %q", key)
	}

	// Fetch again to confirm it was stored
	rr = settingsRequest(t, srv, "GET", "/api/settings/api-key", "")
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["api_key"] != key {
		t.Fatalf("expected stored key %q, got %q", key, resp["api_key"])
	}
}

func TestCORS_Get(t *testing.T) {
	srv := newSettingsTestServer(t)

	rr := settingsRequest(t, srv, "GET", "/api/settings/cors", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["cors_origins"] != "" {
		t.Errorf("expected empty cors_origins, got %q", resp["cors_origins"])
	}
	if resp["env_override"] != false {
		t.Error("expected env_override = false")
	}
}

func TestCORS_PutAndGet(t *testing.T) {
	srv := newSettingsTestServer(t)

	rr := settingsRequest(t, srv, "PUT", "/api/settings/cors", `{"cors_origins":"https://example.com,https://app.example.com"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", rr.Code)
	}

	rr = settingsRequest(t, srv, "GET", "/api/settings/cors", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rr.Code)
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["cors_origins"] != "https://example.com,https://app.example.com" {
		t.Errorf("cors_origins = %q, want comma-separated origins", resp["cors_origins"])
	}
}

func TestCORS_PutBlockedByEnvVar(t *testing.T) {
	us := newMockUserStore()
	us.Create(context.Background(), store.CreateUserParams{
		Email:        "admin@test.com",
		PasswordHash: "hash",
		DisplayName:  "Admin",
		Role:         store.RoleAdmin,
	})
	srv := NewServerWithDeps(ServerDeps{
		DSStore:       newMockStore(),
		LogStore:      newMockLogStore(),
		UserStore:     us,
		SessionStore:  newMockSessionStore(),
		SettingsStore: newMockSettingsStore(),
		Cfg:           &config.Config{CORSAllowedOrigins: []string{"https://env.example.com"}},
	})

	rr := settingsRequest(t, srv, "PUT", "/api/settings/cors", `{"cors_origins":"https://other.com"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}

func TestCORS_GetWithEnvOverride(t *testing.T) {
	us := newMockUserStore()
	us.Create(context.Background(), store.CreateUserParams{
		Email:        "admin@test.com",
		PasswordHash: "hash",
		DisplayName:  "Admin",
		Role:         store.RoleAdmin,
	})
	srv := NewServerWithDeps(ServerDeps{
		DSStore:       newMockStore(),
		LogStore:      newMockLogStore(),
		UserStore:     us,
		SessionStore:  newMockSessionStore(),
		SettingsStore: newMockSettingsStore(),
		Cfg:           &config.Config{CORSAllowedOrigins: []string{"https://env.example.com"}},
	})

	rr := settingsRequest(t, srv, "GET", "/api/settings/cors", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["cors_origins"] != "https://env.example.com" {
		t.Errorf("cors_origins = %q, want env value", resp["cors_origins"])
	}
	if resp["env_override"] != true {
		t.Error("expected env_override = true")
	}
}

func TestAPIKey_RegenerateBlockedByEnvVar(t *testing.T) {
	us := newMockUserStore()
	us.Create(context.Background(), store.CreateUserParams{
		Email:        "admin@test.com",
		PasswordHash: "hash",
		DisplayName:  "Admin",
		Role:         store.RoleAdmin,
	})
	srv := NewServerWithDeps(ServerDeps{
		DSStore:       newMockStore(),
		LogStore:      newMockLogStore(),
		UserStore:     us,
		SessionStore:  newMockSessionStore(),
		SettingsStore: newMockSettingsStore(),
		Cfg:           &config.Config{APIKey: "env-key-123"},
	})

	rr := settingsRequest(t, srv, "POST", "/api/settings/api-key", "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}
