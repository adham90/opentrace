package tools

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewToolResultText(t *testing.T) {
	result := NewToolResultText("hello world")

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", result.Content[0])
	}
	if tc.Text != "hello world" {
		t.Errorf("text = %q, want %q", tc.Text, "hello world")
	}
}

func TestNewToolResultError(t *testing.T) {
	result := NewToolResultError("something failed")

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", result.Content[0])
	}
	if tc.Text != "something failed" {
		t.Errorf("text = %q, want %q", tc.Text, "something failed")
	}
}

func TestGetArguments(t *testing.T) {
	t.Run("nil Params returns empty map", func(t *testing.T) {
		req := &mcp.CallToolRequest{
			Params: nil,
		}
		args := GetArguments(req)
		if args == nil {
			t.Fatal("expected non-nil map")
		}
		if len(args) != 0 {
			t.Errorf("expected empty map, got %v", args)
		}
	})

	t.Run("nil Arguments returns empty map", func(t *testing.T) {
		req := &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				Name:      "test",
				Arguments: nil,
			},
		}
		args := GetArguments(req)
		if args == nil {
			t.Fatal("expected non-nil map")
		}
		if len(args) != 0 {
			t.Errorf("expected empty map, got %v", args)
		}
	})

	t.Run("valid JSON arguments parsed correctly", func(t *testing.T) {
		rawArgs, _ := json.Marshal(map[string]any{
			"name":  "test-tool",
			"count": float64(42),
		})
		req := &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				Name:      "my-tool",
				Arguments: rawArgs,
			},
		}
		args := GetArguments(req)
		if args == nil {
			t.Fatal("expected non-nil map")
		}
		if args["name"] != "test-tool" {
			t.Errorf("args[name] = %v, want %q", args["name"], "test-tool")
		}
		if args["count"] != float64(42) {
			t.Errorf("args[count] = %v, want %v", args["count"], float64(42))
		}
	})

	t.Run("invalid JSON returns empty map", func(t *testing.T) {
		req := &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				Name:      "my-tool",
				Arguments: json.RawMessage(`{invalid json}`),
			},
		}
		args := GetArguments(req)
		if args == nil {
			t.Fatal("expected non-nil map")
		}
		if len(args) != 0 {
			t.Errorf("expected empty map for invalid JSON, got %v", args)
		}
	})
}

func TestMakeCallToolRequest(t *testing.T) {
	t.Run("creates request with name and args", func(t *testing.T) {
		args := map[string]any{
			"query": "SELECT 1",
			"limit": float64(10),
		}
		req := MakeCallToolRequest("run-query", args)

		if req == nil {
			t.Fatal("expected non-nil request")
		}
		if req.Params == nil {
			t.Fatal("expected non-nil Params")
		}
		if req.Params.Name != "run-query" {
			t.Errorf("name = %q, want %q", req.Params.Name, "run-query")
		}

		// Verify arguments round-trip.
		var parsed map[string]any
		if err := json.Unmarshal(req.Params.Arguments, &parsed); err != nil {
			t.Fatalf("failed to unmarshal arguments: %v", err)
		}
		if parsed["query"] != "SELECT 1" {
			t.Errorf("args[query] = %v, want %q", parsed["query"], "SELECT 1")
		}
		if parsed["limit"] != float64(10) {
			t.Errorf("args[limit] = %v, want %v", parsed["limit"], float64(10))
		}
	})

	t.Run("nil args works", func(t *testing.T) {
		req := MakeCallToolRequest("simple-tool", nil)

		if req == nil {
			t.Fatal("expected non-nil request")
		}
		if req.Params == nil {
			t.Fatal("expected non-nil Params")
		}
		if req.Params.Name != "simple-tool" {
			t.Errorf("name = %q, want %q", req.Params.Name, "simple-tool")
		}
		if req.Params.Arguments != nil {
			t.Errorf("expected nil Arguments for nil args, got %s", string(req.Params.Arguments))
		}
	})

	t.Run("empty args map produces valid JSON", func(t *testing.T) {
		req := MakeCallToolRequest("empty-tool", map[string]any{})

		if req.Params.Arguments == nil {
			t.Fatal("expected non-nil Arguments for empty map")
		}
		var parsed map[string]any
		if err := json.Unmarshal(req.Params.Arguments, &parsed); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if len(parsed) != 0 {
			t.Errorf("expected empty map, got %v", parsed)
		}
	})
}
