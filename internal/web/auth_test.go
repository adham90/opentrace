package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
)

// TestSessionAuth_NoCookie verifies that when no session cookie is set,
// the request proceeds through the middleware without a user in the context.
func TestSessionAuth_NoCookie(t *testing.T) {
	us := newMockUserStore()
	ss := newMockSessionStore()
	srv := NewServerWithDeps(ServerDeps{
		DSStore:      newMockStore(),
		LogStore:     newMockLogStore(),
		UserStore:    us,
		SessionStore: ss,
	})

	var gotUser *store.User
	handler := srv.SessionAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotUser != nil {
		t.Fatal("expected no user in context when no cookie is set")
	}
}

// TestSessionAuth_ValidSession verifies that when a valid session cookie is set,
// the user is loaded into the request context.
func TestSessionAuth_ValidSession(t *testing.T) {
	us := newMockUserStore()
	ss := newMockSessionStore()

	// Create a user and a session.
	ctx := context.Background()
	user, err := us.Create(ctx, store.CreateUserParams{
		Email:        "test@example.com",
		PasswordHash: "hash",
		DisplayName:  "Test User",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}

	token := "valid-session-token"
	_, err = ss.Create(ctx, user.ID, token, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("creating session: %v", err)
	}

	srv := NewServerWithDeps(ServerDeps{
		DSStore:      newMockStore(),
		LogStore:     newMockLogStore(),
		UserStore:    us,
		SessionStore: ss,
	})

	var gotUser *store.User
	handler := srv.SessionAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotUser == nil {
		t.Fatal("expected user in context with valid session")
	}
	if gotUser.ID != user.ID {
		t.Fatalf("expected user ID %s, got %s", user.ID, gotUser.ID)
	}
	if gotUser.Email != "test@example.com" {
		t.Fatalf("expected email test@example.com, got %s", gotUser.Email)
	}
}

// TestSessionAuth_InvalidToken verifies that when the session cookie contains
// an invalid token, the request proceeds with no user in the context.
func TestSessionAuth_InvalidToken(t *testing.T) {
	us := newMockUserStore()
	ss := newMockSessionStore()
	srv := NewServerWithDeps(ServerDeps{
		DSStore:      newMockStore(),
		LogStore:     newMockLogStore(),
		UserStore:    us,
		SessionStore: ss,
	})

	var gotUser *store.User
	handler := srv.SessionAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "bad-token"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotUser != nil {
		t.Fatal("expected no user in context with invalid token")
	}
}

// TestRequireAuth_NoUser verifies that RequireAuth redirects to /login (302)
// when no user is in the context.
func TestRequireAuth_NoUser(t *testing.T) {
	handler := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/login" {
		t.Fatalf("expected redirect to /login, got %s", loc)
	}
}

// TestRequireAuth_WithUser verifies that RequireAuth allows the request to
// proceed when a user is present in the context.
func TestRequireAuth_WithUser(t *testing.T) {
	handler := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	ctx := server.WithUser(req.Context(), &store.User{
		ID:       "user-1",
		Email:    "test@example.com",
		Role:     store.RoleMember,
		IsActive: true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// TestRequireAdmin_NonAdmin verifies that RequireAdmin returns 403 when the
// user in the context has the member role.
func TestRequireAdmin_NonAdmin(t *testing.T) {
	handler := RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	ctx := server.WithUser(req.Context(), &store.User{
		ID:       "user-1",
		Email:    "member@example.com",
		Role:     store.RoleMember,
		IsActive: true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

// TestRequireAdmin_WithAdmin verifies that RequireAdmin allows the request to
// proceed when the user in the context has the admin role.
func TestRequireAdmin_WithAdmin(t *testing.T) {
	handler := RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	ctx := server.WithUser(req.Context(), &store.User{
		ID:       "admin-1",
		Email:    "admin@example.com",
		Role:     store.RoleAdmin,
		IsActive: true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// TestRedirectToOnboarding_NoUsers verifies that when no users exist in the
// store (Count() returns 0), the middleware redirects to /onboarding.
func TestRedirectToOnboarding_NoUsers(t *testing.T) {
	us := newMockUserStore() // empty store, Count() == 0
	srv := NewServerWithDeps(ServerDeps{
		DSStore:   newMockStore(),
		LogStore:  newMockLogStore(),
		UserStore: us,
	})

	handler := srv.RedirectToOnboardingIfNeeded(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect to /onboarding when no users exist, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/onboarding" {
		t.Fatalf("expected redirect to /onboarding, got %s", loc)
	}
}

// TestRedirectToOnboarding_WithUsers verifies that when users exist in the
// store (Count() returns 1+) and no user is in the context, the middleware
// redirects to /login.
func TestRedirectToOnboarding_WithUsers(t *testing.T) {
	us := newMockUserStore()
	ctx := context.Background()
	_, err := us.Create(ctx, store.CreateUserParams{
		Email:        "admin@example.com",
		PasswordHash: "hash",
		DisplayName:  "Admin",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}

	srv := NewServerWithDeps(ServerDeps{
		DSStore:   newMockStore(),
		LogStore:  newMockLogStore(),
		UserStore: us,
	})

	handler := srv.RedirectToOnboardingIfNeeded(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect when users exist and no auth, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/login" {
		t.Fatalf("expected redirect to /login, got %s", loc)
	}
}
