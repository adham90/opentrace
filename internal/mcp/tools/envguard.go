package tools

import (
	"context"

	"github.com/adham90/opentrace/internal/mcp/envscope"
)

// scopeAllowsEnv reports whether the caller's environment scope permits reading
// data tagged with env. It is the choke point for env-scoped authorization on
// read actions that resolve a record by an identifier (log_id / trace_id /
// fingerprint) rather than by an env-filtered query — the query-filter path
// uses ResolveEnv instead.
//
// When no scope is attached to ctx (unit tests and non-MCP callers), it returns
// true, mirroring ResolveEnv's test fallback so handlers invoked without the
// auth middleware keep working.
//
// A record whose environment is empty is only readable by a wildcard or
// unscoped caller; a token pinned to specific env(s) is denied. That is the
// safe default for legacy / unknown-env rows: a staging-scoped token must never
// be handed a row that might belong to production.
func scopeAllowsEnv(ctx context.Context, env string) bool {
	scope, ok := envscope.FromOK(ctx)
	if !ok {
		return true
	}
	return scope.Allows(env)
}
