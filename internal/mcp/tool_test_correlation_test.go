package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/adham90/opentrace/internal/store"
)

func TestTestGapsHandler(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tcs := store.NewTestCorrelationStore(db)

	// Seed data
	now := time.Now().UTC().Format(time.RFC3339)
	db.ExecContext(ctx,
		`INSERT INTO uncovered_error_paths (error_fingerprint, service, error_class, error_count, priority_score, last_seen_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"fp-1", "api", "RuntimeError", 10, 50.0, now, now, now)

	handler := testGapsHandler(tcs)

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"service": "api"}

	result, err := handler(ctx, req)
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
	if resp["count"].(float64) != 1 {
		t.Errorf("expected 1 gap, got %v", resp["count"])
	}
}

func TestTestGapsHandler_Empty(t *testing.T) {
	db := setupTestDB(t)
	tcs := store.NewTestCorrelationStore(db)

	handler := testGapsHandler(tcs)

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	txt := result.Content[0].(mcplib.TextContent).Text
	if txt != "No uncovered error paths found." {
		t.Errorf("unexpected text: %s", txt)
	}
}

func TestTestPriorityHandler_RequiresFingerprint(t *testing.T) {
	db := setupTestDB(t)
	tcs := store.NewTestCorrelationStore(db)
	egs := store.NewErrorGroupStore(db)

	handler := testPriorityHandler(tcs, egs)

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when fingerprint missing")
	}
}

func TestTestPriorityHandler_WithData(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tcs := store.NewTestCorrelationStore(db)
	egs := store.NewErrorGroupStore(db)

	now := time.Now().UTC().Format(time.RFC3339)
	db.ExecContext(ctx,
		`INSERT INTO uncovered_error_paths (error_fingerprint, service, error_class, error_count, priority_score, last_seen_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"fp-test", "api", "ValueError", 5, 25.0, now, now, now)

	handler := testPriorityHandler(tcs, egs)

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"fingerprint": "fp-test"}

	result, err := handler(ctx, req)
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
	if resp["fingerprint"] != "fp-test" {
		t.Errorf("fingerprint = %v, want fp-test", resp["fingerprint"])
	}
}
