package mcp

import (
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestCatalogBuilder_NilSafe(t *testing.T) {
	var b *CatalogBuilder
	// Should not panic.
	b.Add("tool", "desc", "cat", "read", "")
	cat := b.Build()
	if len(cat.Categories()) != 0 {
		t.Errorf("nil builder Build() should return empty catalog, got %d categories", len(cat.Categories()))
	}
}

func TestCatalogBuilder_AddAndBuild(t *testing.T) {
	b := &CatalogBuilder{}
	b.Add("db_query_stats", "Show top queries", "Database Introspection", "read", "database connector")
	b.Add("db_table_stats", "Show table stats", "Database Introspection", "read", "database connector")
	b.Add("log_stats", "Log stats", "Log Intelligence", "read", "")
	b.Add("trace_lookup", "Trace lookup", "Log Intelligence", "read", "")

	cat := b.Build()
	categories := cat.Categories()

	if len(categories) != 2 {
		t.Fatalf("expected 2 categories, got %d", len(categories))
	}

	// Verify insertion order is preserved.
	if categories[0].Name != "Database Introspection" {
		t.Errorf("category[0] = %q, want %q", categories[0].Name, "Database Introspection")
	}
	if categories[1].Name != "Log Intelligence" {
		t.Errorf("category[1] = %q, want %q", categories[1].Name, "Log Intelligence")
	}

	// Verify tool counts per category.
	if len(categories[0].Tools) != 2 {
		t.Errorf("Database Introspection tools = %d, want 2", len(categories[0].Tools))
	}
	if len(categories[1].Tools) != 2 {
		t.Errorf("Log Intelligence tools = %d, want 2", len(categories[1].Tools))
	}

	// Verify tool fields.
	dbTool := categories[0].Tools[0]
	if dbTool.Name != "db_query_stats" {
		t.Errorf("tool name = %q, want %q", dbTool.Name, "db_query_stats")
	}
	if dbTool.Access != "read" {
		t.Errorf("tool access = %q, want %q", dbTool.Access, "read")
	}
	if dbTool.Requires != "database connector" {
		t.Errorf("tool requires = %q, want %q", dbTool.Requires, "database connector")
	}
}

func TestCatalogBuilder_CategoryDescriptions(t *testing.T) {
	b := &CatalogBuilder{}
	b.Add("log_stats", "Log stats", "Log Intelligence", "read", "")

	cat := b.Build()
	categories := cat.Categories()

	if len(categories) != 1 {
		t.Fatalf("expected 1 category, got %d", len(categories))
	}
	if categories[0].Description == "" {
		t.Error("expected category description to be populated from categoryDescriptions map")
	}
}

func TestToolCatalog_NilCategories(t *testing.T) {
	var tc *ToolCatalog
	if tc.Categories() != nil {
		t.Error("nil ToolCatalog.Categories() should return nil")
	}
}

func TestBuildCatalog_WithDeps(t *testing.T) {
	registry := connector.NewRegistry()

	cat := BuildCatalog(Deps{
		Registry: registry,
	})

	categories := cat.Categories()
	if len(categories) == 0 {
		t.Fatal("expected non-empty catalog")
	}

	// Collect all tool names.
	toolNames := make(map[string]bool)
	for _, c := range categories {
		for _, tool := range c.Tools {
			toolNames[tool.Name] = true
		}
	}

	// Verify consolidated tools are present.
	expected := []string{
		"connectors",
		"database",
		"overview",
	}
	for _, name := range expected {
		if !toolNames[name] {
			t.Errorf("expected tool %q in catalog", name)
		}
	}
}

func TestCategoriesForDisplay_NilCatalog(t *testing.T) {
	var tc *ToolCatalog
	result := tc.CategoriesForDisplay()
	if result != nil {
		t.Error("nil ToolCatalog.CategoriesForDisplay() should return nil")
	}
}

func TestCategoriesForDisplay_EmptyCatalog(t *testing.T) {
	tc := &ToolCatalog{}
	result := tc.CategoriesForDisplay()
	if len(result) != 0 {
		t.Errorf("empty catalog CategoriesForDisplay() should return empty slice, got %d", len(result))
	}
}

func TestCategoriesForDisplay_MapsCorrectly(t *testing.T) {
	b := &CatalogBuilder{}
	b.Add("db_query_stats", "Show top queries", "Database Introspection", "read", "database connector")
	b.Add("db_table_stats", "Show table stats", "Database Introspection", "admin", "")
	b.Add("log_stats", "Log volume stats", "Log Intelligence", "read", "")

	tc := b.Build()
	display := tc.CategoriesForDisplay()

	if len(display) != 2 {
		t.Fatalf("expected 2 categories, got %d", len(display))
	}

	// Verify first category mapping.
	if display[0].Name != "Database Introspection" {
		t.Errorf("category[0].Name = %q, want %q", display[0].Name, "Database Introspection")
	}
	if display[0].Description == "" {
		t.Error("category description should come from categoryDescriptions map")
	}
	if len(display[0].Tools) != 2 {
		t.Fatalf("category[0] tools = %d, want 2", len(display[0].Tools))
	}

	// Verify tool field mapping.
	tool := display[0].Tools[0]
	if tool.Name != "db_query_stats" {
		t.Errorf("tool.Name = %q, want %q", tool.Name, "db_query_stats")
	}
	if tool.Description != "Show top queries" {
		t.Errorf("tool.Description = %q, want %q", tool.Description, "Show top queries")
	}
	if tool.Access != "read" {
		t.Errorf("tool.Access = %q, want %q", tool.Access, "read")
	}
	if tool.Requires != "database connector" {
		t.Errorf("tool.Requires = %q, want %q", tool.Requires, "database connector")
	}

	// Verify second category.
	if display[1].Name != "Log Intelligence" {
		t.Errorf("category[1].Name = %q, want %q", display[1].Name, "Log Intelligence")
	}
	if len(display[1].Tools) != 1 {
		t.Fatalf("category[1] tools = %d, want 1", len(display[1].Tools))
	}
}

func TestCategoriesForDisplay_PreservesInsertionOrder(t *testing.T) {
	b := &CatalogBuilder{}
	b.Add("t1", "desc1", "Zebra Category", "read", "")
	b.Add("t2", "desc2", "Alpha Category", "read", "")
	b.Add("t3", "desc3", "Middle Category", "admin", "")

	tc := b.Build()
	display := tc.CategoriesForDisplay()

	if len(display) != 3 {
		t.Fatalf("expected 3 categories, got %d", len(display))
	}
	// Insertion order, NOT alphabetical.
	if display[0].Name != "Zebra Category" {
		t.Errorf("category[0] = %q, want %q", display[0].Name, "Zebra Category")
	}
	if display[1].Name != "Alpha Category" {
		t.Errorf("category[1] = %q, want %q", display[1].Name, "Alpha Category")
	}
	if display[2].Name != "Middle Category" {
		t.Errorf("category[2] = %q, want %q", display[2].Name, "Middle Category")
	}
}

func TestBuildCatalog_MinimalDeps(t *testing.T) {
	// With only a registry, conditional tools should be omitted.
	cat := BuildCatalog(Deps{
		Registry: connector.NewRegistry(),
	})

	categories := cat.Categories()
	toolNames := make(map[string]bool)
	for _, c := range categories {
		for _, tool := range c.Tools {
			toolNames[tool.Name] = true
		}
	}

	// The logs tool requires LogStore, so it should be absent.
	if toolNames["logs"] {
		t.Error("logs should not be in catalog without LogStore")
	}

	// Tools that don't require optional deps should be present.
	if !toolNames["connectors"] {
		t.Error("connectors should always be in catalog")
	}
	if !toolNames["database"] {
		t.Error("database should always be in catalog")
	}
}
