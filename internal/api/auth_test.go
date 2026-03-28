package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

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
