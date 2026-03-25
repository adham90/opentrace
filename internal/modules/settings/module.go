package settings

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/server"
)

// Module describes the settings domain.
var Module = server.Module{
	Name:  "settings",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		settingsStore: deps.SettingsStore,
		auditStore:    deps.AuditStore,
		userStore:     deps.UserStore,
		cfg:           deps.Cfg,
		db:            deps.DB,
	}

	// All settings routes require admin.
	r.Group(func(r chi.Router) {
		r.Use(server.RequireAdminAPI)

		r.Get("/settings/retention", h.getRetention)
		r.Put("/settings/retention", h.updateRetention)
		r.Get("/settings/api-key", h.getAPIKey)
		r.Post("/settings/api-key", h.regenerateAPIKey)
		r.Get("/settings/cors", h.getCORSOrigins)
		r.Put("/settings/cors", h.updateCORSOrigins)
		r.Get("/settings/query-guardrails", h.getQueryGuardrails)
		r.Put("/settings/query-guardrails", h.updateQueryGuardrails)
		r.Get("/settings/mcp-name", h.getMCPName)
		r.Put("/settings/mcp-name", h.updateMCPName)
		r.Get("/settings/sampling", h.getSamplingRules)
		r.Put("/settings/sampling", h.updateSamplingRules)

		if deps.AuditStore != nil {
			r.Get("/audit-log", h.auditLog)
		}
	})

	// Admin page routes (mounted on the admin page router with RequireAdmin middleware)
	if deps.AdminPageRouter != nil {
		deps.AdminPageRouter.Get("/settings", h.settingsPage)
		deps.AdminPageRouter.Get("/setup", h.setupPage)
		deps.AdminPageRouter.Get("/users", h.usersPage)
	}
}
