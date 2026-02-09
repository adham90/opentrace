package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/opentrace/opentrace/internal/agent"
	"github.com/opentrace/opentrace/internal/connector"
)

// --- helpers ---

// resultText extracts the text from a CallToolResult, assuming a single TextContent.
func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(r.Content))
	}
	tc, ok := mcp.AsTextContent(r.Content[0])
	if !ok {
		t.Fatalf("expected TextContent, got %T", r.Content[0])
	}
	return tc.Text
}

// makeRequest creates a CallToolRequest with the given arguments.
func makeRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	}
}

// --- convertTool tests ---

func TestConvertTool_StringParam(t *testing.T) {
	tool := convertTool(agent.Tool{
		Name:        "search_logs",
		Description: "Search through logs",
		Params: []agent.ToolParam{
			{Name: "query", Type: "string", Required: true},
		},
	})

	if tool.Name != "search_logs" {
		t.Errorf("name = %q, want %q", tool.Name, "search_logs")
	}
	if tool.Description != "Search through logs" {
		t.Errorf("description = %q, want %q", tool.Description, "Search through logs")
	}

	props := tool.InputSchema.Properties
	if _, ok := props["query"]; !ok {
		t.Fatal("expected 'query' property in schema")
	}

	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "query" {
		t.Errorf("required = %v, want [query]", tool.InputSchema.Required)
	}
}

func TestConvertTool_IntParam(t *testing.T) {
	tool := convertTool(agent.Tool{
		Name:        "get_logs",
		Description: "Get logs with limit",
		Params: []agent.ToolParam{
			{Name: "limit", Type: "int", Required: false},
		},
	})

	if _, ok := tool.InputSchema.Properties["limit"]; !ok {
		t.Fatal("expected 'limit' property in schema")
	}

	// Not required — should not appear in Required list
	if len(tool.InputSchema.Required) != 0 {
		t.Errorf("required = %v, want empty", tool.InputSchema.Required)
	}
}

func TestConvertTool_BoolParam(t *testing.T) {
	tool := convertTool(agent.Tool{
		Name:        "run_query",
		Description: "Run a query",
		Params: []agent.ToolParam{
			{Name: "verbose", Type: "bool", Required: true},
		},
	})

	if _, ok := tool.InputSchema.Properties["verbose"]; !ok {
		t.Fatal("expected 'verbose' property in schema")
	}

	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "verbose" {
		t.Errorf("required = %v, want [verbose]", tool.InputSchema.Required)
	}
}

func TestConvertTool_MultipleParams(t *testing.T) {
	tool := convertTool(agent.Tool{
		Name:        "search",
		Description: "Search",
		Params: []agent.ToolParam{
			{Name: "query", Type: "string", Required: true},
			{Name: "limit", Type: "int", Required: false},
			{Name: "exact", Type: "bool", Required: true},
		},
	})

	if len(tool.InputSchema.Properties) != 3 {
		t.Fatalf("properties count = %d, want 3", len(tool.InputSchema.Properties))
	}

	for _, name := range []string{"query", "limit", "exact"} {
		if _, ok := tool.InputSchema.Properties[name]; !ok {
			t.Errorf("missing property %q", name)
		}
	}

	// query and exact are required; limit is not
	requiredSet := make(map[string]bool)
	for _, r := range tool.InputSchema.Required {
		requiredSet[r] = true
	}
	if !requiredSet["query"] || !requiredSet["exact"] {
		t.Errorf("required = %v, want query and exact", tool.InputSchema.Required)
	}
	if requiredSet["limit"] {
		t.Error("limit should not be required")
	}
}

func TestConvertTool_NoParams(t *testing.T) {
	tool := convertTool(agent.Tool{
		Name:        "list_tables",
		Description: "List all tables",
		Params:      nil,
	})

	if tool.Name != "list_tables" {
		t.Errorf("name = %q, want %q", tool.Name, "list_tables")
	}

	if len(tool.InputSchema.Properties) != 0 {
		t.Errorf("properties count = %d, want 0", len(tool.InputSchema.Properties))
	}
}

// --- bridgeHandler tests ---

func TestBridgeHandler_Success(t *testing.T) {
	tool := agent.Tool{
		Name:        "echo",
		Description: "Echo input",
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "hello " + args["name"].(string), nil
		},
	}

	handler := bridgeHandler(tool)
	result, err := handler(context.Background(), makeRequest(map[string]any{"name": "world"}))

	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result, got error")
	}

	text := resultText(t, result)
	if text != "hello world" {
		t.Errorf("text = %q, want %q", text, "hello world")
	}
}

func TestBridgeHandler_Error(t *testing.T) {
	tool := agent.Tool{
		Name:        "fail",
		Description: "Always fails",
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "", errors.New("something broke")
		},
	}

	handler := bridgeHandler(tool)
	result, err := handler(context.Background(), makeRequest(nil))

	// Transport error should be nil — tool errors are results
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result, got success")
	}

	text := resultText(t, result)
	if text != "something broke" {
		t.Errorf("text = %q, want %q", text, "something broke")
	}
}

func TestBridgeHandler_NilArgs(t *testing.T) {
	var receivedArgs map[string]any
	tool := agent.Tool{
		Name:        "check_args",
		Description: "Check args",
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			receivedArgs = args
			return "ok", nil
		},
	}

	handler := bridgeHandler(tool)

	// Create request with nil arguments
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: nil,
		},
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}

	if receivedArgs == nil {
		t.Fatal("handler received nil args, expected empty map")
	}
}

// --- listConnectorsHandler tests ---

// mockDataSource implements connector.DataSource for testing.
type mockDataSource struct {
	connType connector.ConnectorType
	tools    []agent.Tool
}

func (m *mockDataSource) Type() connector.ConnectorType                      { return m.connType }
func (m *mockDataSource) TestConnection(ctx context.Context) error           { return nil }
func (m *mockDataSource) Tools() []agent.Tool                               { return m.tools }
func (m *mockDataSource) Close() error                                      { return nil }

func TestListConnectorsHandler_Empty(t *testing.T) {
	registry := connector.NewRegistry()
	handler := listConnectorsHandler(registry)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if text != "No connectors are currently active." {
		t.Errorf("text = %q, want 'No connectors are currently active.'", text)
	}
}

func TestListConnectorsHandler_WithTools(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockDataSource{
		connType: connector.ConnectorLogs,
		tools: []agent.Tool{
			{Name: "search_logs", Description: "Search through log entries"},
		},
	})
	registry.Register(&mockDataSource{
		connType: connector.ConnectorDatabase,
		tools: []agent.Tool{
			{Name: "run_query", Description: "Run a SQL query"},
		},
	})

	handler := listConnectorsHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}

	text := resultText(t, result)

	// Should mention both tools
	if !contains(text, "search_logs") {
		t.Error("expected output to contain 'search_logs'")
	}
	if !contains(text, "run_query") {
		t.Error("expected output to contain 'run_query'")
	}
	if !contains(text, "Active tools (2)") {
		t.Error("expected output to contain 'Active tools (2)'")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
