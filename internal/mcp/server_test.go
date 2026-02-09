package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/opentrace/opentrace/internal/agent"
	"github.com/opentrace/opentrace/internal/connector"
	"github.com/opentrace/opentrace/internal/store"
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

// --- Mock stores for watcher/alert tests ---

type mockWatcherStore struct {
	watchers []store.Watcher
	created  *store.Watcher
	err      error
}

func (m *mockWatcherStore) List(ctx context.Context) ([]store.Watcher, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.watchers, nil
}

func (m *mockWatcherStore) Create(ctx context.Context, params store.CreateWatcherParams) (*store.Watcher, error) {
	if m.err != nil {
		return nil, m.err
	}
	w := &store.Watcher{
		ID:              uuid.New(),
		Title:           params.Title,
		Description:     params.Description,
		Severity:        params.Severity,
		Filters:         params.Filters,
		IntervalSeconds: params.IntervalSeconds,
		Status:          store.WatcherActive,
		Notify:          params.Notify,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	m.created = w
	return w, nil
}

func (m *mockWatcherStore) GetByID(ctx context.Context, id uuid.UUID) (*store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) Update(ctx context.Context, id uuid.UUID, params store.UpdateWatcherParams) (*store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) UpdateStatus(ctx context.Context, id uuid.UUID, status store.WatcherStatus) (*store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) Delete(ctx context.Context, id uuid.UUID) error { return nil }
func (m *mockWatcherStore) GetDueWatchers(ctx context.Context) ([]store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) UpdateRunTime(ctx context.Context, id uuid.UUID, lastRun, nextRun time.Time) error {
	return nil
}

type mockAlertStore struct {
	alerts []store.Alert
	err    error
}

func (m *mockAlertStore) List(ctx context.Context, params store.ListAlertParams) ([]store.Alert, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.alerts, nil
}

func (m *mockAlertStore) Create(ctx context.Context, params store.CreateAlertParams) (*store.Alert, error) {
	return nil, nil
}
func (m *mockAlertStore) CountUnread(ctx context.Context) (int, error)          { return 0, nil }
func (m *mockAlertStore) MarkRead(ctx context.Context, id uuid.UUID) error      { return nil }
func (m *mockAlertStore) Dismiss(ctx context.Context, id uuid.UUID) error       { return nil }

// --- listWatchersHandler tests ---

func TestListWatchersHandler_Empty(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := listWatchersHandler(ws)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if text != "No watchers configured." {
		t.Errorf("text = %q, want %q", text, "No watchers configured.")
	}
}

func TestListWatchersHandler_WithWatchers(t *testing.T) {
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: uuid.New(), Title: "Error Monitor", Status: store.WatcherActive},
			{ID: uuid.New(), Title: "Timeout Watcher", Status: store.WatcherPaused},
		},
	}
	handler := listWatchersHandler(ws)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}

	text := resultText(t, result)
	if !contains(text, "Error Monitor") {
		t.Error("expected output to contain 'Error Monitor'")
	}
	if !contains(text, "Timeout Watcher") {
		t.Error("expected output to contain 'Timeout Watcher'")
	}
}

func TestListWatchersHandler_Error(t *testing.T) {
	ws := &mockWatcherStore{err: errors.New("db error")}
	handler := listWatchersHandler(ws)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}

// --- createWatcherHandler tests ---

func TestCreateWatcherHandler_Success(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := createWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"title":            "Error Monitor",
		"description":      "Watch for error spikes in production",
		"service":          "api",
		"level":            "error",
		"environment":      "production",
		"interval_minutes": float64(10),
		"severity":         "critical",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "Watcher created successfully") {
		t.Errorf("text = %q, want it to contain 'Watcher created successfully'", text)
	}

	// Verify the watcher was created with correct params
	if ws.created == nil {
		t.Fatal("watcher was not created")
	}
	if ws.created.Title != "Error Monitor" {
		t.Errorf("title = %q, want %q", ws.created.Title, "Error Monitor")
	}
	if ws.created.IntervalSeconds != 600 {
		t.Errorf("interval = %d, want 600", ws.created.IntervalSeconds)
	}
	if ws.created.Severity != store.SeverityCritical {
		t.Errorf("severity = %q, want %q", ws.created.Severity, store.SeverityCritical)
	}

	// Verify filters
	var filters map[string]string
	json.Unmarshal(ws.created.Filters, &filters)
	if filters["service"] != "api" {
		t.Errorf("filter service = %q, want %q", filters["service"], "api")
	}
	if filters["level"] != "error" {
		t.Errorf("filter level = %q, want %q", filters["level"], "error")
	}
}

func TestCreateWatcherHandler_MissingTitle(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := createWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"description": "some description",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing title")
	}
}

func TestCreateWatcherHandler_Defaults(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := createWatcherHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"title":       "Simple Watcher",
		"description": "Just watch",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	if ws.created.IntervalSeconds != 300 {
		t.Errorf("default interval = %d, want 300", ws.created.IntervalSeconds)
	}
	if ws.created.Severity != store.SeverityWarning {
		t.Errorf("default severity = %q, want %q", ws.created.Severity, store.SeverityWarning)
	}
}

// --- listAlertsHandler tests ---

func TestListAlertsHandler_Empty(t *testing.T) {
	as := &mockAlertStore{}
	handler := listAlertsHandler(as)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if text != "No alerts found." {
		t.Errorf("text = %q, want %q", text, "No alerts found.")
	}
}

func TestListAlertsHandler_WithAlerts(t *testing.T) {
	as := &mockAlertStore{
		alerts: []store.Alert{
			{ID: uuid.New(), Title: "Error spike detected", Severity: store.SeverityCritical},
			{ID: uuid.New(), Title: "High latency", Severity: store.SeverityWarning},
		},
	}
	handler := listAlertsHandler(as)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"limit": float64(5),
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}

	text := resultText(t, result)
	if !contains(text, "Error spike detected") {
		t.Error("expected output to contain 'Error spike detected'")
	}
	if !contains(text, "High latency") {
		t.Error("expected output to contain 'High latency'")
	}
}

func TestListAlertsHandler_Error(t *testing.T) {
	as := &mockAlertStore{err: errors.New("db error")}
	handler := listAlertsHandler(as)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}
