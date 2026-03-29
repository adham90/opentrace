package auth

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the auth domain.
var Module = server.Module{
	Name:  "auth",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		userStore:  deps.UserStore,
		cfg:        deps.Cfg,
		auditStore: deps.AuditStore,
	}
	h.loginTracker = newLoginTracker()

	// --- Unauthenticated routes (on the root router) ---
	if deps.RootRouter != nil {
		root := deps.RootRouter

		// Connect script — served at /connect for easy curl:
		//   curl -s https://server:8080/connect | bash
		root.Get("/connect", h.handleConnectScript)
	}

	// --- Connect API (unauthenticated — for `npx opentrace connect`) ---
	if deps.APIRouter != nil {
		api := deps.APIRouter
		if deps.LoginLimiter != nil {
			api.With(deps.LoginLimiter).Post("/auth/connect", h.handleConnect)
		} else {
			api.Post("/auth/connect", h.handleConnect)
		}
		api.Get("/auth/connect", h.handleConnectCheck)
	}
}
