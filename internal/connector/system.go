package connector

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrace/opentrace/internal/agent"
	"github.com/opentrace/opentrace/internal/store"
)

// SystemConnector provides system-level tools (memory) that are always available.
type SystemConnector struct {
	memoryStore store.MemoryStore
}

// NewSystemConnector creates a new SystemConnector.
func NewSystemConnector(memoryStore store.MemoryStore) *SystemConnector {
	return &SystemConnector{memoryStore: memoryStore}
}

func (c *SystemConnector) Type() ConnectorType { return ConnectorSystem }

func (c *SystemConnector) TestConnection(ctx context.Context) error {
	// Always available
	return nil
}

func (c *SystemConnector) Close() error { return nil }

func (c *SystemConnector) Tools() []agent.Tool {
	return []agent.Tool{
		{
			Name: "memory_read",
			Description: `Read from the system knowledge base. Returns facts learned from previous investigations.
Call with no args to get all memories, or filter by category or search query.

Categories: schema, pattern, error, data, query

Tips:
- Call this at the start of an investigation to see what you already know
- Use category="schema" to recall table structures and relationships
- Use category="query" to recall useful SQL patterns
- Use query="payments" to search for facts mentioning payments`,
			Params: []agent.ToolParam{
				{Name: "category", Type: "string", Required: false},
				{Name: "query", Type: "string", Required: false},
			},
			Handler: c.handleMemoryRead,
		},
		{
			Name: "memory_write",
			Description: `Save a fact to the system knowledge base for future investigations.
Use this when you discover something useful that would help in future queries.
Duplicate facts are automatically deduplicated.

Categories: schema, pattern, error, data, query

Examples:
- category="schema", content="users.status is always one of: active, suspended, deleted"
- category="pattern", content="To find failed payments, join orders with payments on order_id"
- category="error", content="The auth-service logs 'token expired' errors when JWT TTL is exceeded"`,
			Params: []agent.ToolParam{
				{Name: "category", Type: "string", Required: true},
				{Name: "content", Type: "string", Required: true},
			},
			Handler: c.handleMemoryWrite,
		},
	}
}

func (c *SystemConnector) handleMemoryRead(ctx context.Context, args map[string]any) (string, error) {
	category, _ := args["category"].(string)
	query, _ := args["query"].(string)

	var entries []store.MemoryEntry
	var err error

	if query != "" {
		entries, err = c.memoryStore.SearchMemories(ctx, query)
	} else {
		entries, err = c.memoryStore.ListMemories(ctx, category)
	}
	if err != nil {
		return "", fmt.Errorf("reading memory: %w", err)
	}

	if len(entries) == 0 {
		return "No memories found. The knowledge base is empty.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d memories:\n\n", len(entries)))
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", e.Category, e.Content))
	}
	return sb.String(), nil
}

func (c *SystemConnector) handleMemoryWrite(ctx context.Context, args map[string]any) (string, error) {
	category, ok := args["category"].(string)
	if !ok || category == "" {
		return "", fmt.Errorf("category parameter is required")
	}
	content, ok := args["content"].(string)
	if !ok || content == "" {
		return "", fmt.Errorf("content parameter is required")
	}

	// Validate category
	validCategories := map[string]bool{
		"schema": true, "pattern": true, "error": true, "data": true, "query": true,
	}
	if !validCategories[category] {
		return "", fmt.Errorf("invalid category %q, must be one of: schema, pattern, error, data, query", category)
	}

	if err := c.memoryStore.AddMemory(ctx, category, content, ""); err != nil {
		return "", fmt.Errorf("writing memory: %w", err)
	}

	return fmt.Sprintf("Saved to knowledge base [%s]: %s", category, content), nil
}
