package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/adham90/opentrace/internal/store"
)

func TestCodeContextHandler_NotFound(t *testing.T) {
	db := setupTestDB(t)
	ces := store.NewCodeEntityStore(db)

	handler := codeContextHandler(ces, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"entity_name": "missing.rb",
		"service":     "api",
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result)
	}

	txt := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(txt), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := resp["message"]; !ok {
		t.Error("expected message in response")
	}
}

func TestCodeContextHandler_Found(t *testing.T) {
	db := setupTestDB(t)
	ces := store.NewCodeEntityStore(db)

	ctx := context.Background()
	if err := ces.IncrementError(ctx, store.CodeEntityFile, "users.rb", "api"); err != nil {
		t.Fatalf("increment: %v", err)
	}

	handler := codeContextHandler(ces, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"entity_name": "users.rb",
		"service":     "api",
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	txt := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(txt), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["entity"] == nil {
		t.Error("expected entity in response")
	}
}

func TestWhatsFragileHandler(t *testing.T) {
	db := setupTestDB(t)
	ces := store.NewCodeEntityStore(db)
	ctx := context.Background()

	// Create some entities
	for i := 0; i < 3; i++ {
		_ = ces.IncrementError(ctx, store.CodeEntityFile, "fragile.rb", "api")
	}
	_ = ces.BatchRecomputeRisk(ctx)

	handler := whatsFragileHandler(ces)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"service": "api"}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	txt := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(txt), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["count"].(float64) < 1 {
		t.Error("expected at least 1 entity")
	}
}

func TestCodeRiskHandler(t *testing.T) {
	db := setupTestDB(t)
	ces := store.NewCodeEntityStore(db)
	ctx := context.Background()

	_ = ces.IncrementError(ctx, store.CodeEntityFile, "known.rb", "api")

	handler := codeRiskHandler(ces)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"service": "api",
		"files":   []any{"known.rb", "unknown.rb"},
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	txt := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(txt), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	files := resp["files"].([]any)
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
}
