// Package envscope holds the multi-env authorization scope that flows from
// the authenticated MCP user through the request ctx into tool handlers.
//
// It lives in its own subpackage so both the MCP server (which attaches the
// scope at auth time) and the MCP tool handlers (which read it to resolve
// the target env per call) can import it without a cycle through the
// parent mcp package.
package envscope

import (
	"context"
	"slices"

	"github.com/adham90/opentrace/pkg/store"
)

// envScopeKey is the ctx key for EnvScope. Unexported so callers go through
// With / From rather than poking at the raw value.
type envScopeKey struct{}

// WildcardEnv is the legacy scope value that allows any environment. Assigned
// to all pre-multi-env tokens during migration so existing integrations keep
// working; see decision #4 in docs/multi-env-support.md.
const WildcardEnv = "*"

// EnvScope describes which environments a request is authorized to read or
// write. A zero-value EnvScope has an empty Allowed slice, which means
// "no scope granted" — tool handlers treat it as denied.
//
// Valid shapes (see pkg/store.User doc):
//   - nil / empty slice        — denied
//   - []string{"production"}   — single-env, auto-fills
//   - []string{"staging","production"} — multi-env, env arg required
//   - []string{"*"}            — legacy wildcard (deprecation fallback on writes)
type EnvScope struct {
	Allowed []string
}

// FromUser builds an EnvScope from a user's AllowedEnvironments. A nil
// user yields an empty scope (denied).
func FromUser(u *store.User) EnvScope {
	if u == nil {
		return EnvScope{}
	}
	envs := append([]string(nil), u.AllowedEnvironments...)
	return EnvScope{Allowed: envs}
}

// AllowsAll reports whether the scope permits every environment. True when
// any entry in Allowed is "*".
func (s EnvScope) AllowsAll() bool {
	return slices.Contains(s.Allowed, WildcardEnv)
}

// Allows reports whether the scope permits the given environment. A wildcard
// scope allows anything; otherwise the env must be an explicit member.
func (s EnvScope) Allows(env string) bool {
	if s.AllowsAll() {
		return true
	}
	return slices.Contains(s.Allowed, env)
}

// SoleEnv returns the single env the scope is pinned to, if any. It returns
// (env, true) only when Allowed has exactly one entry and that entry is not
// the wildcard. In every other case (empty, multi-element, or wildcard) it
// returns ("", false).
//
// This is the auto-fill gate: single-env tokens skip the "pick one"
// requirement; everything else must pass environment explicitly.
func (s EnvScope) SoleEnv() (string, bool) {
	if len(s.Allowed) != 1 {
		return "", false
	}
	if s.Allowed[0] == WildcardEnv {
		return "", false
	}
	return s.Allowed[0], true
}

// IsLegacyWildcard reports whether the scope is exactly ["*"] — the shape
// assigned by the backward-compat migration. Tools can use this to opt into
// the deprecation fallback (see ResolveEnv / ResolveEnvStrict in PR 3).
func (s EnvScope) IsLegacyWildcard() bool {
	return len(s.Allowed) == 1 && s.Allowed[0] == WildcardEnv
}

// IsEmpty reports whether the scope grants access to nothing.
func (s EnvScope) IsEmpty() bool {
	return len(s.Allowed) == 0
}

// Mode returns a short label describing the scope shape. Used by
// setup:status and any other surface that reports scope to the agent.
func (s EnvScope) Mode() string {
	switch {
	case s.IsEmpty():
		return "denied"
	case s.IsLegacyWildcard():
		return "legacy_wildcard"
	case len(s.Allowed) == 1:
		return "single"
	default:
		return "multi"
	}
}

// With returns ctx with the given scope attached.
func With(ctx context.Context, s EnvScope) context.Context {
	return context.WithValue(ctx, envScopeKey{}, s)
}

// From returns the scope attached via With, or an empty scope if none was
// attached. Callers that need to distinguish "no scope attached" from
// "scope explicitly empty" should use FromOK.
func From(ctx context.Context) EnvScope {
	s, _ := FromOK(ctx)
	return s
}

// FromOK returns the scope and a bool indicating whether one was actually
// attached. Useful in tests and in middleware that wants to reject requests
// reaching a handler without scope plumbing.
func FromOK(ctx context.Context) (EnvScope, bool) {
	if ctx == nil {
		return EnvScope{}, false
	}
	s, ok := ctx.Value(envScopeKey{}).(EnvScope)
	return s, ok
}
