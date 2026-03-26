package onboarding

import (
	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
)

// Module describes the onboarding domain.
var Module = server.Module{
	Name:  "onboarding",
	Mount: mount,
}

func mount(r chi.Router, deps *server.Deps) {
	h := &handler{
		userStore:     deps.UserStore,
		sessionStore:  deps.SessionStore,
		settingsStore: deps.SettingsStore,
		cfg:           deps.Cfg,
	}
	h.secureCookies = deps.SecureCookies

	// Onboarding routes are unauthenticated (on the root router).
	if deps.RootRouter != nil {
		deps.RootRouter.Get("/onboarding", h.handleOnboardingPage)
		deps.RootRouter.Post("/onboarding", h.handleOnboardingSubmit)
	}
}
