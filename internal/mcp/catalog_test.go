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
	ls := &mockLogStore{}

	cat := BuildCatalog(Deps{
		Registry: registry,
		LogStore: ls,
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

	// Verify key tools are present.
	expected := []string{
		"list_connectors",
		"db_query_stats", "db_table_stats", "db_activity", "db_locks",
		"log_stats", "trace_lookup", "compare_periods",
		"db_index_analysis", "connection_pool_stats",
		"explain_query",
	}
	for _, name := range expected {
		if !toolNames[name] {
			t.Errorf("expected tool %q in catalog", name)
		}
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

	// Tools that require LogStore should be absent.
	if toolNames["log_stats"] {
		t.Error("log_stats should not be in catalog without LogStore")
	}
	if toolNames["compare_periods"] {
		t.Error("compare_periods should not be in catalog without LogStore")
	}

	// Tools that don't require optional deps should be present.
	if !toolNames["list_connectors"] {
		t.Error("list_connectors should always be in catalog")
	}
	if !toolNames["db_query_stats"] {
		t.Error("db_query_stats should always be in catalog")
	}
}
