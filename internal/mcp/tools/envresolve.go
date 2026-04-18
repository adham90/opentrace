package tools

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/adham90/opentrace/internal/mcp/envscope"
)

// ResolveEnv decides which environment a tool call should target.
//
// Resolution order (see docs/multi-env-support.md decisions #3 and #4):
//
//  1. If the caller passed environment=<name>, it must be in the token's
//     scope. Out-of-scope values return an error — never silently clamp.
//  2. Otherwise, if the token covers exactly one non-wildcard env, that
//     env is auto-filled (the silent single-env path).
//  3. Otherwise, for the legacy ["*"] wildcard scope, fall back to
//     OPENTRACE_DEFAULT_ENV (or "production") and log a deprecation
//     warning so admins see how much traffic still depends on the
//     grandfathered behaviour.
//  4. Otherwise (multi-env token, empty scope), return an error asking
//     the agent to specify environment explicitly.
//
// Tests and non-MCP callers that invoke handlers without running them
// through the SSE/stdio middleware see NO scope attached to the ctx. In
// that case we return "" so the underlying query runs unfiltered — the
// pre-multi-env behaviour. Production paths always attach a scope, so
// this fallback never hides real-traffic bugs.
//
// Write handlers that must NEVER auto-fill from the legacy wildcard — the
// watch creator is the canonical example — use ResolveEnvStrict instead.
func ResolveEnv(ctx context.Context, args map[string]any) (string, error) {
	scope, scopeAttached := envscope.FromOK(ctx)
	if !scopeAttached {
		// No middleware ran. Respect an explicit arg if provided, otherwise
		// return no filter. Tests rely on this.
		return ArgString(args, "environment"), nil
	}
	requested := ArgString(args, "environment")

	if requested != "" {
		if !scope.Allows(requested) {
			return "", fmt.Errorf(
				"token not authorized for environment %q (allowed: %v)",
				requested, scope.Allowed,
			)
		}
		return requested, nil
	}

	if env, ok := scope.SoleEnv(); ok {
		return env, nil
	}

	if scope.IsLegacyWildcard() {
		fallback := defaultEnv()
		slog.Warn("env_fallback",
			"event", "env_fallback",
			"scope", "*",
			"fallback_env", fallback,
			"reason", "legacy wildcard token omitted environment arg",
		)
		return fallback, nil
	}

	if scope.IsEmpty() {
		return "", fmt.Errorf(
			"this token has no environment scope — ask an admin to grant access",
		)
	}

	return "", fmt.Errorf(
		"environment required — choose from %v", scope.Allowed,
	)
}

// ResolveEnvStrict is the write-path variant used where the legacy wildcard
// fallback is unsafe. Specifically, watch creation requires an explicit env
// (see decision #6) — allowing a legacy-wildcard token to silently create a
// prod watch would produce false alerts.
//
// Rules:
//   - environment="*" is always rejected; watches are single-env by design.
//   - Any other value must pass the standard scope check.
//   - If the caller didn't pass environment and the token isn't exactly
//     one non-wildcard env, we refuse rather than falling back.
func ResolveEnvStrict(ctx context.Context, args map[string]any) (string, error) {
	scope, scopeAttached := envscope.FromOK(ctx)
	if !scopeAttached {
		// Same test fallback as ResolveEnv — if no middleware ran, the caller
		// is a test harness and we respect whatever it passed (even "").
		return ArgString(args, "environment"), nil
	}
	requested := ArgString(args, "environment")

	if requested == envscope.WildcardEnv {
		return "", fmt.Errorf(
			"environment cannot be %q — this action requires a specific env",
			envscope.WildcardEnv,
		)
	}

	if requested != "" {
		if !scope.Allows(requested) {
			return "", fmt.Errorf(
				"token not authorized for environment %q (allowed: %v)",
				requested, scope.Allowed,
			)
		}
		return requested, nil
	}

	if env, ok := scope.SoleEnv(); ok {
		return env, nil
	}

	if scope.IsLegacyWildcard() {
		return "", fmt.Errorf(
			"environment required — this action does not accept the legacy wildcard token fallback",
		)
	}

	if scope.IsEmpty() {
		return "", fmt.Errorf(
			"this token has no environment scope — ask an admin to grant access",
		)
	}

	return "", fmt.Errorf(
		"environment required — choose from %v", scope.Allowed,
	)
}

// defaultEnv returns the server's configured default env for the legacy
// wildcard fallback. Reads OPENTRACE_DEFAULT_ENV at call time so changes
// propagate without a restart in tests; config.Config.DefaultEnv is the
// production-side source of truth.
func defaultEnv() string {
	if v := os.Getenv("OPENTRACE_DEFAULT_ENV"); v != "" {
		return v
	}
	return "production"
}
