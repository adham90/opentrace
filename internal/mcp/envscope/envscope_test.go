package envscope

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/pkg/store"
)

func TestEnvScope_AllowsAll(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		want    bool
	}{
		{"nil", nil, false},
		{"empty", []string{}, false},
		{"single_env", []string{"production"}, false},
		{"multi_env", []string{"staging", "production"}, false},
		{"wildcard_alone", []string{"*"}, true},
		{"wildcard_mixed", []string{"staging", "*"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EnvScope{Allowed: tc.allowed}.AllowsAll()
			if got != tc.want {
				t.Errorf("AllowsAll(%v) = %v, want %v", tc.allowed, got, tc.want)
			}
		})
	}
}

func TestEnvScope_Allows(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		env     string
		want    bool
	}{
		{"empty_denies_anything", nil, "production", false},
		{"empty_denies_empty_env", nil, "", false},
		{"wildcard_allows_any", []string{"*"}, "production", true},
		{"wildcard_allows_empty", []string{"*"}, "", true},
		{"single_matches", []string{"staging"}, "staging", true},
		{"single_mismatch", []string{"staging"}, "production", false},
		{"multi_first", []string{"staging", "production"}, "staging", true},
		{"multi_second", []string{"staging", "production"}, "production", true},
		{"multi_miss", []string{"staging", "production"}, "dev", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EnvScope{Allowed: tc.allowed}.Allows(tc.env)
			if got != tc.want {
				t.Errorf("Allows(%q) with %v = %v, want %v", tc.env, tc.allowed, got, tc.want)
			}
		})
	}
}

func TestEnvScope_SoleEnv(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		wantEnv string
		wantOK  bool
	}{
		{"empty", nil, "", false},
		{"single_non_wildcard", []string{"production"}, "production", true},
		{"single_wildcard_not_sole", []string{"*"}, "", false},
		{"multi", []string{"staging", "production"}, "", false},
		{"wildcard_mixed", []string{"staging", "*"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, ok := EnvScope{Allowed: tc.allowed}.SoleEnv()
			if env != tc.wantEnv || ok != tc.wantOK {
				t.Errorf("SoleEnv() with %v = (%q, %v), want (%q, %v)",
					tc.allowed, env, ok, tc.wantEnv, tc.wantOK)
			}
		})
	}
}

func TestEnvScope_IsLegacyWildcard(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		want    bool
	}{
		{"exactly_wildcard", []string{"*"}, true},
		{"empty", nil, false},
		{"single_non_wildcard", []string{"production"}, false},
		{"wildcard_mixed", []string{"*", "production"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EnvScope{Allowed: tc.allowed}.IsLegacyWildcard()
			if got != tc.want {
				t.Errorf("IsLegacyWildcard() with %v = %v, want %v", tc.allowed, got, tc.want)
			}
		})
	}
}

func TestEnvScope_Mode(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		want    string
	}{
		{"empty", nil, "denied"},
		{"legacy_wildcard", []string{"*"}, "legacy_wildcard"},
		{"single", []string{"production"}, "single"},
		{"multi", []string{"staging", "production"}, "multi"},
		{"wildcard_mixed_counts_as_multi", []string{"*", "staging"}, "multi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EnvScope{Allowed: tc.allowed}.Mode()
			if got != tc.want {
				t.Errorf("Mode() with %v = %q, want %q", tc.allowed, got, tc.want)
			}
		})
	}
}

func TestEnvScope_Context(t *testing.T) {
	t.Run("round_trip", func(t *testing.T) {
		scope := EnvScope{Allowed: []string{"staging"}}
		ctx := With(context.Background(), scope)
		got, ok := FromOK(ctx)
		if !ok {
			t.Fatal("expected scope to be present in ctx")
		}
		if len(got.Allowed) != 1 || got.Allowed[0] != "staging" {
			t.Errorf("got %v, want [staging]", got.Allowed)
		}
	})

	t.Run("no_scope_attached", func(t *testing.T) {
		got, ok := FromOK(context.Background())
		if ok {
			t.Errorf("expected ok=false, got scope=%v", got)
		}
		if len(got.Allowed) != 0 {
			t.Errorf("expected empty scope, got %v", got)
		}
	})

	t.Run("nil_ctx", func(t *testing.T) {
		// FromOK defends against nil so a mis-wired call site can't panic.
		var ctx context.Context
		got, ok := FromOK(ctx)
		if ok {
			t.Errorf("expected ok=false for nil ctx, got scope=%v", got)
		}
		if len(got.Allowed) != 0 {
			t.Errorf("expected empty scope, got %v", got)
		}
	})

	t.Run("shortcut_returns_empty_when_absent", func(t *testing.T) {
		got := From(context.Background())
		if len(got.Allowed) != 0 {
			t.Errorf("expected empty scope, got %v", got)
		}
	})
}

func TestFromUser(t *testing.T) {
	t.Run("nil_user", func(t *testing.T) {
		s := FromUser(nil)
		if len(s.Allowed) != 0 {
			t.Errorf("FromUser(nil) should be empty, got %v", s.Allowed)
		}
	})

	t.Run("user_with_scope", func(t *testing.T) {
		u := &store.User{AllowedEnvironments: []string{"staging", "production"}}
		s := FromUser(u)
		if len(s.Allowed) != 2 || s.Allowed[0] != "staging" || s.Allowed[1] != "production" {
			t.Errorf("got %v, want [staging production]", s.Allowed)
		}
	})

	t.Run("independent_from_source_slice", func(t *testing.T) {
		src := []string{"staging"}
		u := &store.User{AllowedEnvironments: src}
		s := FromUser(u)
		src[0] = "production"
		if s.Allowed[0] != "staging" {
			t.Errorf("scope aliased user slice: got %q, want %q", s.Allowed[0], "staging")
		}
	})

	t.Run("empty_user", func(t *testing.T) {
		u := &store.User{}
		s := FromUser(u)
		if len(s.Allowed) != 0 {
			t.Errorf("empty user should produce empty scope, got %v", s.Allowed)
		}
		if !s.IsEmpty() {
			t.Error("expected IsEmpty=true")
		}
	})
}
