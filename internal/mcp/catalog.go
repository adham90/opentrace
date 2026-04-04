package mcp

import "github.com/adham90/opentrace/pkg/server"

// CatalogEntry describes a single MCP tool for the web tools page.
type CatalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Access      string `json:"access"` // "read" or "admin"
	Category    string `json:"-"`      // grouping key (not serialized directly)
	Requires    string `json:"requires,omitempty"`
}

// CatalogCategory groups tools by functional area.
type CatalogCategory struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Tools       []CatalogEntry `json:"tools"`
}

// ToolCatalog holds the full set of categorized MCP tools.
type ToolCatalog struct {
	categories []CatalogCategory
}

// Categories returns the ordered list of tool categories.
func (tc *ToolCatalog) Categories() []CatalogCategory {
	if tc == nil {
		return nil
	}
	return tc.categories
}

// CategoriesForDisplay implements server.ToolCatalogProvider, returning
// categories in a package-independent type for use by domain modules.
func (tc *ToolCatalog) CategoriesForDisplay() []server.ToolCatalogCategory {
	if tc == nil {
		return nil
	}
	cats := make([]server.ToolCatalogCategory, len(tc.categories))
	for i, c := range tc.categories {
		tools := make([]server.ToolCatalogEntry, len(c.Tools))
		for j, t := range c.Tools {
			tools[j] = server.ToolCatalogEntry{
				Name:        t.Name,
				Description: t.Description,
				Access:      t.Access,
				Requires:    t.Requires,
			}
		}
		cats[i] = server.ToolCatalogCategory{
			Name:        c.Name,
			Description: c.Description,
			Tools:       tools,
		}
	}
	return cats
}

// categoryDescriptions maps category names to their UI descriptions.
var categoryDescriptions = map[string]string{
	"Database Introspection": "Analyze database performance, table health, and active connections",
	"Log Intelligence":       "Analyze log patterns, volumes, and distributed traces",
	"Connectors":             "View and manage database connectors",
	"Server Metrics":         "Monitor server infrastructure health and performance",
	"Connector Queries":      "Dynamic tools registered by active database connectors",
	"Health":                 "System health digests and summaries",
	"Settings":               "View and manage OpenTrace configuration",
	"Errors":                 "Track and manage application errors grouped by fingerprint",
	"Incidents":              "Investigate incidents with chronological event timelines",
	"Uptime":                 "Monitor HTTP endpoint availability, response times, and uptime percentage",
	"Overview":               "High-level system health summaries and multi-source investigation",
	"Agent Memory":           "Persistent notes for the AI agent to carry context across sessions",
	"Deep Capture":           "Query deep capture data: request/response details, SQL queries, HTTP calls, emails, audit trail, file operations",
}

// CatalogBuilder accumulates tool entries and builds a ToolCatalog.
// A nil *CatalogBuilder is safe to call Add on (it's a no-op).
type CatalogBuilder struct {
	entries []CatalogEntry
	// Track insertion order of categories.
	categoryOrder []string
	seen          map[string]bool
}

// Add registers a tool entry. Safe to call on a nil receiver (no-op).
func (b *CatalogBuilder) Add(name, description, category, access, requires string) {
	if b == nil {
		return
	}
	b.entries = append(b.entries, CatalogEntry{
		Name:        name,
		Description: description,
		Access:      access,
		Category:    category,
		Requires:    requires,
	})
	if b.seen == nil {
		b.seen = make(map[string]bool)
	}
	if !b.seen[category] {
		b.seen[category] = true
		b.categoryOrder = append(b.categoryOrder, category)
	}
}

// Build creates a ToolCatalog from the accumulated entries,
// grouping by category in insertion order.
func (b *CatalogBuilder) Build() *ToolCatalog {
	if b == nil {
		return &ToolCatalog{}
	}

	// Group entries by category.
	groups := make(map[string][]CatalogEntry)
	for _, e := range b.entries {
		groups[e.Category] = append(groups[e.Category], e)
	}

	categories := make([]CatalogCategory, 0, len(b.categoryOrder))
	for _, name := range b.categoryOrder {
		desc := categoryDescriptions[name]
		categories = append(categories, CatalogCategory{
			Name:        name,
			Description: desc,
			Tools:       groups[name],
		})
	}

	return &ToolCatalog{categories: categories}
}

// BuildCatalog creates a ToolCatalog by running the gateway registration logic
// (catalog-only mode — no MCP server, tools are only cataloged).
func BuildCatalog(deps Deps) *ToolCatalog {
	b := &CatalogBuilder{}
	// Use the gateway builder which populates the CatalogBuilder.
	_ = buildGateway(deps, true, b)
	return b.Build()
}
