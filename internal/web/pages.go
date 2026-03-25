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

// Page handlers moved to domain modules:
//   - Logs: internal/modules/logs/
//   - Errors: internal/modules/errors/
//   - Health: internal/modules/healthchecks/
//   - Connectors: internal/modules/connectors/
//   - Tools: internal/modules/tools/
//   - Sessions: internal/modules/investigations/
//   - Watchers: internal/modules/watches/
//   - Settings/Users: internal/modules/settings/
//   - Auth/Profile: internal/modules/auth/
