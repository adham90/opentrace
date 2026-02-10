package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
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

func (m *mockWatcherStore) List(ctx context.Context, params store.ListWatcherParams) ([]store.Watcher, error) {
	if m.err != nil {
		return nil, m.err
	}
	if params.Environment != "" {
		var filtered []store.Watcher
		for _, w := range m.watchers {
			if w.Environment == params.Environment {
				filtered = append(filtered, w)
			}
		}
		return filtered, nil
	}
	return m.watchers, nil
}

func (m *mockWatcherStore) Create(ctx context.Context, params store.CreateWatcherParams) (*store.Watcher, error) {
	if m.err != nil {
		return nil, m.err
	}
	effort := params.Effort
	if effort == "" {
		effort = store.EffortMedium
	}
	mt := params.MonitorType
	if mt == "" {
		mt = store.MonitorTypeAI
	}
	w := &store.Watcher{
		ID:           uuid.New(),
		Title:        params.Title,
		Description:  params.Description,
		MonitorType:  mt,
		RuleConfig:   params.RuleConfig,
		DataSourceID: params.DataSourceID,
		Severity:     params.Severity,
		Filters:      params.Filters,
		TimeRange:    params.TimeRange,
		Effort:       effort,
		Status:       store.WatcherActive,
		Notify:       params.Notify,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	m.created = w
	return w, nil
}

func (m *mockWatcherStore) GetByID(ctx context.Context, id uuid.UUID) (*store.Watcher, error) {
	for i := range m.watchers {
		if m.watchers[i].ID == id {
			return &m.watchers[i], nil
		}
	}
	return nil, store.ErrNotFound
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

func (m *mockWatcherStore) UpdateAdaptiveState(ctx context.Context, id uuid.UUID, params store.UpdateAdaptiveParams) error {
	return nil
}

func (m *mockWatcherStore) ResumeMonitor(ctx context.Context, id uuid.UUID) error {
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
func (m *mockAlertStore) CountUnread(ctx context.Context, environment string) (int, error) {
	return 0, nil
}
func (m *mockAlertStore) CountTotal(ctx context.Context, environment string) (int, error) {
	return 0, nil
}
func (m *mockAlertStore) MarkRead(ctx context.Context, id uuid.UUID) error    { return nil }
func (m *mockAlertStore) MarkAllRead(ctx context.Context, environment string) error { return nil }
func (m *mockAlertStore) Dismiss(ctx context.Context, id uuid.UUID) error     { return nil }
func (m *mockAlertStore) DismissAll(ctx context.Context, environment string) error { return nil }
func (m *mockAlertStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
}
func (m *mockAlertStore) GetByID(ctx context.Context, id uuid.UUID) (*store.Alert, error) {
	for i := range m.alerts {
		if m.alerts[i].ID == id {
			return &m.alerts[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockAlertStore) CountBySeverity(ctx context.Context, since, until time.Time, environment string) (map[string]int, error) {
	result := make(map[string]int)
	for _, a := range m.alerts {
		if a.CreatedAt.Before(since) || !a.CreatedAt.Before(until) {
			continue
		}
		if environment != "" && a.Environment != environment {
			continue
		}
		result[string(a.Severity)]++
	}
	return result, nil
}

// mockWatcherRunStore implements store.WatcherRunStore for MCP tests.
type mockWatcherRunStore struct {
	runs []store.WatcherRun
}

func (m *mockWatcherRunStore) Create(_ context.Context, watcherID uuid.UUID) (*store.WatcherRun, error) {
	return nil, nil
}
func (m *mockWatcherRunStore) Complete(_ context.Context, _ uuid.UUID, _ string, _ any, _ bool) error {
	return nil
}
func (m *mockWatcherRunStore) Fail(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (m *mockWatcherRunStore) FailStaleRuns(_ context.Context, _ time.Duration) (int, error) {
	return 0, nil
}
func (m *mockWatcherRunStore) List(_ context.Context, watcherID uuid.UUID, limit int) ([]store.WatcherRun, error) {
	var result []store.WatcherRun
	for _, r := range m.runs {
		if r.WatcherID == watcherID {
			result = append(result, r)
		}
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
func (m *mockWatcherRunStore) GetByID(_ context.Context, id uuid.UUID) (*store.WatcherRun, error) {
	for i := range m.runs {
		if m.runs[i].ID == id {
			return &m.runs[i], nil
		}
	}
	return nil, store.ErrNotFound
}
func (m *mockWatcherRunStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
func (m *mockWatcherRunStore) ListWithFilter(_ context.Context, watcherID uuid.UUID, limit int, status string) ([]store.WatcherRun, error) {
	var result []store.WatcherRun
	for _, r := range m.runs {
		if r.WatcherID != watcherID {
			continue
		}
		switch status {
		case "completed":
			if r.Status != "completed" {
				continue
			}
		case "failed", "error":
			if r.Status != "failed" && r.Status != "error" {
				continue
			}
		case "alerted":
			if r.Status != "completed" || !r.HasAlert {
				continue
			}
		}
		result = append(result, r)
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *mockWatcherRunStore) CountRuns(_ context.Context, params store.CountRunParams) (int, error) {
	count := 0
	for _, r := range m.runs {
		if r.StartedAt.Before(params.Since) || !r.StartedAt.Before(params.Until) {
			continue
		}
		if params.Status != "" && r.Status != params.Status {
			continue
		}
		if params.WatcherID != nil && r.WatcherID != *params.WatcherID {
			continue
		}
		count++
	}
	return count, nil
}

// --- listMonitorsHandler tests ---

func TestListMonitorsHandler_Empty(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := listMonitorsHandler(ws)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if text != "No monitors configured." {
		t.Errorf("text = %q, want %q", text, "No monitors configured.")
	}
}

func TestListMonitorsHandler_WithMonitors(t *testing.T) {
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: uuid.New(), Title: "Error Monitor", Status: store.WatcherActive, MonitorType: store.MonitorTypeAI},
			{ID: uuid.New(), Title: "Connection Check", Status: store.WatcherActive, MonitorType: store.MonitorTypeRule},
		},
	}
	handler := listMonitorsHandler(ws)

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
	if !contains(text, "Connection Check") {
		t.Error("expected output to contain 'Connection Check'")
	}
}

func TestListMonitorsHandler_FilterByType(t *testing.T) {
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: uuid.New(), Title: "AI Monitor", Status: store.WatcherActive, MonitorType: store.MonitorTypeAI},
			{ID: uuid.New(), Title: "Rule Monitor", Status: store.WatcherActive, MonitorType: store.MonitorTypeRule},
		},
	}
	// Override List to check the filter is passed.
	handler := listMonitorsHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"monitor_type": "rule",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}
	// The mock doesn't filter by type — just verify the call doesn't error.
	// The real store filters by type via the SQL query.
}

func TestListMonitorsHandler_Error(t *testing.T) {
	ws := &mockWatcherStore{err: errors.New("db error")}
	handler := listMonitorsHandler(ws)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}

// --- createMonitorHandler tests ---

func TestCreateMonitorHandler_AISuccess(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := createMonitorHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"title":       "Error Monitor",
		"description": "Watch for error spikes in production",
		"service":     "api",
		"level":       "error",
		"environment": "production",
		"time_range":  "10m",
		"severity":    "critical",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "Monitor created successfully") {
		t.Errorf("text = %q, want it to contain 'Monitor created successfully'", text)
	}

	if ws.created == nil {
		t.Fatal("monitor was not created")
	}
	if ws.created.Title != "Error Monitor" {
		t.Errorf("title = %q, want %q", ws.created.Title, "Error Monitor")
	}
	if ws.created.MonitorType != store.MonitorTypeAI {
		t.Errorf("monitor_type = %q, want %q", ws.created.MonitorType, store.MonitorTypeAI)
	}
	if ws.created.TimeRange != "10m" {
		t.Errorf("time_range = %q, want %q", ws.created.TimeRange, "10m")
	}
	if ws.created.Severity != store.SeverityCritical {
		t.Errorf("severity = %q, want %q", ws.created.Severity, store.SeverityCritical)
	}

	var filters map[string]string
	json.Unmarshal(ws.created.Filters, &filters)
	if filters["service"] != "api" {
		t.Errorf("filter service = %q, want %q", filters["service"], "api")
	}
}

func TestCreateMonitorHandler_RuleSuccess(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := createMonitorHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"title":          "Connection count",
		"monitor_type":   "rule",
		"rule_config":    `{"source":"query","query":"SELECT count(*) FROM pg_stat_activity","metric":"value","operator":"gt","threshold":100}`,
		"data_source_id": "ds-123",
		"severity":       "warning",
		"time_range":     "5m",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	if ws.created == nil {
		t.Fatal("monitor was not created")
	}
	if ws.created.MonitorType != store.MonitorTypeRule {
		t.Errorf("monitor_type = %q, want %q", ws.created.MonitorType, store.MonitorTypeRule)
	}
	if ws.created.RuleConfig == nil {
		t.Fatal("expected rule_config to be set")
	}
	if ws.created.RuleConfig.Source != store.RuleSourceQuery {
		t.Errorf("rule_config.source = %q, want %q", ws.created.RuleConfig.Source, store.RuleSourceQuery)
	}
	if ws.created.DataSourceID == nil || *ws.created.DataSourceID != "ds-123" {
		t.Errorf("data_source_id = %v, want ds-123", ws.created.DataSourceID)
	}
}

func TestCreateMonitorHandler_RuleMissingConfig(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := createMonitorHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"title":        "Rule without config",
		"monitor_type": "rule",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for rule monitor without rule_config")
	}
}

func TestCreateMonitorHandler_MissingTitle(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := createMonitorHandler(ws)

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

func TestCreateMonitorHandler_AIMissingDescription(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := createMonitorHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"title": "No description AI monitor",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for AI monitor without description")
	}
}

func TestCreateMonitorHandler_Defaults(t *testing.T) {
	ws := &mockWatcherStore{}
	handler := createMonitorHandler(ws)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"title":       "Simple Monitor",
		"description": "Just watch",
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	if ws.created.TimeRange != "15m" {
		t.Errorf("default time_range = %q, want %q", ws.created.TimeRange, "15m")
	}
	if ws.created.Severity != store.SeverityWarning {
		t.Errorf("default severity = %q, want %q", ws.created.Severity, store.SeverityWarning)
	}
	if ws.created.MonitorType != store.MonitorTypeAI {
		t.Errorf("default monitor_type = %q, want %q", ws.created.MonitorType, store.MonitorTypeAI)
	}
}

// --- previewMonitorHandler tests ---

func TestPreviewMonitorHandler_MissingConfig(t *testing.T) {
	re := watcher.NewRuleEvaluator(connector.NewRegistry(), nil, nil)
	handler := previewMonitorHandler(re)

	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing rule_config")
	}
}

func TestPreviewMonitorHandler_InvalidJSON(t *testing.T) {
	re := watcher.NewRuleEvaluator(connector.NewRegistry(), nil, nil)
	handler := previewMonitorHandler(re)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"rule_config": "not-json",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestPreviewMonitorHandler_EnhancedResponse(t *testing.T) {
	// Set up a registry with a mock QueryExecutor that returns a value.
	registry := connector.NewRegistry()
	registry.Register(&previewMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"count"},
			Rows:     [][]any{{int64(42)}},
			RowCount: 1,
		},
	})

	re := watcher.NewRuleEvaluator(registry, nil, nil)
	handler := previewMonitorHandler(re)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"rule_config": `{"source":"query","query":"SELECT count(*) FROM pg_stat_activity","metric":"value","operator":"gt","threshold":100}`,
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Check new fields exist.
	if resp["threshold"] != float64(100) {
		t.Errorf("threshold = %v, want 100", resp["threshold"])
	}
	if resp["operator"] != "gt" {
		t.Errorf("operator = %v, want gt", resp["operator"])
	}
	if _, ok := resp["recommendation"]; !ok {
		t.Error("expected recommendation field")
	}
	if _, ok := resp["query_result_sample"]; !ok {
		t.Error("expected query_result_sample field")
	}
	// 42 < 100, so should NOT trigger.
	if resp["would_alert"] != false {
		t.Errorf("would_alert = %v, want false", resp["would_alert"])
	}
	if !contains(resp["recommendation"].(string), "would NOT trigger") {
		t.Errorf("expected NOT-trigger recommendation, got: %s", resp["recommendation"])
	}
}

func TestPreviewMonitorHandler_WouldAlert(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&previewMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns:  []string{"count"},
			Rows:     [][]any{{int64(150)}},
			RowCount: 1,
		},
	})

	re := watcher.NewRuleEvaluator(registry, nil, nil)
	handler := previewMonitorHandler(re)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"rule_config": `{"source":"query","query":"SELECT count(*) FROM pg_stat_activity","metric":"value","operator":"gt","threshold":100}`,
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["would_alert"] != true {
		t.Errorf("would_alert = %v, want true", resp["would_alert"])
	}
	if !contains(resp["recommendation"].(string), "WOULD trigger") {
		t.Errorf("expected WOULD-trigger recommendation, got: %s", resp["recommendation"])
	}
}

// previewMockQE implements DataSource + QueryExecutor for preview tests.
type previewMockQE struct {
	mockDataSource
	result *connector.QueryResult
}

func (m *previewMockQE) ExecuteReadQuery(ctx context.Context, query string) (*connector.QueryResult, error) {
	return m.result, nil
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
	// No UserStore, no token → backward compat → full access.
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
		Registry:  connector.NewRegistry(),
		UserStore: us,
		MCPToken:  token,
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
		Registry:  connector.NewRegistry(),
		UserStore: us,
		MCPToken:  token,
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
		Registry:  connector.NewRegistry(),
		UserStore: us,
		MCPToken:  "bad-token",
	}
	hasAccess, _ := testAccessControl(deps)
	if hasAccess {
		t.Error("expected hasAccess=false for invalid token")
	}
}

func TestAccessControl_EmptyToken(t *testing.T) {
	// UserStore is set but token is empty → condition (UserStore != nil && MCPToken != "")
	// is false → backward compat → full access.
	us := &mockUserStore{
		users: map[string]*store.User{},
	}

	deps := Deps{
		Registry:  connector.NewRegistry(),
		UserStore: us,
		MCPToken:  "",
	}
	hasAccess, isAdmin := testAccessControl(deps)
	if !hasAccess {
		t.Error("expected hasAccess=true when MCPToken is empty (backward compat)")
	}
	if !isAdmin {
		t.Error("expected isAdmin=true when MCPToken is empty (backward compat)")
	}
}

// --- Tool registration tests ---

func TestAddReadOnlyTools_RegistersExpectedTools(t *testing.T) {
	registry := connector.NewRegistry()
	ws := &mockWatcherStore{}
	as := &mockAlertStore{}
	rs := &mockWatcherRunStore{}

	ls := &mockLogStore{}

	s := server.NewMCPServer("opentrace-test", "0.1.0")
	deps := Deps{
		Registry:        registry,
		WatcherStore:    ws,
		AlertStore:      as,
		WatcherRunStore: rs,
		LogStore:        ls,
	}
	addReadOnlyTools(s, deps)

	tools := s.ListTools()
	expectedTools := []string{"list_connectors", "list_monitors", "list_alerts", "get_digest", "db_locks", "log_stats", "db_index_analysis", "compare_periods"}
	for _, name := range expectedTools {
		if _, ok := tools[name]; !ok {
			t.Errorf("expected read-only tool %q to be registered", name)
		}
	}

	// Verify write tools are NOT registered by addReadOnlyTools.
	if _, ok := tools["create_monitor"]; ok {
		t.Error("create_monitor should not be registered by addReadOnlyTools")
	}
	if _, ok := tools["explain_query"]; ok {
		t.Error("explain_query should not be registered by addReadOnlyTools")
	}
}

func TestAddWriteTools_RegistersConnectorTools(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockDataSource{
		connType: connector.ConnectorDatabase,
		tools: []agent.Tool{
			{
				Name:        "run_query",
				Description: "Run a SQL query",
				Handler: func(ctx context.Context, args map[string]any) (string, error) {
					return "ok", nil
				},
			},
		},
	})

	ws := &mockWatcherStore{}
	re := watcher.NewRuleEvaluator(registry, nil, nil)

	s := server.NewMCPServer("opentrace-test", "0.1.0")
	deps := Deps{
		Registry:      registry,
		WatcherStore:  ws,
		RuleEvaluator: re,
	}
	addWriteTools(s, deps)

	tools := s.ListTools()

	// Connector tool should be registered.
	if _, ok := tools["run_query"]; !ok {
		t.Error("expected connector tool 'run_query' to be registered by addWriteTools")
	}

	// create_monitor should be registered when WatcherStore is provided.
	if _, ok := tools["create_monitor"]; !ok {
		t.Error("expected 'create_monitor' to be registered by addWriteTools")
	}

	// preview_monitor should be registered when RuleEvaluator is provided.
	if _, ok := tools["preview_monitor"]; !ok {
		t.Error("expected 'preview_monitor' to be registered by addWriteTools")
	}
}

// --- getDigestHandler tests ---

func TestGetDigestHandler_Empty(t *testing.T) {
	as := &mockAlertStore{}
	ws := &mockWatcherStore{}
	rs := &mockWatcherRunStore{}

	handler := getDigestHandler(as, ws, rs)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}

	text := resultText(t, result)

	// Parse the JSON response
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["status"] != "healthy" {
		t.Errorf("status = %v, want healthy", resp["status"])
	}

	// Should contain summary
	if _, ok := resp["summary"].(string); !ok {
		t.Error("expected summary string in response")
	}
}

func TestGetDigestHandler_WithAlerts(t *testing.T) {
	now := time.Now().UTC()
	wID := uuid.New()

	as := &mockAlertStore{
		alerts: []store.Alert{
			{ID: uuid.New(), WatcherID: &wID, WatcherTitle: "Connection Monitor", Title: "High connections",
				Summary: "92/100 connections", Severity: store.SeverityCritical, CreatedAt: now.Add(-2 * time.Hour)},
			{ID: uuid.New(), WatcherID: &wID, WatcherTitle: "Connection Monitor", Title: "Warning level",
				Summary: "75/100 connections", Severity: store.SeverityWarning, CreatedAt: now.Add(-4 * time.Hour)},
		},
	}
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: wID, Title: "Connection Monitor", Status: store.WatcherActive},
		},
	}
	rs := &mockWatcherRunStore{
		runs: []store.WatcherRun{
			{ID: uuid.New(), WatcherID: wID, StartedAt: now.Add(-1 * time.Hour), Status: "completed"},
		},
	}

	handler := getDigestHandler(as, ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"period": "last_24h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}

	text := resultText(t, result)

	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["status"] != "critical" {
		t.Errorf("status = %v, want critical", resp["status"])
	}

	alerts, ok := resp["alerts"].(map[string]any)
	if !ok {
		t.Fatal("expected alerts map in response")
	}
	if alerts["critical"] != float64(1) {
		t.Errorf("critical = %v, want 1", alerts["critical"])
	}
	if alerts["warning"] != float64(1) {
		t.Errorf("warning = %v, want 1", alerts["warning"])
	}
}

func TestGetDigestHandler_PeriodYesterday(t *testing.T) {
	as := &mockAlertStore{}
	ws := &mockWatcherStore{}
	rs := &mockWatcherRunStore{}

	handler := getDigestHandler(as, ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"period": "yesterday",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}

	text := resultText(t, result)

	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Verify period is set to yesterday
	period, ok := resp["period"].(map[string]any)
	if !ok {
		t.Fatal("expected period map")
	}

	startStr, _ := period["start"].(string)
	endStr, _ := period["end"].(string)

	start, _ := time.Parse(time.RFC3339, startStr)
	end, _ := time.Parse(time.RFC3339, endStr)

	if end.Sub(start) != 24*time.Hour {
		t.Errorf("yesterday period duration = %v, want 24h", end.Sub(start))
	}
}

func TestGetDigestHandler_EnvironmentFilter(t *testing.T) {
	now := time.Now().UTC()
	w1 := uuid.New()
	w2 := uuid.New()

	as := &mockAlertStore{
		alerts: []store.Alert{
			{ID: uuid.New(), WatcherID: &w1, Environment: "production", Severity: store.SeverityCritical, CreatedAt: now.Add(-1 * time.Hour)},
			{ID: uuid.New(), WatcherID: &w2, Environment: "staging", Severity: store.SeverityWarning, CreatedAt: now.Add(-2 * time.Hour)},
		},
	}
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: w1, Title: "Prod Monitor", Environment: "production", Status: store.WatcherActive},
			{ID: w2, Title: "Staging Monitor", Environment: "staging", Status: store.WatcherActive},
		},
	}
	rs := &mockWatcherRunStore{}

	handler := getDigestHandler(as, ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"environment": "production",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	alerts, ok := resp["alerts"].(map[string]any)
	if !ok {
		t.Fatal("expected alerts map")
	}
	// Only production alert should be counted
	if alerts["critical"] != float64(1) {
		t.Errorf("critical = %v, want 1", alerts["critical"])
	}
	if alerts["warning"] != float64(0) {
		t.Errorf("warning = %v, want 0 (staging filtered out)", alerts["warning"])
	}
}
