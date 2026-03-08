package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/adham90/opentrace/internal/store"
)

func TestDeployHistoryHandler(t *testing.T) {
	db := setupTestDB(t)
	ds := store.NewDeployStore(db)
	ctx := context.Background()

	_, err := ds.Create(ctx, store.CreateDeployParams{
		Service:    "api",
		CommitHash: "abc123",
		Branch:     "main",
		Author:     "dev@example.com",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	handler := deployHistoryHandler(ds)
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
	if resp["count"].(float64) != 1 {
		t.Errorf("expected 1 deploy, got %v", resp["count"])
	}
}

func TestDeployImpactHandler_Pending(t *testing.T) {
	db := setupTestDB(t)
	ds := store.NewDeployStore(db)
	ctx := context.Background()

	_, err := ds.Create(ctx, store.CreateDeployParams{
		Service:    "api",
		CommitHash: "pending1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	handler := deployImpactHandler(ds)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"commit_hash": "pending1"}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	txt := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(txt), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	impact := resp["impact"].(map[string]any)
	if impact["status"] != "pending" {
		t.Errorf("expected pending status, got %v", impact["status"])
	}
}

func TestRecordDeployHandler(t *testing.T) {
	db := setupTestDB(t)
	ds := store.NewDeployStore(db)

	handler := recordDeployHandler(ds)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"service": "api",
		"commit":  "new123",
		"branch":  "main",
		"author":  "dev@example.com",
		"files":   []any{"users.rb", "orders.rb"},
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	txt := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(txt), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["commit_hash"] != "new123" {
		t.Errorf("expected commit_hash=new123, got %v", resp["commit_hash"])
	}
}
