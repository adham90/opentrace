package testutil

import (
	"net/http"

	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
)

// AdminUser returns a test admin user.
func AdminUser() *store.User {
	return &store.User{
		ID:          "test-admin",
		Email:       "admin@test.com",
		Role:        store.RoleAdmin,
		IsActive:    true,
		DisplayName: "Admin",
	}
}

// MemberUser returns a test non-admin user.
func MemberUser() *store.User {
	return &store.User{
		ID:          "test-member",
		Email:       "member@test.com",
		Role:        store.RoleMember,
		IsActive:    true,
		DisplayName: "Member",
	}
}

// WithUser injects a user into the request context for testing.
func WithUser(req *http.Request, user *store.User) *http.Request {
	return req.WithContext(server.WithUser(req.Context(), user))
}

// WithAdmin injects an admin user into the request context.
func WithAdmin(req *http.Request) *http.Request {
	return WithUser(req, AdminUser())
}
