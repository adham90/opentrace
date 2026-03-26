package views

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

// LayoutData holds the common data needed by the base layout.
type LayoutData struct {
	Title     string
	Nav       string // active nav item: "dashboard", "logs", "errors", etc.
	User      *store.User
	IsAdmin   bool
	CSRFToken string
	DevMode   bool
}

// LayoutDataFromContext builds LayoutData from the request context.
// Uses the server package's context helpers to extract user/session.
func LayoutDataFromContext(ctx context.Context, title, nav string) LayoutData {
	user := server.UserFromContext(ctx)
	isAdmin := user != nil && user.Role == store.RoleAdmin
	return LayoutData{
		Title:   title,
		Nav:     nav,
		User:    user,
		IsAdmin: isAdmin,
	}
}

// LayoutDataFromRequest builds LayoutData from the HTTP request.
func LayoutDataFromRequest(r *http.Request, title, nav string) LayoutData {
	return LayoutDataFromContext(r.Context(), title, nav)
}

// NavItem represents a sidebar navigation link.
type NavItem struct {
	Label  string
	Href   string
	Key    string // matches LayoutData.Nav for active state
	Icon   string // icon name
	Admin  bool   // only show for admins
}

// NavItems returns the standard navigation items.
func NavItems() []NavItem {
	return []NavItem{
		{Label: "Dashboard", Href: "/", Key: "dashboard", Icon: "home"},
		{Label: "Logs", Href: "/logs", Key: "logs", Icon: "file-text"},
		{Label: "Errors", Href: "/errors", Key: "errors", Icon: "alert-circle"},
		{Label: "Watchers", Href: "/watchers", Key: "watchers", Icon: "eye"},
		{Label: "Health", Href: "/health", Key: "health", Icon: "activity"},
		{Label: "Connectors", Href: "/connectors", Key: "connectors", Icon: "database"},
		{Label: "Tools", Href: "/tools", Key: "tools", Icon: "wrench"},
		{Label: "Sessions", Href: "/sessions", Key: "sessions", Icon: "terminal"},
	}
}

// AdminNavItems returns admin-only navigation items.
func AdminNavItems() []NavItem {
	return []NavItem{
		{Label: "Settings", Href: "/settings", Key: "settings", Icon: "settings"},
		{Label: "Users", Href: "/users", Key: "users", Icon: "users"},
	}
}

// TimeAgo returns a human-readable relative time string.
func TimeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
