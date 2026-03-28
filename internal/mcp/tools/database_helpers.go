package tools

import (
	"fmt"
	"strings"


	"github.com/adham90/opentrace/internal/connector"
)

// ---------------------------------------------------------------------------
// Helper: get query executor from registry
// ---------------------------------------------------------------------------

func getQueryExecutor(registry *connector.Registry) (connector.QueryExecutor, *CallToolResult) {
	ds := registry.Get(connector.ConnectorDatabase)
	if ds == nil {
		return nil, NewToolResultError("No database connector is active. Connect a PostgreSQL data source first.")
	}
	qe, ok := ds.(connector.QueryExecutor)
	if !ok {
		return nil, NewToolResultError("The active database connector does not support direct queries.")
	}
	return qe, nil
}

// ---------------------------------------------------------------------------
// Utility functions (local to tools package)
// ---------------------------------------------------------------------------

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
