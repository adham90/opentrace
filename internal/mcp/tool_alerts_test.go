package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestCheckAlertsHandler_EmptyDeps(t *testing.T) {
	handler := checkAlertsHandler(Deps{})

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"service": "api"}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("unexpected error result")
	}

	txt := result.Content[0].(mcplib.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(txt), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["service"] != "api" {
		t.Errorf("service = %v, want api", resp["service"])
	}
}

func TestCheckAlertsHandler_NoService(t *testing.T) {
	handler := checkAlertsHandler(Deps{})

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("unexpected error result")
	}
}
