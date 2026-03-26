package web

import (
	"embed"
	"net/http"

	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/views"
)

//go:embed static/*
var staticFS embed.FS

func (s *Server) isDevMode() bool {
	return s.cfg != nil && s.cfg.DevMode
}

// layoutData creates a views.LayoutData for templ rendering.
func (s *Server) layoutData(r *http.Request, title, nav string) views.LayoutData {
	user := UserFromContext(r.Context())
	isAdmin := user != nil && user.Role == store.RoleAdmin
	return views.LayoutData{
		Title:   title,
		Nav:     nav,
		User:    user,
		IsAdmin: isAdmin,
		DevMode: s.isDevMode(),
	}
}

// Page handlers moved to domain packages:
//   - Logs: internal/domains/logs/
//   - Errors: internal/domains/errors/
//   - Health: internal/domains/healthchecks/
//   - Connectors: internal/domains/connectors/
//   - Tools: internal/domains/tools/
//   - Sessions: internal/domains/investigations/
//   - Watchers: internal/domains/watches/
//   - Settings/Users: internal/domains/settings/
//   - Auth/Profile: internal/domains/auth/
