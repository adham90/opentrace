package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

func TestAdminHandler_UnknownAction(t *testing.T) {
	d := AdminDeps{}
	handler := AdminHandler(d)

	req := MakeCallToolRequest("admin", map[string]any{"action": "nope"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError for unknown action")
	}
}

func TestHandleAdminSettings(t *testing.T) {
	settingsStore := mocks.NewSettingsStore()
	settingsStore.MaxQueryRows = 500
	settingsStore.StatementTimeout = 5000
	settingsStore.MCPName = "test-mcp"
	d := AdminDeps{SettingsStore: settingsStore}

	result, err := HandleAdminSettings(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	text := extractText(t, result)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if parsed["retention_days"] != float64(30) {
		t.Errorf("retention_days = %v, want 30", parsed["retention_days"])
	}
	if parsed["max_query_rows"] != float64(500) {
		t.Errorf("max_query_rows = %v, want 500", parsed["max_query_rows"])
	}
	if parsed["statement_timeout_ms"] != float64(5000) {
		t.Errorf("statement_timeout_ms = %v, want 5000", parsed["statement_timeout_ms"])
	}
	if parsed["mcp_name"] != "test-mcp" {
		t.Errorf("mcp_name = %v, want %q", parsed["mcp_name"], "test-mcp")
	}
}

func TestHandleAdminSettings_NilStore(t *testing.T) {
	d := AdminDeps{SettingsStore: nil}

	result, err := HandleAdminSettings(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when SettingsStore is nil")
	}
}

func TestHandleListUsers(t *testing.T) {
	userStore := mocks.NewUserStore()
	// Seed two users.
	for i := 0; i < 2; i++ {
		_, err := userStore.Create(context.Background(), store.CreateUserParams{
			Email:       fmt.Sprintf("user%d@example.com", i),
			DisplayName: fmt.Sprintf("User %d", i),
			Role:        store.RoleMember,
		})
		if err != nil {
			t.Fatalf("failed to seed user: %v", err)
		}
	}
	d := AdminDeps{UserStore: userStore}

	result, err := HandleListUsers(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	text := extractText(t, result)
	var users []map[string]any
	if err := json.Unmarshal([]byte(text), &users); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("expected 2 users, got %d", len(users))
	}
}

func TestHandleListUsers_NilStore(t *testing.T) {
	d := AdminDeps{UserStore: nil}

	result, err := HandleListUsers(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when UserStore is nil")
	}
}

func TestHandleUpdateRetention(t *testing.T) {
	settingsStore := mocks.NewSettingsStore()
	d := AdminDeps{SettingsStore: settingsStore}

	t.Run("valid retention update", func(t *testing.T) {
		args := map[string]any{"retention_days": float64(90)}
		result, err := HandleUpdateRetention(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.IsError {
			t.Error("expected IsError to be false")
		}

		text := extractText(t, result)
		if text == "" {
			t.Error("expected non-empty result text")
		}

		// Verify the store was updated.
		retention, err := settingsStore.GetRetention(context.Background())
		if err != nil {
			t.Fatalf("failed to read retention: %v", err)
		}
		if retention.RetentionDays != 90 {
			t.Errorf("retention = %d, want 90", retention.RetentionDays)
		}
	})

	t.Run("missing retention_days", func(t *testing.T) {
		args := map[string]any{}
		result, err := HandleUpdateRetention(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError when retention_days is missing")
		}
	})

	t.Run("out of range retention_days", func(t *testing.T) {
		args := map[string]any{"retention_days": float64(999)}
		result, err := HandleUpdateRetention(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError when retention_days exceeds 365")
		}
	})
}

func TestHandleAudit(t *testing.T) {
	auditStore := mocks.NewAuditStore()

	// Seed some audit entries.
	for i := 0; i < 3; i++ {
		_ = auditStore.Log(context.Background(), store.LogAuditParams{
			UserID:    "user-1",
			UserEmail: "admin@example.com",
			Action:    fmt.Sprintf("test.action.%d", i),
		})
	}

	d := AdminDeps{AuditStore: auditStore}

	t.Run("returns audit entries", func(t *testing.T) {
		args := map[string]any{}
		result, err := HandleAudit(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.IsError {
			t.Error("expected IsError to be false")
		}

		text := extractText(t, result)
		var entries []map[string]any
		if err := json.Unmarshal([]byte(text), &entries); err != nil {
			t.Fatalf("failed to parse result JSON: %v", err)
		}
		if len(entries) != 3 {
			t.Errorf("expected 3 audit entries, got %d", len(entries))
		}
	})

	t.Run("empty audit log", func(t *testing.T) {
		emptyAuditStore := mocks.NewAuditStore()
		dEmpty := AdminDeps{AuditStore: emptyAuditStore}

		args := map[string]any{}
		result, err := HandleAudit(context.Background(), dEmpty, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("expected IsError to be false for empty audit log")
		}
		text := extractText(t, result)
		if text != "No audit log entries found." {
			t.Errorf("unexpected text: %q", text)
		}
	})
}

func TestHandleAudit_NilStore(t *testing.T) {
	d := AdminDeps{AuditStore: nil}
	args := map[string]any{}
	result, err := HandleAudit(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when AuditStore is nil")
	}
}

func TestHandleNotes(t *testing.T) {
	noteStore := mocks.NewAgentNoteStore()
	d := AdminDeps{AgentNoteStore: noteStore}

	t.Run("create note via upsert", func(t *testing.T) {
		args := map[string]any{
			"entity_type": "query",
			"entity_id":   "slow-query-1",
			"note":        "This query needs an index on users.email",
		}
		result, err := HandleNotes(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.IsError {
			t.Error("expected IsError to be false")
		}
	})

	t.Run("list notes with empty store", func(t *testing.T) {
		args := map[string]any{}
		result, err := HandleNotes(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		// The stub returns nil for List, so we should get the "no notes" message.
		if result.IsError {
			t.Error("expected IsError to be false")
		}
	})

	t.Run("missing entity_type on upsert", func(t *testing.T) {
		args := map[string]any{
			"note": "some note without entity",
		}
		result, err := HandleNotes(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError when entity_type is missing for upsert")
		}
	})
}

func TestHandleNotes_NilStore(t *testing.T) {
	d := AdminDeps{AgentNoteStore: nil}
	args := map[string]any{}
	result, err := HandleNotes(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when AgentNoteStore is nil")
	}
}

func TestHandleDeleteNote(t *testing.T) {
	noteStore := mocks.NewAgentNoteStore()
	d := AdminDeps{AgentNoteStore: noteStore}

	t.Run("delete note", func(t *testing.T) {
		args := map[string]any{
			"entity_type": "query",
			"entity_id":   "slow-query-1",
		}
		result, err := HandleDeleteNote(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.IsError {
			t.Error("expected IsError to be false")
		}

		text := extractText(t, result)
		var parsed map[string]any
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			t.Fatalf("failed to parse result JSON: %v", err)
		}
		if parsed["status"] != "deleted" {
			t.Errorf("status = %v, want %q", parsed["status"], "deleted")
		}
	})

	t.Run("missing entity_type", func(t *testing.T) {
		args := map[string]any{"entity_id": "x"}
		result, err := HandleDeleteNote(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError when entity_type is missing")
		}
	})

	t.Run("missing entity_id", func(t *testing.T) {
		args := map[string]any{"entity_type": "query"}
		result, err := HandleDeleteNote(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError when entity_id is missing")
		}
	})
}

func TestHandleAdminActivity_NilRegistry(t *testing.T) {
	d := AdminDeps{Registry: nil}

	result, err := HandleAdminActivity(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when Registry is nil")
	}
}
