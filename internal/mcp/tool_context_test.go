package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestClassifyTaskType(t *testing.T) {
	tests := []struct {
		task string
		want string
	}{
		{"debugging payment errors", "debugging"},
		{"investigating slow queries", "debugging"},
		{"deploying api service", "deploying"},
		{"shipping new release", "deploying"},
		{"testing auth flow", "testing"},
		{"writing tests for login", "testing"},
		{"reviewing PR #42", "reviewing"},
		{"code review for payments", "reviewing"},
		{"editing orders_controller.rb", "editing"},
		{"building new feature", "editing"},
		{"", "editing"},
	}

	for _, tt := range tests {
		t.Run(tt.task, func(t *testing.T) {
			got := classifyTaskType(tt.task)
			if got != tt.want {
				t.Errorf("classifyTaskType(%q) = %q, want %q", tt.task, got, tt.want)
			}
		})
	}
}

func TestContextMetaToolHandler_RequiresTask(t *testing.T) {
	handler := contextMetaToolHandler(Deps{})

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when task is missing")
	}
}

func TestContextMetaToolHandler_Debugging(t *testing.T) {
	handler := contextMetaToolHandler(Deps{})

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"task": "debugging payment errors",
	}

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
	if resp["task_type"] != "debugging" {
		t.Errorf("task_type = %v, want debugging", resp["task_type"])
	}
}

func TestContextMetaToolHandler_Deploying(t *testing.T) {
	handler := contextMetaToolHandler(Deps{})

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"task":    "deploying payments service",
		"service": "payments",
	}

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
	if resp["task_type"] != "deploying" {
		t.Errorf("task_type = %v, want deploying", resp["task_type"])
	}
}
