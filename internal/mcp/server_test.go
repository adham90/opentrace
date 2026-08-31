package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/store"
)

// --- helpers ---

// resultText extracts the text from a CallToolResult, assuming a single TextContent.
func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(r.Content))
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *TextContent, got %T", r.Content[0])
	}
	return tc.Text
}

// makeRequest creates a CallToolRequest with the given arguments.
func makeRequest(args map[string]any) *mcp.CallToolRequest {
	return MakeCallToolRequest("test", args)
}

// schemaProps extracts the properties map from a tool's InputSchema.
func schemaProps(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("failed to marshal InputSchema: %v", err)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("failed to unmarshal InputSchema: %v", err)
	}
	return schema.Properties
}

// schemaRequired extracts the required array from a tool's InputSchema.
func schemaRequired(t *testing.T, tool *mcp.Tool) []string {
	t.Helper()
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("failed to marshal InputSchema: %v", err)
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("failed to unmarshal InputSchema: %v", err)
	}
	return schema.Required
}

// --- convertTool tests ---

func TestConvertTool_StringParam(t *testing.T) {
	tool := convertTool(connector.Tool{
		Name:        "search_logs",
		Description: "Search through logs",
		Params: []connector.ToolParam{
			{Name: "query", Type: "string", Required: true},
		},
	})

	if tool.Name != "search_logs" {
		t.Errorf("name = %q, want %q", tool.Name, "search_logs")
	}
	if tool.Description != "Search through logs" {
		t.Errorf("description = %q, want %q", tool.Description, "Search through logs")
	}

	props := schemaProps(t, tool)
	if _, ok := props["query"]; !ok {
		t.Fatal("expected 'query' property in schema")
	}

	required := schemaRequired(t, tool)
	if len(required) != 1 || required[0] != "query" {
		t.Errorf("required = %v, want [query]", required)
	}
}

func TestConvertTool_IntParam(t *testing.T) {
	tool := convertTool(connector.Tool{
		Name:        "get_logs",
		Description: "Get logs with limit",
		Params: []connector.ToolParam{
			{Name: "limit", Type: "int", Required: false},
		},
	})

	props := schemaProps(t, tool)
	if _, ok := props["limit"]; !ok {
		t.Fatal("expected 'limit' property in schema")
	}

	// Not required — should not appear in Required list
	required := schemaRequired(t, tool)
	if len(required) != 0 {
		t.Errorf("required = %v, want empty", required)
	}
}

func TestConvertTool_BoolParam(t *testing.T) {
	tool := convertTool(connector.Tool{
		Name:        "run_query",
		Description: "Run a query",
		Params: []connector.ToolParam{
			{Name: "verbose", Type: "bool", Required: true},
		},
	})

	props := schemaProps(t, tool)
	if _, ok := props["verbose"]; !ok {
		t.Fatal("expected 'verbose' property in schema")
	}

	required := schemaRequired(t, tool)
	if len(required) != 1 || required[0] != "verbose" {
		t.Errorf("required = %v, want [verbose]", required)
	}
}

func TestConvertTool_MultipleParams(t *testing.T) {
	tool := convertTool(connector.Tool{
		Name:        "search",
		Description: "Search",
		Params: []connector.ToolParam{
			{Name: "query", Type: "string", Required: true},
			{Name: "limit", Type: "int", Required: false},
			{Name: "exact", Type: "bool", Required: true},
		},
	})

	props := schemaProps(t, tool)
	if len(props) != 3 {
		t.Fatalf("properties count = %d, want 3", len(props))
	}

	for _, name := range []string{"query", "limit", "exact"} {
		if _, ok := props[name]; !ok {
			t.Errorf("missing property %q", name)
		}
	}

	// query and exact are required; limit is not
	requiredSet := make(map[string]bool)
	for _, r := range schemaRequired(t, tool) {
		requiredSet[r] = true
	}
	if !requiredSet["query"] || !requiredSet["exact"] {
		t.Errorf("required = %v, want query and exact", schemaRequired(t, tool))
	}
	if requiredSet["limit"] {
		t.Error("limit should not be required")
	}
}

func TestConvertTool_NoParams(t *testing.T) {
	tool := convertTool(connector.Tool{
		Name:        "list_tables",
		Description: "List all tables",
		Params:      nil,
	})

	if tool.Name != "list_tables" {
		t.Errorf("name = %q, want %q", tool.Name, "list_tables")
	}

	props := schemaProps(t, tool)
	if len(props) != 0 {
		t.Errorf("properties count = %d, want 0", len(props))
	}
}

// --- bridgeHandler tests ---

func TestBridgeHandler_Success(t *testing.T) {
	tool := connector.Tool{
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
	tool := connector.Tool{
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
	tool := connector.Tool{
		Name:        "check_args",
		Description: "Check args",
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			receivedArgs = args
			return "ok", nil
		},
	}

	handler := bridgeHandler(tool)

	// Create request with nil arguments
	req := MakeCallToolRequest("check_args", nil)

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

// mockDataSource implements connector.DataSource for testing.
type mockDataSource struct {
	connType connector.ConnectorType
	tools    []connector.Tool
}

func (m *mockDataSource) Type() connector.ConnectorType            { return m.connType }
func (m *mockDataSource) TestConnection(ctx context.Context) error { return nil }
func (m *mockDataSource) Tools() []connector.Tool                  { return m.tools }
func (m *mockDataSource) Close() error                             { return nil }

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

// --- Mock stores for watcher/alert tests ---

// previewMockQE implements DataSource + QueryExecutor for preview tests.
type previewMockQE struct {
	mockDataSource
	result *connector.QueryResult
}

func (m *previewMockQE) ExecuteReadQuery(ctx context.Context, query string) (*connector.QueryResult, error) {
	return m.result, nil
}

// --- Mock UserStore for auth tests ---

type mockUserStore struct {
	users map[string]*store.User // keyed by MCP token
	err   error
}

func (m *mockUserStore) Create(ctx context.Context, params store.CreateUserParams) (*store.User, error) {
	return nil, m.err
}

func (m *mockUserStore) GetByID(ctx context.Context, id string) (*store.User, error) {
	return nil, m.err
}

func (m *mockUserStore) GetByEmail(ctx context.Context, email string) (*store.User, error) {
	return nil, m.err
}

func (m *mockUserStore) GetByMCPToken(ctx context.Context, token string) (*store.User, error) {
	if m.err != nil {
		return nil, m.err
	}
	if u, ok := m.users[token]; ok {
		if u.MCPEnabled && u.IsActive {
			return u, nil
		}
		return nil, store.ErrNotFound
	}
	return nil, store.ErrNotFound
}

func (m *mockUserStore) List(ctx context.Context) ([]store.User, error) {
	return nil, m.err
}

func (m *mockUserStore) Update(ctx context.Context, id string, params store.UpdateUserParams) (*store.User, error) {
	return nil, m.err
}

func (m *mockUserStore) UpdatePassword(ctx context.Context, id string, passwordHash string) error {
	return m.err
}

func (m *mockUserStore) UpdateMCPToken(ctx context.Context, id string, token string) error {
	return m.err
}

func (m *mockUserStore) Delete(ctx context.Context, id string) error {
	return m.err
}

func (m *mockUserStore) Count(ctx context.Context) (int, error) {
	return len(m.users), m.err
}

// --- NewConfiguredServer tests ---

func TestNewConfiguredServer_MinimalDeps(t *testing.T) {
	deps := Deps{
		Registry: connector.NewRegistry(),
	}
	s := NewConfiguredServer(deps, false, nil)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestNewConfiguredServer_CustomServerName(t *testing.T) {
	deps := Deps{
		Registry:   connector.NewRegistry(),
		ServerName: "my-custom-server",
	}
	s := NewConfiguredServer(deps, false, nil)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestNewConfiguredServer_WithActivityStore(t *testing.T) {
	as := &mockMCPActivityStore{}
	deps := Deps{
		Registry: connector.NewRegistry(),
		Stores:   store.Stores{MCPActivityStore: as},
	}
	// ActivityLogger should be auto-created when MCPActivityStore is set.
	s := NewConfiguredServer(deps, false, nil)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestNewConfiguredServer_PresetActivityLogger(t *testing.T) {
	as := &mockMCPActivityStore{}
	al := NewActivityLogger(context.Background(), as, 8, 1)
	defer al.Close()

	deps := Deps{
		Registry:       connector.NewRegistry(),
		Stores:         store.Stores{MCPActivityStore: as},
		ActivityLogger: al,
	}
	s := NewConfiguredServer(deps, true, nil)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestNewConfiguredServer_AdminRegistersWriteTools(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockDataSource{
		connType: connector.ConnectorDatabase,
		tools: []connector.Tool{
			{Name: "run_query", Description: "Run SQL"},
		},
	})

	deps := Deps{
		Registry: registry,
	}
	// Admin mode should not panic with connector tools.
	s := NewConfiguredServer(deps, true, nil)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
}

// --- wrapHandler tests ---

func TestWrapHandler_WithoutActivityStore(t *testing.T) {
	deps := Deps{
		Registry: connector.NewRegistry(),
	}
	called := false
	inner := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return NewToolResultText("ok"), nil
	}
	handler := wrapHandler(deps, "test_tool", inner)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("inner handler was not called")
	}
	if result.IsError {
		t.Error("expected success result")
	}
}

func TestWrapHandler_WithActivityStore(t *testing.T) {
	as := &mockMCPActivityStore{}
	al := NewActivityLogger(context.Background(), as, 8, 1)
	defer al.Close()

	deps := Deps{
		Registry:       connector.NewRegistry(),
		Stores:         store.Stores{MCPActivityStore: as},
		ActivityLogger: al,
	}
	called := false
	inner := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return NewToolResultText("logged"), nil
	}
	handler := wrapHandler(deps, "test_tool", inner)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("inner handler was not called")
	}
	text := resultText(t, result)
	if text != "logged" {
		t.Errorf("text = %q, want %q", text, "logged")
	}
}

// --- buildGateway tests ---

func TestBuildGateway_ReadOnly(t *testing.T) {
	deps := Deps{
		Registry: connector.NewRegistry(),
	}
	b := &CatalogBuilder{}
	gw := buildGateway(deps, false, b)
	if gw == nil {
		t.Fatal("expected non-nil gateway")
	}
	// Should have at least connectors, database, overview, setup tools.
	if len(gw.entries) < 4 {
		t.Errorf("expected at least 4 gateway entries, got %d", len(gw.entries))
	}
	// Admin tool should NOT be registered.
	for _, e := range gw.entries {
		if e.Name == "admin" {
			t.Error("admin tool should not be registered in read-only mode")
		}
	}
}

func TestBuildGateway_Admin(t *testing.T) {
	deps := Deps{
		Registry: connector.NewRegistry(),
	}
	b := &CatalogBuilder{}
	gw := buildGateway(deps, true, b)
	if gw == nil {
		t.Fatal("expected non-nil gateway")
	}
	// Admin tool should be registered.
	found := false
	for _, e := range gw.entries {
		if e.Name == "admin" {
			found = true
			break
		}
	}
	if !found {
		t.Error("admin tool should be registered in admin mode")
	}
}

// testAccessControl mirrors the access-control logic from Serve() so we can
// test it without starting a blocking stdio server.
func testAccessControl(deps Deps) (hasAccess, isAdmin bool) {
	hasAccess = true
	isAdmin = true
	if deps.UserStore != nil && deps.MCPToken != "" {
		ctx := context.Background()
		user, err := deps.UserStore.GetByMCPToken(ctx, deps.MCPToken)
		if err != nil || user == nil {
			hasAccess = false
			return
		}
		isAdmin = user.Role == store.RoleAdmin
	}
	return
}

// --- Access-control logic tests ---

func TestAccessControl_NoUserStore(t *testing.T) {
	deps := Deps{
		Registry: connector.NewRegistry(),
	}
	hasAccess, isAdmin := testAccessControl(deps)
	if !hasAccess {
		t.Error("expected hasAccess=true when no UserStore is set")
	}
	if !isAdmin {
		t.Error("expected isAdmin=true when no UserStore is set")
	}
}

func TestAccessControl_ValidAdminToken(t *testing.T) {
	token := "admin-token-abc123"
	us := &mockUserStore{
		users: map[string]*store.User{
			token: {
				ID:         "u1",
				Email:      "admin@example.com",
				Role:       store.RoleAdmin,
				MCPEnabled: true,
				IsActive:   true,
			},
		},
	}

	deps := Deps{
		Registry: connector.NewRegistry(),
		Stores:   store.Stores{UserStore: us},
		MCPToken: token,
	}
	hasAccess, isAdmin := testAccessControl(deps)
	if !hasAccess {
		t.Error("expected hasAccess=true for valid admin token")
	}
	if !isAdmin {
		t.Error("expected isAdmin=true for admin user")
	}
}

func TestAccessControl_ValidMemberToken(t *testing.T) {
	token := "member-token-xyz789"
	us := &mockUserStore{
		users: map[string]*store.User{
			token: {
				ID:         "u2",
				Email:      "member@example.com",
				Role:       store.RoleMember,
				MCPEnabled: true,
				IsActive:   true,
			},
		},
	}

	deps := Deps{
		Registry: connector.NewRegistry(),
		Stores:   store.Stores{UserStore: us},
		MCPToken: token,
	}
	hasAccess, isAdmin := testAccessControl(deps)
	if !hasAccess {
		t.Error("expected hasAccess=true for valid member token")
	}
	if isAdmin {
		t.Error("expected isAdmin=false for member user")
	}
}

func TestAccessControl_InvalidToken(t *testing.T) {
	us := &mockUserStore{
		users: map[string]*store.User{}, // no users
	}

	deps := Deps{
		Registry: connector.NewRegistry(),
		Stores:   store.Stores{UserStore: us},
		MCPToken: "bad-token",
	}
	hasAccess, _ := testAccessControl(deps)
	if hasAccess {
		t.Error("expected hasAccess=false for invalid token")
	}
}

func TestAccessControl_EmptyToken(t *testing.T) {
	us := &mockUserStore{
		users: map[string]*store.User{},
	}

	deps := Deps{
		Registry: connector.NewRegistry(),
		Stores:   store.Stores{UserStore: us},
		MCPToken: "",
	}
	hasAccess, isAdmin := testAccessControl(deps)
	if !hasAccess {
		t.Error("expected hasAccess=true when MCPToken is empty (backward compat)")
	}
	if !isAdmin {
		t.Error("expected isAdmin=true when MCPToken is empty (backward compat)")
	}
}

// Catch-up cursor: these doubles do not exercise it, so the methods exist only
// to satisfy store.UserStore.
func (m *mockUserStore) CatchupCursor(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}

func (m *mockUserStore) SetCatchupCursor(context.Context, string, time.Time) error {
	return nil
}
