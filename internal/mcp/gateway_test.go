package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- truncateForBrief ---

func TestTruncateForBrief_NoArrays(t *testing.T) {
	data := map[string]any{
		"status": "ok",
		"count":  42.0,
	}
	modified := truncateForBrief(data)
	if modified {
		t.Error("expected modified=false for data with no arrays")
	}
	if data["status"] != "ok" {
		t.Error("non-array values should be untouched")
	}
}

func TestTruncateForBrief_ShortArray(t *testing.T) {
	data := map[string]any{
		"items": []any{"a", "b", "c"},
	}
	modified := truncateForBrief(data)
	if modified {
		t.Error("arrays with <= 3 items should not be truncated")
	}
	arr := data["items"].([]any)
	if len(arr) != 3 {
		t.Errorf("array length = %d, want 3", len(arr))
	}
	if _, ok := data["items_total"]; ok {
		t.Error("should not add _total key for short arrays")
	}
}

func TestTruncateForBrief_ExactlyThreeItems(t *testing.T) {
	data := map[string]any{
		"logs": []any{1, 2, 3},
	}
	modified := truncateForBrief(data)
	if modified {
		t.Error("arrays with exactly 3 items should not be truncated")
	}
}

func TestTruncateForBrief_LongArray(t *testing.T) {
	data := map[string]any{
		"logs": []any{"a", "b", "c", "d", "e"},
	}
	modified := truncateForBrief(data)
	if !modified {
		t.Error("expected modified=true for arrays with > 3 items")
	}
	arr := data["logs"].([]any)
	if len(arr) != 3 {
		t.Errorf("truncated array length = %d, want 3", len(arr))
	}
	if data["logs_total"] != 5 {
		t.Errorf("logs_total = %v, want 5", data["logs_total"])
	}
	if data["logs_truncated"] != true {
		t.Errorf("logs_truncated = %v, want true", data["logs_truncated"])
	}
}

func TestTruncateForBrief_MultipleArrays(t *testing.T) {
	data := map[string]any{
		"errors":    []any{1, 2, 3, 4, 5, 6},
		"warnings":  []any{"w1", "w2"},
		"traces":    []any{"t1", "t2", "t3", "t4"},
		"scalar":    "hello",
	}
	modified := truncateForBrief(data)
	if !modified {
		t.Error("expected modified=true")
	}

	// "errors" should be truncated
	errArr := data["errors"].([]any)
	if len(errArr) != 3 {
		t.Errorf("errors length = %d, want 3", len(errArr))
	}
	if data["errors_total"] != 6 {
		t.Errorf("errors_total = %v, want 6", data["errors_total"])
	}

	// "warnings" should NOT be truncated (only 2 items)
	warnArr := data["warnings"].([]any)
	if len(warnArr) != 2 {
		t.Errorf("warnings length = %d, want 2", len(warnArr))
	}
	if _, ok := data["warnings_total"]; ok {
		t.Error("should not add warnings_total for short array")
	}

	// "traces" should be truncated (4 > 3)
	traceArr := data["traces"].([]any)
	if len(traceArr) != 3 {
		t.Errorf("traces length = %d, want 3", len(traceArr))
	}
	if data["traces_total"] != 4 {
		t.Errorf("traces_total = %v, want 4", data["traces_total"])
	}

	// scalar should be untouched
	if data["scalar"] != "hello" {
		t.Error("scalar values should not be modified")
	}
}

func TestTruncateForBrief_EmptyMap(t *testing.T) {
	data := map[string]any{}
	modified := truncateForBrief(data)
	if modified {
		t.Error("expected modified=false for empty map")
	}
}

func TestTruncateForBrief_EmptyArray(t *testing.T) {
	data := map[string]any{
		"items": []any{},
	}
	modified := truncateForBrief(data)
	if modified {
		t.Error("expected modified=false for empty array")
	}
}

// --- truncate (helper) ---

func TestTruncate_Short(t *testing.T) {
	result := truncate("hello", 10)
	if result != "hello" {
		t.Errorf("truncate(%q, 10) = %q, want %q", "hello", result, "hello")
	}
}

func TestTruncate_ExactLength(t *testing.T) {
	result := truncate("hello", 5)
	if result != "hello" {
		t.Errorf("truncate(%q, 5) = %q, want %q", "hello", result, "hello")
	}
}

func TestTruncate_Long(t *testing.T) {
	result := truncate("hello world", 5)
	if result != "hello..." {
		t.Errorf("truncate(%q, 5) = %q, want %q", "hello world", result, "hello...")
	}
}

func TestTruncate_Empty(t *testing.T) {
	result := truncate("", 5)
	if result != "" {
		t.Errorf("truncate(%q, 5) = %q, want %q", "", result, "")
	}
}

// --- suggest / withSuggestions helpers ---

func TestSuggest(t *testing.T) {
	s := suggest("logs", "check recent errors", map[string]any{"level": "ERROR"})
	if s.Tool != "logs" {
		t.Errorf("Tool = %q, want %q", s.Tool, "logs")
	}
	if s.Why != "check recent errors" {
		t.Errorf("Why = %q, want %q", s.Why, "check recent errors")
	}
	if s.Source != "static" {
		t.Errorf("Source = %q, want %q", s.Source, "static")
	}
	if s.Args["level"] != "ERROR" {
		t.Errorf("Args[level] = %v, want ERROR", s.Args["level"])
	}
}

func TestWithSuggestions_NonEmpty(t *testing.T) {
	resp := map[string]any{"data": "test"}
	result := withSuggestions(resp, suggest("logs", "why", nil))
	if _, ok := result["suggested_tools"]; !ok {
		t.Error("expected suggested_tools key in response")
	}
}

func TestWithSuggestions_Empty(t *testing.T) {
	resp := map[string]any{"data": "test"}
	result := withSuggestions(resp)
	if _, ok := result["suggested_tools"]; ok {
		t.Error("should not add suggested_tools when no suggestions provided")
	}
}

// --- Gateway ---

func TestNewGateway(t *testing.T) {
	gw := NewGateway()
	if gw == nil {
		t.Fatal("NewGateway() returned nil")
	}
	if gw.handlers == nil {
		t.Error("handlers map should be initialized")
	}
	if len(gw.entries) != 0 {
		t.Errorf("entries should be empty, got %d", len(gw.entries))
	}
}

func TestGateway_Register(t *testing.T) {
	gw := NewGateway()
	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return NewToolResultText("ok"), nil
	}
	gw.Register("test_tool", handler, GatewayEntry{
		Description: "A test tool",
		Category:    "Testing",
		Access:      "read",
		ReadOnly:    true,
	})

	if len(gw.entries) != 1 {
		t.Fatalf("entries count = %d, want 1", len(gw.entries))
	}
	if gw.entries[0].Name != "test_tool" {
		t.Errorf("entry name = %q, want %q", gw.entries[0].Name, "test_tool")
	}
	if _, ok := gw.handlers["test_tool"]; !ok {
		t.Error("handler should be registered")
	}
}

func TestGateway_Tool(t *testing.T) {
	gw := NewGateway()
	tool := gw.Tool()
	if tool.Name != "opentrace" {
		t.Errorf("tool name = %q, want %q", tool.Name, "opentrace")
	}
	if tool.Description == "" {
		t.Error("tool description should not be empty")
	}
	if tool.Annotations == nil {
		t.Fatal("tool annotations should not be nil")
	}
}

func TestGateway_Handler_Discover(t *testing.T) {
	gw := NewGateway()
	gw.Register("logs", nil, GatewayEntry{
		Description: "Log tools",
		Category:    "Log Intelligence",
		Access:      "read",
	})

	handler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool": "discover",
	})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected successful result")
	}
	// Verify JSON contains expected fields.
	tc := result.Content[0].(*mcp.TextContent)
	var data map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if data["total_tools"] != 1.0 {
		t.Errorf("total_tools = %v, want 1", data["total_tools"])
	}
}

func TestGateway_Handler_DiscoverEmpty(t *testing.T) {
	gw := NewGateway()

	handler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool": "",
	})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected successful result for empty tool (discover)")
	}
}

func TestGateway_Handler_Describe(t *testing.T) {
	gw := NewGateway()
	gw.Register("logs", nil, GatewayEntry{
		Description: "Log tools",
		Category:    "Log Intelligence",
		Access:      "read",
		ReadOnly:    true,
		Actions:     []string{"search", "stats"},
		Params:      map[string]string{"query": "search text"},
	})

	handler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool":   "describe",
		"action": "logs",
	})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected successful result")
	}
	tc := result.Content[0].(*mcp.TextContent)
	var data map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if data["name"] != "logs" {
		t.Errorf("name = %v, want logs", data["name"])
	}
	if data["read_only"] != true {
		t.Errorf("read_only = %v, want true", data["read_only"])
	}
}

func TestGateway_Handler_DescribeNoAction(t *testing.T) {
	gw := NewGateway()
	handler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool": "describe",
	})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when action is missing for describe")
	}
}

func TestGateway_Handler_DescribeUnknownTool(t *testing.T) {
	gw := NewGateway()
	handler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool":   "describe",
		"action": "nonexistent",
	})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for unknown tool describe")
	}
}

func TestGateway_Handler_UnknownTool(t *testing.T) {
	gw := NewGateway()
	gw.Register("logs", nil, GatewayEntry{
		Description: "Log tools",
		Category:    "Log Intelligence",
	})

	handler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool": "nonexistent",
	})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for unknown tool")
	}
	tc := result.Content[0].(*mcp.TextContent)
	if tc.Text == "" {
		t.Error("error message should not be empty")
	}
}

func TestGateway_Handler_DispatchesToRegisteredTool(t *testing.T) {
	gw := NewGateway()
	called := false
	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		args := GetArguments(req)
		action, _ := args["action"].(string)
		if action != "search" {
			t.Errorf("action = %q, want %q", action, "search")
		}
		return NewToolResultText(`{"status":"ok"}`), nil
	}
	gw.Register("logs", handler, GatewayEntry{
		Description: "Log tools",
		Category:    "Log Intelligence",
	})

	gwHandler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool":   "logs",
		"action": "search",
		"params": map[string]any{"query": "error"},
	})
	result, err := gwHandler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("inner handler was not called")
	}
	if result.IsError {
		t.Error("expected successful result")
	}
}

func TestGateway_Handler_QuietMode(t *testing.T) {
	gw := NewGateway()
	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resp := map[string]any{
			"data":                   "test",
			"suggested_tools":        []any{"logs"},
			"investigation_context":  "some context",
		}
		data, _ := json.Marshal(resp)
		return NewToolResultText(string(data)), nil
	}
	gw.Register("logs", handler, GatewayEntry{})

	gwHandler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool":  "logs",
		"quiet": true,
	})
	result, err := gwHandler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tc := result.Content[0].(*mcp.TextContent)
	var data map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if _, ok := data["suggested_tools"]; ok {
		t.Error("quiet mode should remove suggested_tools")
	}
	if _, ok := data["investigation_context"]; ok {
		t.Error("quiet mode should remove investigation_context")
	}
	if data["data"] != "test" {
		t.Error("quiet mode should preserve other fields")
	}
}

func TestGateway_Handler_BriefMode(t *testing.T) {
	gw := NewGateway()
	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resp := map[string]any{
			"items": []any{1, 2, 3, 4, 5},
		}
		data, _ := json.Marshal(resp)
		return NewToolResultText(string(data)), nil
	}
	gw.Register("logs", handler, GatewayEntry{})

	gwHandler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool":   "logs",
		"detail": "brief",
	})
	result, err := gwHandler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tc := result.Content[0].(*mcp.TextContent)
	var data map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	items := data["items"].([]any)
	if len(items) != 3 {
		t.Errorf("brief mode should truncate array to 3, got %d", len(items))
	}
	if data["items_total"] != 5.0 {
		t.Errorf("items_total = %v, want 5", data["items_total"])
	}
}

func TestGateway_PostProcess_NilResult(t *testing.T) {
	gw := NewGateway()
	result := gw.postProcess(nil, true, "brief")
	if result != nil {
		t.Error("postProcess(nil) should return nil")
	}
}

func TestGateway_PostProcess_ErrorResult(t *testing.T) {
	gw := NewGateway()
	errResult := NewToolResultError("something failed")
	result := gw.postProcess(errResult, true, "brief")
	if !result.IsError {
		t.Error("postProcess should pass through error results unchanged")
	}
}

func TestGateway_PostProcess_NonJSONContent(t *testing.T) {
	gw := NewGateway()
	result := NewToolResultText("not json")
	processed := gw.postProcess(result, true, "brief")
	tc := processed.Content[0].(*mcp.TextContent)
	if tc.Text != "not json" {
		t.Error("non-JSON content should pass through unchanged")
	}
}

func TestGateway_DiscoverCategoryFilter(t *testing.T) {
	gw := NewGateway()
	gw.Register("logs", nil, GatewayEntry{
		Description: "Log tools",
		Category:    "Log Intelligence",
	})
	gw.Register("db", nil, GatewayEntry{
		Description: "DB tools",
		Category:    "Database Introspection",
	})

	handler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool":   "discover",
		"action": "Log Intelligence",
	})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result.Content[0].(*mcp.TextContent)
	var data map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if data["total_tools"] != 1.0 {
		t.Errorf("filtered discover should return 1 tool, got %v", data["total_tools"])
	}
}

func TestGateway_SetServer(t *testing.T) {
	gw := NewGateway()
	if gw.mcpServer != nil {
		t.Error("mcpServer should be nil initially")
	}
	// SetServer with nil should not panic.
	gw.SetServer(nil)
	if gw.mcpServer != nil {
		t.Error("mcpServer should still be nil after SetServer(nil)")
	}
}

func TestGateway_Elicit_NoServer(t *testing.T) {
	gw := NewGateway()
	result, err := gw.Elicit(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("Elicit with no server should return nil")
	}
}

func TestGateway_Handler_ParamsNilFallback(t *testing.T) {
	gw := NewGateway()
	called := false
	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		args := GetArguments(req)
		// When no params provided, action should still be passed.
		action, _ := args["action"].(string)
		if action != "list" {
			t.Errorf("action = %q, want %q", action, "list")
		}
		return NewToolResultText(`{"ok":true}`), nil
	}
	gw.Register("logs", handler, GatewayEntry{})

	gwHandler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool":   "logs",
		"action": "list",
		// No "params" key at all.
	})
	_, err := gwHandler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("handler should have been called")
	}
}

// --- Additional gateway tests ---

func TestGateway_Handler_InnerHandlerError(t *testing.T) {
	gw := NewGateway()
	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, context.DeadlineExceeded
	}
	gw.Register("failing", handler, GatewayEntry{
		Description: "A failing tool",
		Category:    "Testing",
	})

	gwHandler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool": "failing",
	})
	_, err := gwHandler(context.Background(), req)
	if err == nil {
		t.Error("expected error from inner handler")
	}
}

func TestGateway_Handler_InnerHandlerReturnsErrorResult(t *testing.T) {
	gw := NewGateway()
	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return NewToolResultError("something went wrong"), nil
	}
	gw.Register("err_tool", handler, GatewayEntry{})

	gwHandler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool": "err_tool",
	})
	result, err := gwHandler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError result from inner handler")
	}
}

func TestGateway_PostProcess_QuietOnly(t *testing.T) {
	gw := NewGateway()
	resp := map[string]any{
		"data":                  "value",
		"suggested_tools":       []any{"a"},
		"investigation_context": "ctx",
		"items":                 []any{1, 2, 3, 4, 5},
	}
	data, _ := json.Marshal(resp)
	result := NewToolResultText(string(data))

	processed := gw.postProcess(result, true, "")
	tc := processed.Content[0].(*mcp.TextContent)
	var out map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("JSON parse error: %v", err)
	}
	if _, ok := out["suggested_tools"]; ok {
		t.Error("quiet should remove suggested_tools")
	}
	if _, ok := out["investigation_context"]; ok {
		t.Error("quiet should remove investigation_context")
	}
	// Arrays should NOT be truncated in quiet-only mode (no brief).
	items := out["items"].([]any)
	if len(items) != 5 {
		t.Errorf("quiet-only should not truncate arrays; got %d items", len(items))
	}
}

func TestGateway_PostProcess_BriefOnly(t *testing.T) {
	gw := NewGateway()
	resp := map[string]any{
		"data":            "value",
		"suggested_tools": []any{"a", "b"},
		"items":           []any{1, 2, 3, 4, 5},
	}
	data, _ := json.Marshal(resp)
	result := NewToolResultText(string(data))

	processed := gw.postProcess(result, false, "brief")
	tc := processed.Content[0].(*mcp.TextContent)
	var out map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("JSON parse error: %v", err)
	}
	// Brief does truncate arrays.
	items := out["items"].([]any)
	if len(items) != 3 {
		t.Errorf("brief should truncate arrays to 3; got %d", len(items))
	}
	// suggested_tools should still be present (only 2 items, not truncated).
	if _, ok := out["suggested_tools"]; !ok {
		t.Error("brief-only should not remove suggested_tools")
	}
}

func TestGateway_Handler_QuietFromParams(t *testing.T) {
	gw := NewGateway()
	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resp := map[string]any{
			"data":            "test",
			"suggested_tools": []any{"logs"},
		}
		data, _ := json.Marshal(resp)
		return NewToolResultText(string(data)), nil
	}
	gw.Register("logs", handler, GatewayEntry{})

	gwHandler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool":   "logs",
		"params": map[string]any{"quiet": true},
	})
	result, err := gwHandler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result.Content[0].(*mcp.TextContent)
	var data map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}
	if _, ok := data["suggested_tools"]; ok {
		t.Error("quiet from params should remove suggested_tools")
	}
}

func TestGateway_Handler_DetailFromParams(t *testing.T) {
	gw := NewGateway()
	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resp := map[string]any{
			"items": []any{1, 2, 3, 4, 5},
		}
		data, _ := json.Marshal(resp)
		return NewToolResultText(string(data)), nil
	}
	gw.Register("logs", handler, GatewayEntry{})

	gwHandler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool":   "logs",
		"params": map[string]any{"detail": "brief"},
	})
	result, err := gwHandler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result.Content[0].(*mcp.TextContent)
	var data map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}
	items := data["items"].([]any)
	if len(items) != 3 {
		t.Errorf("brief from params should truncate; got %d items", len(items))
	}
}

func TestGateway_Elicit_WithServerNoSessions(t *testing.T) {
	gw := NewGateway()
	s := mcp.NewServer(
		&mcp.Implementation{Name: "test", Version: "0.0.1"},
		nil,
	)
	gw.SetServer(s)

	// No sessions connected, so Elicit should return nil.
	result, err := gw.Elicit(context.Background(), "confirm?", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("Elicit with no sessions should return nil")
	}
}

func TestGateway_SetServer_NonNil(t *testing.T) {
	gw := NewGateway()
	s := mcp.NewServer(
		&mcp.Implementation{Name: "test", Version: "0.0.1"},
		nil,
	)
	gw.SetServer(s)
	if gw.mcpServer != s {
		t.Error("SetServer should store the server reference")
	}
}

func TestGateway_Register_MultipleTools(t *testing.T) {
	gw := NewGateway()
	for _, name := range []string{"logs", "errors", "db", "admin"} {
		gw.Register(name, nil, GatewayEntry{
			Description: name + " tools",
			Category:    "test",
		})
	}
	if len(gw.entries) != 4 {
		t.Errorf("entries count = %d, want 4", len(gw.entries))
	}
	if len(gw.handlers) != 4 {
		t.Errorf("handlers count = %d, want 4", len(gw.handlers))
	}
}

func TestGateway_DiscoverMultipleCategories(t *testing.T) {
	gw := NewGateway()
	gw.Register("logs", nil, GatewayEntry{Category: "Logs"})
	gw.Register("errors", nil, GatewayEntry{Category: "Errors"})
	gw.Register("admin", nil, GatewayEntry{Category: "Admin"})

	handler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{"tool": "discover"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tc := result.Content[0].(*mcp.TextContent)
	var data map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
		t.Fatalf("JSON parse error: %v", err)
	}
	if data["total_tools"] != 3.0 {
		t.Errorf("total_tools = %v, want 3", data["total_tools"])
	}
	categories := data["categories"].([]any)
	if len(categories) != 3 {
		t.Errorf("categories count = %d, want 3", len(categories))
	}
}

func TestGateway_DescribeWithActionsAndParams(t *testing.T) {
	gw := NewGateway()
	gw.Register("logs", nil, GatewayEntry{
		Description: "Log tools",
		Category:    "Log Intelligence",
		Access:      "read",
		ReadOnly:    true,
		Destructive: false,
		Idempotent:  true,
		Actions:     []string{"search", "stats", "tail"},
		Params: map[string]string{
			"query":   "search text",
			"level":   "log level filter",
			"service": "service name",
		},
	})

	handler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{
		"tool":   "describe",
		"action": "logs",
	})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected successful result")
	}

	tc := result.Content[0].(*mcp.TextContent)
	var data map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
		t.Fatalf("JSON parse error: %v", err)
	}

	actions := data["actions"].([]any)
	if len(actions) != 3 {
		t.Errorf("actions count = %d, want 3", len(actions))
	}
	params := data["params"].(map[string]any)
	if len(params) != 3 {
		t.Errorf("params count = %d, want 3", len(params))
	}
	if data["idempotent"] != true {
		t.Errorf("idempotent = %v, want true", data["idempotent"])
	}
}

func TestGateway_DiscoverEmptyCategory(t *testing.T) {
	gw := NewGateway()
	gw.Register("misc", nil, GatewayEntry{
		Description: "Miscellaneous",
		// Category intentionally empty.
	})

	handler := gw.Handler()
	req := MakeCallToolRequest("opentrace", map[string]any{"tool": "discover"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tc := result.Content[0].(*mcp.TextContent)
	var data map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
		t.Fatalf("JSON parse error: %v", err)
	}
	// Empty category should be grouped as "Other".
	categories := data["categories"].([]any)
	if len(categories) != 1 {
		t.Fatalf("categories count = %d, want 1", len(categories))
	}
	cat := categories[0].(map[string]any)
	if cat["category"] != "Other" {
		t.Errorf("empty category should be grouped as 'Other', got %v", cat["category"])
	}
}
