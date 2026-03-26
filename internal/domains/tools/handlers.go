package tools

import (
	"net/http"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/views"
	webviews "github.com/adham90/opentrace/internal/web/views"
)

type handler struct {
	toolCatalog server.ToolCatalogProvider
	registry    *connector.Registry
	cfg         *config.Config
}

// ── Page handlers ────────────────────────────────────────────

func (h *handler) layoutData(r *http.Request, title, nav string) views.LayoutData {
	user := server.UserFromContext(r.Context())
	isAdmin := user != nil && user.Role == store.RoleAdmin
	return views.LayoutData{
		Title:   title,
		Nav:     nav,
		User:    user,
		IsAdmin: isAdmin,
		DevMode: h.cfg != nil && h.cfg.DevMode,
	}
}

func (h *handler) toolsPage(w http.ResponseWriter, r *http.Request) {
	layout := h.layoutData(r, "Tools", "tools")

	var categories []webviews.ToolCategory
	if h.toolCatalog != nil {
		for _, cat := range h.toolCatalog.CategoriesForDisplay() {
			tc := webviews.ToolCategory{Name: cat.Name, Description: cat.Description}
			for _, t := range cat.Tools {
				tc.Tools = append(tc.Tools, webviews.ToolInfo{
					Name:        t.Name,
					Description: t.Description,
					Access:      t.Access,
					Requires:    t.Requires,
				})
			}
			categories = append(categories, tc)
		}
	}
	if h.registry != nil {
		dynamicTools := h.registry.AllTools()
		if len(dynamicTools) > 0 {
			dynCat := webviews.ToolCategory{
				Name:        "Connector Queries",
				Description: "Dynamic tools registered by active database connectors",
			}
			for _, t := range dynamicTools {
				dynCat.Tools = append(dynCat.Tools, webviews.ToolInfo{
					Name:        t.Name,
					Description: t.Description,
					Access:      "admin",
					Requires:    "database connector",
				})
			}
			categories = append(categories, dynCat)
		}
	}

	webviews.ToolsPage(layout, categories).Render(r.Context(), w)
}

// ── API handlers ─────────────────────────────────────────────

func (h *handler) toolsAPI(w http.ResponseWriter, r *http.Request) {
	type toolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Access      string `json:"access"`
		Requires    string `json:"requires,omitempty"`
	}

	type toolCategory struct {
		Name        string     `json:"name"`
		Description string     `json:"description"`
		Tools       []toolInfo `json:"tools"`
	}

	var categories []toolCategory

	if h.toolCatalog != nil {
		for _, cat := range h.toolCatalog.CategoriesForDisplay() {
			tc := toolCategory{Name: cat.Name, Description: cat.Description}
			for _, t := range cat.Tools {
				tc.Tools = append(tc.Tools, toolInfo{
					Name:        t.Name,
					Description: t.Description,
					Access:      t.Access,
					Requires:    t.Requires,
				})
			}
			categories = append(categories, tc)
		}
	}

	// Append dynamic connector tools from the registry (added after startup).
	if h.registry != nil {
		dynamicTools := h.registry.AllTools()
		if len(dynamicTools) > 0 {
			dynCat := toolCategory{
				Name:        "Connector Queries",
				Description: "Dynamic tools registered by active database connectors",
			}
			for _, t := range dynamicTools {
				dynCat.Tools = append(dynCat.Tools, toolInfo{
					Name:        t.Name,
					Description: t.Description,
					Access:      "admin",
					Requires:    "database connector",
				})
			}
			categories = append(categories, dynCat)
		}
	}

	server.WriteJSON(w, http.StatusOK, categories)
}
