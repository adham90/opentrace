package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

func TestSetupHandler_UnknownAction(t *testing.T) {
	d := SetupDeps{}
	handler := SetupHandler(d)

	req := MakeCallToolRequest("setup", map[string]any{"action": "nope"})
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

func TestHandleSetupStatus(t *testing.T) {
	logStore := mocks.NewLogStore()
	userStore := mocks.NewUserStore()
	dsStore := mocks.NewDataSourceStore()

	// Seed a user so Count returns 1.
	_, err := userStore.Create(context.Background(), store.CreateUserParams{
		Email: "admin@example.com",
		Role:  store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	d := SetupDeps{
		LogStore:  logStore,
		UserStore: userStore,
		DSStore:   dsStore,
	}

	result, err := HandleSetupStatus(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	// Parse the JSON text content to verify key fields.
	text := extractText(t, result)
	var status map[string]any
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if status["server"] != "ok" {
		t.Errorf("server = %v, want %q", status["server"], "ok")
	}
	if status["users"] != float64(1) {
		t.Errorf("users = %v, want 1", status["users"])
	}
}

func TestHandleSetupStatus_EnvScope(t *testing.T) {
	cases := []struct {
		name        string
		allowed     []string
		wantMode    string
		wantWarnSub string // substring the warning must contain; "" means no warning
	}{
		{"single_env_no_warning", []string{"production"}, "single", ""},
		{"multi_env_requires_explicit", []string{"staging", "production"}, "multi", "must specify environment"},
		{"legacy_wildcard_deprecated", []string{"*"}, "legacy_wildcard", "deprecated wildcard"},
		{"denied_has_warning", []string{}, "denied", "no environment scope"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := SetupDeps{}
			ctx := envscope.With(context.Background(), envscope.EnvScope{Allowed: tc.allowed})

			result, err := HandleSetupStatus(ctx, d)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil || result.IsError {
				t.Fatalf("unexpected error result: %+v", result)
			}

			text := extractText(t, result)
			var status map[string]any
			if err := json.Unmarshal([]byte(text), &status); err != nil {
				t.Fatalf("failed to parse result JSON: %v", err)
			}

			// env_scope round-trips as []any through JSON.
			scopeAny, ok := status["env_scope"].([]any)
			if !ok {
				t.Fatalf("env_scope missing or wrong type: %T", status["env_scope"])
			}
			if len(scopeAny) != len(tc.allowed) {
				t.Errorf("env_scope len = %d, want %d", len(scopeAny), len(tc.allowed))
			}
			for i, v := range scopeAny {
				if s, _ := v.(string); s != tc.allowed[i] {
					t.Errorf("env_scope[%d] = %v, want %q", i, v, tc.allowed[i])
				}
			}

			if status["scope_mode"] != tc.wantMode {
				t.Errorf("scope_mode = %v, want %q", status["scope_mode"], tc.wantMode)
			}

			warn, hasWarn := status["scope_warning"].(string)
			if tc.wantWarnSub == "" {
				if hasWarn {
					t.Errorf("expected no scope_warning, got %q", warn)
				}
			} else {
				if !hasWarn {
					t.Fatalf("expected scope_warning containing %q, got none", tc.wantWarnSub)
				}
				if !strings.Contains(warn, tc.wantWarnSub) {
					t.Errorf("scope_warning %q missing substring %q", warn, tc.wantWarnSub)
				}
			}
		})
	}
}

func TestHandleSetupStatus_NoScopeInContext(t *testing.T) {
	// When scope is missing entirely (e.g. a test or a non-MCP caller),
	// setup status still reports scope fields — just with the "denied"
	// default — so downstream consumers see a consistent shape.
	d := SetupDeps{}
	result, err := HandleSetupStatus(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := extractText(t, result)
	var status map[string]any
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	if status["scope_mode"] != "denied" {
		t.Errorf("scope_mode = %v, want denied", status["scope_mode"])
	}
	scopeAny, ok := status["env_scope"].([]any)
	if !ok {
		t.Fatalf("env_scope missing or wrong type: %T", status["env_scope"])
	}
	if len(scopeAny) != 0 {
		t.Errorf("env_scope = %v, want empty", scopeAny)
	}
}

func TestHandleSetupStatus_NilStores(t *testing.T) {
	d := SetupDeps{
		LogStore:  nil,
		UserStore: nil,
		DSStore:   nil,
	}

	result, err := HandleSetupStatus(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false even with nil stores")
	}

	text := extractText(t, result)
	var status map[string]any
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if status["server"] != "ok" {
		t.Errorf("server = %v, want %q", status["server"], "ok")
	}
}

func TestHandleSetupVerify(t *testing.T) {
	logStore := mocks.NewLogStore()
	d := SetupDeps{LogStore: logStore}

	result, err := HandleSetupVerify(context.Background(), d)
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
	// With no log entries the mock returns nil counts, so expect no_data status.
	if parsed["status"] != "no_data" {
		t.Errorf("status = %v, want %q", parsed["status"], "no_data")
	}
}

func TestHandleSetupVerify_NilLogStore(t *testing.T) {
	d := SetupDeps{LogStore: nil}

	result, err := HandleSetupVerify(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError when LogStore is nil")
	}
}

func TestHandleSetupDetect(t *testing.T) {
	d := SetupDeps{}

	t.Run("with Rails files", func(t *testing.T) {
		args := map[string]any{
			"files": "Gemfile,config/application.rb,Rakefile",
		}
		result, err := HandleSetupDetect(context.Background(), d, args)
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
		if parsed["detected_framework"] != "Ruby on Rails" {
			t.Errorf("detected_framework = %v, want %q", parsed["detected_framework"], "Ruby on Rails")
		}
		if parsed["confidence"] != "high" {
			t.Errorf("confidence = %v, want %q", parsed["confidence"], "high")
		}
	})

	t.Run("with no files", func(t *testing.T) {
		args := map[string]any{}
		result, err := HandleSetupDetect(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.IsError {
			t.Error("expected IsError to be false for empty files hint")
		}
	})

	t.Run("with Go files", func(t *testing.T) {
		args := map[string]any{"files": "go.mod,main.go"}
		result, err := HandleSetupDetect(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := extractText(t, result)
		var parsed map[string]any
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			t.Fatalf("failed to parse result JSON: %v", err)
		}
		if parsed["detected_framework"] != "Go" {
			t.Errorf("detected_framework = %v, want %q", parsed["detected_framework"], "Go")
		}
	})
}

func TestHandleSetupGuide(t *testing.T) {
	settingsStore := mocks.NewSettingsStore()
	settingsStore.APIKey = "test-api-key-123"
	d := SetupDeps{SettingsStore: settingsStore}

	t.Run("rails guide", func(t *testing.T) {
		args := map[string]any{"framework": "rails"}
		result, err := HandleSetupGuide(context.Background(), d, args)
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
		if !strings.Contains(text, "Gemfile") {
			t.Error("expected guide to mention Gemfile")
		}
		if !strings.Contains(text, "test-api-key-123") {
			t.Error("expected guide to contain the API key")
		}
	})

	t.Run("missing framework", func(t *testing.T) {
		args := map[string]any{}
		result, err := HandleSetupGuide(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError when framework is missing")
		}
	})

	t.Run("unknown framework", func(t *testing.T) {
		args := map[string]any{"framework": "cobol"}
		result, err := HandleSetupGuide(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError for unknown framework")
		}
	})
}

func TestSetupHandler_DispatchesCorrectly(t *testing.T) {
	d := SetupDeps{
		LogStore:      mocks.NewLogStore(),
		UserStore:     mocks.NewUserStore(),
		SettingsStore: mocks.NewSettingsStore(),
		DSStore:       mocks.NewDataSourceStore(),
	}
	handler := SetupHandler(d)

	req := MakeCallToolRequest("setup", map[string]any{"action": "status"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Error("expected IsError to be false for action=status")
	}

	text := extractText(t, result)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
	if data["server"] != "ok" {
		t.Errorf("expected server=ok, got %v", data["server"])
	}
}

func TestHandleSetupDBGuide(t *testing.T) {
	d := SetupDeps{}

	t.Run("postgres guide", func(t *testing.T) {
		args := map[string]any{"database": "postgres"}
		result, err := HandleSetupDBGuide(context.Background(), d, args)
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
		if !strings.Contains(text, "Postgres") {
			t.Error("expected guide to mention Postgres")
		}
	})

	t.Run("missing database", func(t *testing.T) {
		args := map[string]any{}
		result, err := HandleSetupDBGuide(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError when database is missing")
		}
	})

	t.Run("unknown database", func(t *testing.T) {
		args := map[string]any{"database": "sqlite"}
		result, err := HandleSetupDBGuide(context.Background(), d, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError for unknown database")
		}
	})
}

