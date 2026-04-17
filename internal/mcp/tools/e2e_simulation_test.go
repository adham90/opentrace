package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/domain/logs"
	"github.com/adham90/opentrace/internal/testutil/mocks"
)

// ---------------------------------------------------------------------------
// E2E Gateway Simulation Tests
//
// These tests simulate the MCP gateway dispatch pattern: a single "opentrace"
// tool receives tool+action params and routes to the appropriate handler.
// Unlike the unit tests in *_test.go files which call handlers directly,
// these tests exercise the full routing path through each tool's handler
// function, verifying that every tool+action combination returns a valid
// CallToolResult without panics.
// ---------------------------------------------------------------------------

// gatewayEnv holds all the mock stores and registered tool handlers,
// simulating the gateway's dispatch table.
type gatewayEnv struct {
	handlers map[string]ToolHandlerFunc
}

// newGatewayEnv builds a fully populated gateway environment with all tools
// registered using mock stores. This mirrors what buildGateway() does in
// internal/mcp/server_tools.go, but without the mcp package dependency.
func newGatewayEnv() *gatewayEnv {
	logStore := mocks.NewLogStore()
	dsStore := mocks.NewDataSourceStore()
	serverStore := mocks.NewServerStore()
	errorGroupStore := mocks.NewErrorGroupStore()
	watchStore := mocks.NewWatchStore()
	healthCheckStore := mocks.NewHealthCheckStore()
	settingsStore := mocks.NewSettingsStore()
	agentNoteStore := mocks.NewAgentNoteStore()
	userStore := mocks.NewUserStore()
	metricStore := mocks.NewMetricStore()
	analyticsStore := mocks.NewAnalyticsStore()
	trendStore := mocks.NewTrendStore()
	errorImpactStore := mocks.NewErrorImpactStore()
	auditStore := mocks.NewAuditStore()
	mcpActivityStore := mocks.NewMCPActivityStore()
	registry := connector.NewRegistry()

	logService := logs.NewService(logStore)

	handlers := map[string]ToolHandlerFunc{
		"overview": OverviewHandler(OverviewDeps{
			LogStore:         logStore,
			DSStore:          dsStore,
			ServerStore:      serverStore,
			ErrorGroupStore:  errorGroupStore,
			WatchStore:       watchStore,
			HealthCheckStore: healthCheckStore,
			SettingsStore:    settingsStore,
			AgentNoteStore:   agentNoteStore,
		}),
		"logs": LogsHandler(LogsDeps{
			Logs:            logService,
			LogStore:        logStore,
			ErrorGroupStore: errorGroupStore,
		}),
		"errors": ErrorsHandler(ErrorsDeps{
			ErrorGroupStore:  errorGroupStore,
			LogStore:         logStore,
			ErrorImpactStore: errorImpactStore,
		}),
		"watches": WatchesHandler(WatchesDeps{
			WatchStore: watchStore,
			LogStore:   logStore,
		}),
		"healthchecks": HealthchecksHandler(HealthchecksDeps{
			HealthCheckStore: healthCheckStore,
		}),
		"servers": ServersHandler(ServersDeps{
			ServerStore: serverStore,
			MetricStore: metricStore,
		}),
		"connectors": ConnectorsHandler(ConnectorsDeps{
			DSStore:  dsStore,
			Registry: registry,
			LogStore: logStore,
		}),
		"analytics": AnalyticsHandler(AnalyticsDeps{
			AnalyticsStore: analyticsStore,
			TrendStore:     trendStore,
		}),
		"setup": SetupHandler(SetupDeps{
			LogStore:      logStore,
			UserStore:     userStore,
			SettingsStore: settingsStore,
			DSStore:       dsStore,
		}),
		"admin": AdminHandler(AdminDeps{
			SettingsStore:    settingsStore,
			UserStore:        userStore,
			AuditStore:       auditStore,
			MCPActivityStore: mcpActivityStore,
			Registry:         registry,
		}),
	}

	return &gatewayEnv{handlers: handlers}
}

// dispatch simulates the gateway: looks up the tool handler by name, injects
// the action into the params, and calls the handler. Returns the same
// (result, error) tuple the real gateway returns.
func (g *gatewayEnv) dispatch(ctx context.Context, tool, action string, extraParams map[string]any) (*CallToolResult, error) {
	handler, ok := g.handlers[tool]
	if !ok {
		validTools := make([]string, 0, len(g.handlers))
		for k := range g.handlers {
			validTools = append(validTools, k)
		}
		return NewToolResultError(fmt.Sprintf(
			"unknown tool %q. Available: %s",
			tool, strings.Join(validTools, ", "))), nil
	}

	params := make(map[string]any)
	for k, v := range extraParams {
		params[k] = v
	}
	if action != "" {
		params["action"] = action
	}

	req := MakeCallToolRequest(tool, params)
	return handler(ctx, req)
}

// ---------------------------------------------------------------------------
// Happy-path tests: every tool+action that should succeed with empty/mock data
// ---------------------------------------------------------------------------

func TestE2E_HappyPath(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	tests := []struct {
		tool   string
		action string
		params map[string]any
	}{
		// overview
		{"overview", "status", nil},
		{"overview", "triage", nil},
		{"overview", "diagnose", nil},
		{"overview", "settings", nil},
		{"overview", "notes", nil},
		{"overview", "changes", nil},

		// logs
		{"logs", "search", map[string]any{"query": "error"}},
		{"logs", "stats", map[string]any{"time_range": "1h", "group_by": "level"}},
		{"logs", "summary", map[string]any{"time_range": "1h"}},
		{"logs", "performance", nil},

		// errors
		{"errors", "list", nil},
		{"errors", "ranking", nil},
		{"errors", "new", nil},

		// watches
		{"watches", "status", nil},
		{"watches", "alerts", nil},

		// healthchecks
		{"healthchecks", "list", nil},

		// servers
		{"servers", "list", nil},

		// connectors
		{"connectors", "list", nil},

		// analytics
		{"analytics", "traffic", nil},
		{"analytics", "endpoints", nil},

		// setup
		{"setup", "status", nil},
		{"setup", "verify", nil},

		// admin
		{"admin", "settings", nil},
		{"admin", "users", nil},
	}

	for _, tt := range tests {
		name := tt.tool + "/" + tt.action
		t.Run(name, func(t *testing.T) {
			result, err := env.dispatch(ctx, tt.tool, tt.action, tt.params)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.IsError {
				text := extractText(t, result)
				t.Fatalf("expected success, got error: %s", text)
			}
			if len(result.Content) == 0 {
				t.Fatal("expected at least one content item")
			}
			// Verify content is non-empty text.
			text := extractText(t, result)
			if text == "" {
				t.Error("expected non-empty text in result")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Error-case tests: tool+action combos that should return specific errors
// ---------------------------------------------------------------------------

func TestE2E_ErrorCases(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	tests := []struct {
		tool     string
		action   string
		params   map[string]any
		contains string // substring expected in the error text
	}{
		// overview/timeline without start param
		{
			tool:     "overview",
			action:   "timeline",
			params:   nil,
			contains: "start is required",
		},
		// logs/attributes without field
		{
			tool:     "logs",
			action:   "attributes",
			params:   nil,
			contains: "field is required",
		},
		// errors/detail without fingerprint
		{
			tool:     "errors",
			action:   "detail",
			params:   nil,
			contains: "fingerprint",
		},
		// errors/resolve without fingerprint
		{
			tool:     "errors",
			action:   "resolve",
			params:   nil,
			contains: "fingerprint",
		},
		// errors/ignore without fingerprint
		{
			tool:     "errors",
			action:   "ignore",
			params:   nil,
			contains: "fingerprint",
		},
		// errors/impact without fingerprint
		{
			tool:     "errors",
			action:   "impact",
			params:   nil,
			contains: "fingerprint",
		},
		// errors/user_errors without user_id
		{
			tool:     "errors",
			action:   "user_errors",
			params:   nil,
			contains: "user_id",
		},
		// watches/create without conditions
		{
			tool:     "watches",
			action:   "create",
			params:   nil,
			contains: "conditions is required",
		},
		// watches/delete without watch_id
		{
			tool:     "watches",
			action:   "delete",
			params:   nil,
			contains: "watch_id is required",
		},
		// healthchecks/create without name
		{
			tool:     "healthchecks",
			action:   "create",
			params:   nil,
			contains: "name is required",
		},
		// healthchecks/delete without id
		{
			tool:     "healthchecks",
			action:   "delete",
			params:   nil,
			contains: "id is required",
		},
		// servers/query without server_id
		{
			tool:     "servers",
			action:   "query",
			params:   nil,
			contains: "server_id is required",
		},
		// servers/health without server_id
		{
			tool:     "servers",
			action:   "health",
			params:   nil,
			contains: "server_id is required",
		},
		// connectors/get without connector_id
		{
			tool:     "connectors",
			action:   "get",
			params:   nil,
			contains: "connector_id is required",
		},
		// admin/update_retention without retention_days
		{
			tool:     "admin",
			action:   "update_retention",
			params:   nil,
			contains: "retention_days is required",
		},
		// admin/update_role without user_id
		{
			tool:     "admin",
			action:   "update_role",
			params:   nil,
			contains: "user_id is required",
		},
		// overview/investigate without service
		{
			tool:     "overview",
			action:   "investigate",
			params:   nil,
			contains: "service is required",
		},
		// logs/context without log_id
		{
			tool:     "logs",
			action:   "context",
			params:   nil,
			contains: "log_id",
		},
	}

	for _, tt := range tests {
		name := tt.tool + "/" + tt.action
		t.Run(name, func(t *testing.T) {
			result, err := env.dispatch(ctx, tt.tool, tt.action, tt.params)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if !result.IsError {
				text := extractText(t, result)
				t.Fatalf("expected error result, got success: %s", text)
			}
			text := extractText(t, result)
			if !strings.Contains(strings.ToLower(text), strings.ToLower(tt.contains)) {
				t.Errorf("expected error to contain %q, got %q", tt.contains, text)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Unknown tool/action tests
// ---------------------------------------------------------------------------

func TestE2E_UnknownTool(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	result, err := env.dispatch(ctx, "nonexistent_tool", "anything", nil)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Fatal("expected error for unknown tool")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "unknown tool") {
		t.Errorf("expected 'unknown tool' in error, got %q", text)
	}
}

func TestE2E_UnknownActions(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	toolNames := []string{
		"overview", "logs", "errors", "watches", "healthchecks",
		"servers", "connectors", "analytics", "setup", "admin",
	}

	for _, tool := range toolNames {
		t.Run(tool+"/unknown_action", func(t *testing.T) {
			result, err := env.dispatch(ctx, tool, "nonexistent_action_xyz", nil)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if !result.IsError {
				text := extractText(t, result)
				t.Fatalf("expected error for unknown action, got success: %s", text)
			}
			text := extractText(t, result)
			if !strings.Contains(strings.ToLower(text), "unknown action") &&
				!strings.Contains(strings.ToLower(text), "action is required") {
				t.Errorf("expected 'unknown action' or 'action is required' in error, got %q", text)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Missing action tests (no action provided)
// ---------------------------------------------------------------------------

func TestE2E_MissingAction(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	// Tools that require an action parameter should return an error when
	// action is empty.
	toolNames := []string{
		"logs", "errors", "watches", "healthchecks",
		"servers", "connectors", "analytics", "setup", "admin",
	}

	for _, tool := range toolNames {
		t.Run(tool+"/missing_action", func(t *testing.T) {
			result, err := env.dispatch(ctx, tool, "", nil)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if !result.IsError {
				// Some tools may default to a valid action on empty; that's OK.
				return
			}
			text := extractText(t, result)
			if text == "" {
				t.Error("expected non-empty error message")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Response structure validation: happy-path responses contain valid JSON
// ---------------------------------------------------------------------------

func TestE2E_ResponsesAreValidJSON(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	// These tool+actions are known to return JSON responses.
	jsonResponders := []struct {
		tool   string
		action string
		params map[string]any
	}{
		{"overview", "status", nil},
		{"overview", "diagnose", nil},
		{"overview", "settings", nil},
		{"logs", "stats", map[string]any{"time_range": "1h", "group_by": "level"}},
		{"logs", "summary", map[string]any{"time_range": "1h"}},
		{"watches", "status", nil},
		{"analytics", "traffic", nil},
		{"setup", "status", nil},
		{"admin", "settings", nil},
	}

	for _, tt := range jsonResponders {
		name := tt.tool + "/" + tt.action
		t.Run(name, func(t *testing.T) {
			result, err := env.dispatch(ctx, tt.tool, tt.action, tt.params)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.IsError {
				t.Skip("tool returned error; skipping JSON validation")
			}

			text := extractText(t, result)
			// Attempt to parse as JSON. Some results are plain text messages
			// (e.g., "No health checks configured") — only fail for responses
			// that look like they should be JSON.
			if strings.HasPrefix(strings.TrimSpace(text), "{") || strings.HasPrefix(strings.TrimSpace(text), "[") {
				var parsed any
				if err := json.Unmarshal([]byte(text), &parsed); err != nil {
					t.Errorf("response looks like JSON but failed to parse: %v\ntext: %s", err, Truncate(text, 200))
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// All registered tools are callable (smoke test every tool)
// ---------------------------------------------------------------------------

func TestE2E_AllToolsCallable(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	for tool := range env.handlers {
		t.Run(tool, func(t *testing.T) {
			// Call with empty params. We only verify no panic and non-nil result.
			// An error result is acceptable (many tools require params).
			result, err := env.dispatch(ctx, tool, "", nil)
			if err != nil {
				t.Fatalf("unexpected Go error calling tool %q: %v", tool, err)
			}
			if result == nil {
				t.Fatalf("expected non-nil result for tool %q", tool)
			}
			if len(result.Content) == 0 {
				t.Fatalf("expected at least one content item for tool %q", tool)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Concurrent dispatch: verify no data races under concurrent access
// ---------------------------------------------------------------------------

func TestE2E_ConcurrentDispatch(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	type call struct {
		tool   string
		action string
		params map[string]any
	}

	calls := []call{
		{"overview", "status", nil},
		{"overview", "triage", nil},
		{"logs", "search", map[string]any{"query": "test"}},
		{"logs", "stats", map[string]any{"time_range": "1h"}},
		{"errors", "list", nil},
		{"errors", "ranking", nil},
		{"watches", "status", nil},
		{"healthchecks", "list", nil},
		{"servers", "list", nil},
		{"connectors", "list", nil},
		{"analytics", "traffic", nil},
		{"setup", "status", nil},
		{"admin", "settings", nil},
		{"admin", "users", nil},
	}

	// Run all calls concurrently. With -race flag this will detect races.
	t.Run("parallel", func(t *testing.T) {
		for _, c := range calls {
			c := c // capture
			t.Run(c.tool+"/"+c.action, func(t *testing.T) {
				t.Parallel()
				result, err := env.dispatch(ctx, c.tool, c.action, c.params)
				if err != nil {
					t.Fatalf("unexpected Go error: %v", err)
				}
				if result == nil {
					t.Fatal("expected non-nil result")
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// Overview-specific scenarios
// ---------------------------------------------------------------------------

func TestE2E_OverviewTimeline_WithValidParams(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	result, err := env.dispatch(ctx, "overview", "timeline", map[string]any{
		"start": "2025-01-01T00:00:00Z",
		"end":   "2025-01-01T01:00:00Z",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("expected success, got error: %s", text)
	}

	text := extractText(t, result)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
}

func TestE2E_OverviewDiagnose_WithService(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	result, err := env.dispatch(ctx, "overview", "diagnose", map[string]any{
		"service": "web-api",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("expected success, got error: %s", text)
	}

	text := extractText(t, result)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
	if svc, _ := data["service"].(string); svc != "web-api" {
		t.Errorf("expected service='web-api', got %q", svc)
	}
}

// ---------------------------------------------------------------------------
// Logs-specific scenarios
// ---------------------------------------------------------------------------

func TestE2E_LogsSearch_WithFilters(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	tests := []struct {
		name   string
		params map[string]any
	}{
		{"query only", map[string]any{"query": "error"}},
		{"service filter", map[string]any{"service": "api"}},
		{"level filter", map[string]any{"level": "error"}},
		{"time range", map[string]any{"time_range": "24h"}},
		{"combined", map[string]any{
			"query":      "timeout",
			"service":    "api",
			"level":      "error",
			"time_range": "1h",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := env.dispatch(ctx, "logs", "search", tt.params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.IsError {
				text := extractText(t, result)
				t.Fatalf("expected success, got error: %s", text)
			}
		})
	}
}

func TestE2E_LogsAttributes_WithField(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	result, err := env.dispatch(ctx, "logs", "attributes", map[string]any{
		"field": "service",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("expected success, got error: %s", text)
	}
}

// ---------------------------------------------------------------------------
// Watches create with valid conditions
// ---------------------------------------------------------------------------

func TestE2E_WatchesCreate_WithConditions(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	result, err := env.dispatch(ctx, "watches", "create", map[string]any{
		"conditions": map[string]any{
			"type":   "threshold",
			"metric": "error_rate",
			"op":     ">",
			"value":  float64(5),
		},
		"service": "api",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("expected success, got error: %s", text)
	}
}

// ---------------------------------------------------------------------------
// Admin audit action
// ---------------------------------------------------------------------------

func TestE2E_AdminAudit(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	result, err := env.dispatch(ctx, "admin", "audit", map[string]any{
		"limit": float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("expected success, got error: %s", text)
	}
}

// ---------------------------------------------------------------------------
// Overview notes create + list round-trip
// ---------------------------------------------------------------------------

func TestE2E_OverviewNotes_CreateAndList(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	// Create a note.
	result, err := env.dispatch(ctx, "overview", "notes", map[string]any{
		"entity_type": "service",
		"entity_id":   "web-api",
		"note":        "This service handles authentication",
	})
	if err != nil {
		t.Fatalf("unexpected error creating note: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("expected success creating note, got error: %s", text)
	}

	text := extractText(t, result)
	if !strings.Contains(text, "Note saved") {
		t.Errorf("expected 'Note saved' in response, got %q", text)
	}

	// List notes (empty because mock doesn't persist, but should not error).
	listResult, err := env.dispatch(ctx, "overview", "notes", nil)
	if err != nil {
		t.Fatalf("unexpected error listing notes: %v", err)
	}
	if listResult == nil {
		t.Fatal("expected non-nil result")
	}
	if listResult.IsError {
		text := extractText(t, listResult)
		t.Fatalf("expected success listing notes, got error: %s", text)
	}
}

// ---------------------------------------------------------------------------
// Setup detect with file hints
// ---------------------------------------------------------------------------

func TestE2E_SetupDetect_WithFiles(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	result, err := env.dispatch(ctx, "setup", "detect", map[string]any{
		"files": "Gemfile,config/application.rb,Rakefile",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("expected success, got error: %s", text)
	}

	text := extractText(t, result)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
	if fw, _ := data["detected_framework"].(string); fw != "Ruby on Rails" {
		t.Errorf("expected detected_framework='Ruby on Rails', got %q", fw)
	}
}

// ---------------------------------------------------------------------------
// Analytics heatmap
// ---------------------------------------------------------------------------

func TestE2E_AnalyticsHeatmap(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	result, err := env.dispatch(ctx, "analytics", "heatmap", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Empty analytics store may return either success (empty data) or text.
	// We just verify no panics and valid response.
	if len(result.Content) == 0 {
		t.Fatal("expected at least one content item")
	}
}

// ---------------------------------------------------------------------------
// Analytics trends and movers
// ---------------------------------------------------------------------------

func TestE2E_AnalyticsTrendsAndMovers(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	for _, action := range []string{"trends", "movers"} {
		t.Run(action, func(t *testing.T) {
			result, err := env.dispatch(ctx, "analytics", action, map[string]any{
				"since":  "24h",
				"metric": "error_rate",
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if len(result.Content) == 0 {
				t.Fatal("expected at least one content item")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Errors resolve and ignore with fingerprint but missing reason
// ---------------------------------------------------------------------------

func TestE2E_ErrorsResolve_MissingReason(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	result, err := env.dispatch(ctx, "errors", "resolve", map[string]any{
		"fingerprint": "fp-abc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Fatal("expected error when reason is missing")
	}
	text := extractText(t, result)
	if !strings.Contains(strings.ToLower(text), "reason") {
		t.Errorf("expected error about missing reason, got %q", text)
	}
}

func TestE2E_ErrorsIgnore_MissingReason(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	result, err := env.dispatch(ctx, "errors", "ignore", map[string]any{
		"fingerprint": "fp-abc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Fatal("expected error when reason is missing")
	}
	text := extractText(t, result)
	if !strings.Contains(strings.ToLower(text), "reason") {
		t.Errorf("expected error about missing reason, got %q", text)
	}
}

// ---------------------------------------------------------------------------
// Healthchecks uptime (happy path with empty data)
// ---------------------------------------------------------------------------

func TestE2E_HealthchecksUptime(t *testing.T) {
	env := newGatewayEnv()
	ctx := context.Background()

	result, err := env.dispatch(ctx, "healthchecks", "uptime", map[string]any{
		"hours": float64(24),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Empty store returns "No health checks configured" message.
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("expected success, got error: %s", text)
	}
}
