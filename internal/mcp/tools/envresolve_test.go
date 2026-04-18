package tools

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/mcp/envscope"
)

func withScope(envs ...string) context.Context {
	return envscope.With(context.Background(), envscope.EnvScope{Allowed: envs})
}

func TestResolveEnv_ExplicitArg(t *testing.T) {
	cases := []struct {
		name    string
		scope   []string
		env     string
		want    string
		wantErr string
	}{
		{"in_scope_single", []string{"staging"}, "staging", "staging", ""},
		{"in_scope_multi", []string{"staging", "production"}, "production", "production", ""},
		{"wildcard_allows_anything", []string{"*"}, "qa", "qa", ""},
		{"out_of_scope", []string{"staging"}, "production", "", "not authorized"},
		{"empty_scope_with_arg", nil, "production", "", "not authorized"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := withScope(tc.scope...)
			args := map[string]any{"environment": tc.env}
			got, err := ResolveEnv(ctx, args)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error with %q, got success (%q)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveEnv_SingleEnvAutoFill(t *testing.T) {
	ctx := withScope("staging")
	got, err := ResolveEnv(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "staging" {
		t.Errorf("got %q, want staging (auto-filled from sole env)", got)
	}
}

func TestResolveEnv_MultiEnvRequiresExplicit(t *testing.T) {
	ctx := withScope("staging", "production")
	_, err := ResolveEnv(ctx, map[string]any{})
	if err == nil {
		t.Fatal("expected error when multi-env scope lacks environment arg")
	}
	if !strings.Contains(err.Error(), "environment required") {
		t.Errorf("err = %v, want 'environment required'", err)
	}
}

func TestResolveEnv_LegacyWildcardFallback(t *testing.T) {
	t.Setenv("OPENTRACE_DEFAULT_ENV", "prod-us")

	ctx := withScope("*")
	got, err := ResolveEnv(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "prod-us" {
		t.Errorf("got %q, want prod-us (legacy wildcard fallback)", got)
	}
}

func TestResolveEnv_LegacyWildcardFallback_Default(t *testing.T) {
	// Ensure no override is in scope for this test.
	os.Unsetenv("OPENTRACE_DEFAULT_ENV")

	ctx := withScope("*")
	got, err := ResolveEnv(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "production" {
		t.Errorf("got %q, want production (hardcoded default)", got)
	}
}

func TestResolveEnv_EmptyScope(t *testing.T) {
	ctx := withScope()
	_, err := ResolveEnv(ctx, map[string]any{})
	if err == nil {
		t.Fatal("expected error on empty scope")
	}
	if !strings.Contains(err.Error(), "no environment scope") {
		t.Errorf("err = %v, want 'no environment scope'", err)
	}
}

func TestResolveEnvStrict_RejectsWildcard(t *testing.T) {
	ctx := withScope("*", "staging")
	_, err := ResolveEnvStrict(ctx, map[string]any{"environment": "*"})
	if err == nil {
		t.Fatal("expected error when requesting environment=*")
	}
	if !strings.Contains(err.Error(), "cannot be \"*\"") {
		t.Errorf("err = %v, want rejection of wildcard arg", err)
	}
}

func TestResolveEnvStrict_NoLegacyFallback(t *testing.T) {
	// Even when OPENTRACE_DEFAULT_ENV is set, legacy-wildcard tokens must
	// pass environment explicitly to the strict resolver.
	t.Setenv("OPENTRACE_DEFAULT_ENV", "production")

	ctx := withScope("*")
	_, err := ResolveEnvStrict(ctx, map[string]any{})
	if err == nil {
		t.Fatal("expected ResolveEnvStrict to refuse legacy wildcard without explicit env")
	}
	if !strings.Contains(err.Error(), "does not accept the legacy wildcard") {
		t.Errorf("err = %v, want 'does not accept the legacy wildcard'", err)
	}
}

func TestResolveEnvStrict_SingleEnvAutoFillStillWorks(t *testing.T) {
	ctx := withScope("staging")
	got, err := ResolveEnvStrict(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "staging" {
		t.Errorf("got %q, want staging", got)
	}
}
