package auth

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
)

// Module describes the auth domain.
var Module = server.Module{
	Name:  "auth",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		userStore:    deps.UserStore,
		sessionStore: deps.SessionStore,
		cfg:          deps.Cfg,
		auditStore:   deps.AuditStore,
	}
	h.loginTracker = newLoginTracker()
	h.secureCookies = deps.SecureCookies

	// --- Unauthenticated routes (on the root router) ---
	if deps.RootRouter != nil {
		root := deps.RootRouter

		root.Get("/login", h.handleLoginPage)
		if deps.LoginLimiter != nil {
			root.With(deps.LoginLimiter).Post("/login", h.handleLoginSubmit)
			root.With(deps.LoginLimiter).Post("/register", h.handleRegisterSubmit)
		} else {
			root.Post("/login", h.handleLoginSubmit)
			root.Post("/register", h.handleRegisterSubmit)
		}
		root.Get("/register", h.handleRegisterPage)
		root.Post("/logout", h.handleLogout)
	}

	// --- Authenticated page routes ---
	if deps.PageRouter != nil {
		deps.PageRouter.Get("/profile", h.handleProfilePage)
	}

	// --- API routes (on the auth-protected API router) ---
	// Profile API (auth required)
	r.Post("/profile/password", h.handleChangePassword)
	r.Get("/profile/mcp-token", h.handleGetOwnMCPToken)

	// User management API (admin only)
	r.Group(func(r chi.Router) {
		r.Use(server.RequireAdminAPI)
		r.Post("/users/{id}/role", h.handleUpdateUserRole)
		r.Post("/users/{id}/mcp", h.handleToggleMCPAccess)
		r.Post("/users/{id}/active", h.handleToggleUserActive)
		r.Post("/users/{id}/mcp-token", h.handleRegenerateMCPToken)
		r.Delete("/users/{id}", h.handleDeleteUser)
	})
}
