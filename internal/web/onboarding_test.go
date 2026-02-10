package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/store"
)

func TestOnboardingPage_ShowsWhenNoUsers(t *testing.T) {
	srv := NewServerWithDeps(ServerDeps{
		DSStore:       newMockStore(),
		LogStore:      newMockLogStore(),
		UserStore:     newMockUserStore(),
		SessionStore:  newMockSessionStore(),
		SettingsStore: newMockSettingsStore(),
	})

	req := httptest.NewRequest(http.MethodGet, "/onboarding", nil)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "setup") {
		t.Fatal("expected onboarding page content")
	}
}

func TestOnboardingPage_RedirectsWhenUsersExist(t *testing.T) {
	us := newMockUserStore()
	ctx := context.Background()
	us.Create(ctx, store.CreateUserParams{
		Email:        "admin@example.com",
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
	})

	req := httptest.NewRequest(http.MethodGet, "/onboarding", nil)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/" {
		t.Fatalf("expected redirect to /, got %s", loc)
	}
}

func TestOnboardingSubmit_CreatesAdminAndAPIKey(t *testing.T) {
	us := newMockUserStore()
	ss := newMockSessionStore()
	settings := newMockSettingsStore()

	srv := NewServerWithDeps(ServerDeps{
		DSStore:       newMockStore(),
		LogStore:      newMockLogStore(),
		UserStore:     us,
		SessionStore:  ss,
		SettingsStore: settings,
	})

	form := url.Values{
		"email":    {"admin@test.com"},
		"password": {"password123"},
	}
	req := httptest.NewRequest(http.MethodPost, "/onboarding",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (step 2), got %d", rec.Code)
	}

	// User was created
	count, _ := us.Count(context.Background())
	if count != 1 {
		t.Fatalf("expected 1 user, got %d", count)
	}

	// API key was generated and stored
	key, _ := settings.GetAPIKey(context.Background())
	if key == "" {
		t.Fatal("expected API key to be stored")
	}
	if !strings.HasPrefix(key, "ot_") {
		t.Fatalf("expected key with ot_ prefix, got %q", key)
	}

	// Session cookie was set
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == sessionCookieName && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected session cookie to be set")
	}

	// Response body contains the API key
	body := rec.Body.String()
	if !strings.Contains(body, key) {
		t.Fatal("expected API key to appear in the response body")
	}
}

func TestOnboardingSubmit_RejectsWhenUsersExist(t *testing.T) {
	us := newMockUserStore()
	ctx := context.Background()
	us.Create(ctx, store.CreateUserParams{
		Email:        "admin@example.com",
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
	})

	form := url.Values{
		"email":    {"new@test.com"},
		"password": {"password123"},
	}
	req := httptest.NewRequest(http.MethodPost, "/onboarding",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
}

func TestOnboardingSubmit_ValidationErrors(t *testing.T) {
	srv := NewServerWithDeps(ServerDeps{
		DSStore:       newMockStore(),
		LogStore:      newMockLogStore(),
		UserStore:     newMockUserStore(),
		SessionStore:  newMockSessionStore(),
		SettingsStore: newMockSettingsStore(),
	})

	tests := []struct {
		name     string
		email    string
		password string
		errMsg   string
	}{
		{"empty email", "", "password123", "Email and password are required"},
		{"empty password", "test@test.com", "", "Email and password are required"},
		{"short password", "test@test.com", "short", "Password must be at least 8 characters"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{
				"email":    {tt.email},
				"password": {tt.password},
			}
			req := httptest.NewRequest(http.MethodPost, "/onboarding",
				strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			srv.Router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("expected 422, got %d", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.errMsg) {
				t.Fatalf("expected error %q in body", tt.errMsg)
			}
		})
	}
}
