package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/mcp/envscope"
)

// ---------------------------------------------------------------------------
// Helper: get query executor from registry
// ---------------------------------------------------------------------------

// getQueryExecutor returns the active database connector's query executor,
// after enforcing that the caller's env scope permits the connector's
// environment. The registry keys connectors by type (one per type), so a token
// scoped to "staging" must not be allowed to run queries against a connector
// registered for "production".
func getQueryExecutor(ctx context.Context, registry *connector.Registry) (connector.QueryExecutor, *CallToolResult) {
	ds := registry.Get(connector.ConnectorDatabase)
	if ds == nil {
		return nil, NewToolResultError("No database connector is active. Connect a PostgreSQL data source first.")
	}
	if errResult := enforceConnectorEnvScope(ctx, ds); errResult != nil {
		return nil, errResult
	}
	qe, ok := ds.(connector.QueryExecutor)
	if !ok {
		return nil, NewToolResultError("The active database connector does not support direct queries.")
	}
	return qe, nil
}

// enforceConnectorEnvScope rejects the call when the caller's env scope does
// not permit the connector's pinned environment. Connectors with an empty or
// "*" environment are shared infrastructure and always allowed. Returns nil
// (allowed) when no scope is attached — the unit-test / non-MCP path.
func enforceConnectorEnvScope(ctx context.Context, ds connector.DataSource) *CallToolResult {
	es, ok := ds.(connector.EnvironmentScoped)
	if !ok {
		return nil
	}
	env := es.Environment()
	if env == "" || env == envscope.WildcardEnv {
		return nil
	}
	scope, attached := envscope.FromOK(ctx)
	if !attached {
		return nil
	}
	if !scope.Allows(env) {
		return NewToolResultError(fmt.Sprintf(
			"not authorized for the database connector's environment %q (token allows: %v)",
			env, scope.Allowed,
		))
	}
	return nil
}

// errParamsUnsupported is returned when a caller-supplied value has to be sent
// as a bound parameter but the active connector cannot bind parameters. We fail
// closed rather than falling back to string interpolation.
var errParamsUnsupported = errors.New("the active database connector does not support parameterized queries")

// executeReadQuery runs a read-only query, binding args as parameters
// ($1, $2, ...) when any are supplied. Every caller-supplied value that ends up
// in a query MUST travel through args — never through fmt.Sprintf into the SQL
// text — so that a value like `x' UNION SELECT ...` is compared as a literal
// string instead of being parsed as SQL.
func executeReadQuery(ctx context.Context, qe connector.QueryExecutor, query string, args ...any) (*connector.QueryResult, error) {
	if len(args) == 0 {
		return qe.ExecuteReadQuery(ctx, query)
	}
	pq, ok := qe.(connector.ParameterizedQueryExecutor)
	if !ok {
		return nil, errParamsUnsupported
	}
	return pq.ExecuteReadQueryArgs(ctx, query, args...)
}

// maxCatalogNameLen is Postgres's NAMEDATALEN-1 limit: no schema, table or
// index name can be longer, so a longer value can never match a real object.
// Rejecting it early keeps oversized junk (typically an injection attempt) out
// of the catalog queries entirely.
const maxCatalogNameLen = 63

// validateCatalogName rejects a caller-supplied catalog object name that cannot
// possibly identify a real object. It is a fail-fast guard, not the injection
// defence — bound parameters are. Returns nil when the name is acceptable.
func validateCatalogName(kind, name string) *CallToolResult {
	if len(name) > maxCatalogNameLen {
		return NewToolResultError(fmt.Sprintf(
			"%s name is too long (max %d characters)", kind, maxCatalogNameLen))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Utility functions (local to tools package)
// ---------------------------------------------------------------------------

// toInt converts an any value to int, returning 0 if conversion fails.
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case nil:
		return 0
	default:
		return 0
	}
}

// toInt64 converts an any value to int64, returning 0 if conversion fails.
func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case nil:
		return 0
	default:
		return 0
	}
}

// toFloat64 attempts to convert an interface value to float64.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case string:
		var f float64
		_, err := fmt.Sscanf(n, "%f", &f)
		return f, err == nil
	default:
		return 0, false
	}
}

// toString converts an any value to string.
func toString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// sanitizeIdentifier removes characters that aren't valid in a SQL identifier.
func sanitizeIdentifier(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// humanSize formats bytes into human-readable size.
func humanSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// round2 rounds a float64 to 2 decimal places.
func round2(f float64) float64 {
	return float64(int(f*100)) / 100
}

// absFloat returns the absolute value of a float64.
func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// parsePostgresArray parses a simple Postgres array literal like {a,b,c}.
func parsePostgresArray(s string) []string {
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
