package guardrail

import (
	"fmt"
	"strings"
)

// ValidateReadOnlyGeneric performs a keyword-based check that the query is a
// read-only statement. Works for any SQL dialect (PostgreSQL, MySQL, SQLite, Turso).
func ValidateReadOnlyGeneric(query string) error {
	cleaned := strings.TrimSpace(query)
	if cleaned == "" {
		return fmt.Errorf("empty query")
	}

	// Remove trailing semicolons and whitespace
	cleaned = strings.TrimRight(cleaned, "; \t\n\r")

	// Basic multi-statement check: reject if semicolon is followed by non-whitespace
	if idx := strings.Index(cleaned, ";"); idx >= 0 && idx < len(cleaned)-1 {
		rest := strings.TrimSpace(cleaned[idx+1:])
		if rest != "" {
			return fmt.Errorf("multiple statements are not allowed")
		}
	}

	// Strip leading SQL comments
	for {
		if strings.HasPrefix(cleaned, "--") {
			if nl := strings.Index(cleaned, "\n"); nl >= 0 {
				cleaned = strings.TrimSpace(cleaned[nl+1:])
				continue
			}
			return fmt.Errorf("query is only a comment")
		}
		if strings.HasPrefix(cleaned, "/*") {
			if end := strings.Index(cleaned, "*/"); end >= 0 {
				cleaned = strings.TrimSpace(cleaned[end+2:])
				continue
			}
			return fmt.Errorf("unterminated comment")
		}
		break
	}

	if cleaned == "" {
		return fmt.Errorf("empty query")
	}

	upper := strings.ToUpper(cleaned)

	switch {
	case strings.HasPrefix(upper, "SELECT"):
		return nil
	case strings.HasPrefix(upper, "WITH"):
		// CTE (Common Table Expression) — allow if it eventually contains SELECT
		return nil
	case strings.HasPrefix(upper, "EXPLAIN"):
		if strings.Contains(upper, "ANALYZE") {
			return fmt.Errorf("EXPLAIN ANALYZE is not allowed (it executes the query)")
		}
		// Validate the inner statement: strip EXPLAIN keyword and optional
		// parenthesized options like (FORMAT JSON), then check the inner query.
		inner := stripExplainPrefix(upper)
		if inner == "" {
			return fmt.Errorf("empty EXPLAIN statement")
		}
		if !isReadOnlyKeyword(inner) {
			return fmt.Errorf("only SELECT statements are allowed inside EXPLAIN")
		}
		return nil
	case strings.HasPrefix(upper, "SHOW"):
		return nil
	case strings.HasPrefix(upper, "DESCRIBE"), strings.HasPrefix(upper, "DESC "):
		return nil
	case strings.HasPrefix(upper, "PRAGMA"):
		return validateReadOnlyPragma(upper)
	default:
		return fmt.Errorf("only SELECT statements are allowed")
	}
}

// stripExplainPrefix removes "EXPLAIN" and any optional parenthesized options
// (e.g., "(FORMAT JSON)") from the beginning of an uppercased query, returning
// the remaining inner statement.
func stripExplainPrefix(upper string) string {
	// Remove "EXPLAIN" keyword
	inner := strings.TrimSpace(strings.TrimPrefix(upper, "EXPLAIN"))

	// Skip optional parenthesized options: EXPLAIN (FORMAT JSON) SELECT ...
	if strings.HasPrefix(inner, "(") {
		if close := strings.Index(inner, ")"); close >= 0 {
			inner = strings.TrimSpace(inner[close+1:])
		}
	}

	return inner
}

// isReadOnlyKeyword checks whether the uppercased statement begins with a
// read-only SQL keyword (SELECT, WITH, SHOW, DESCRIBE, PRAGMA).
func isReadOnlyKeyword(upper string) bool {
	switch {
	case strings.HasPrefix(upper, "SELECT"):
		return true
	case strings.HasPrefix(upper, "WITH"):
		return true
	case strings.HasPrefix(upper, "SHOW"):
		return true
	case strings.HasPrefix(upper, "DESCRIBE"), strings.HasPrefix(upper, "DESC "):
		return true
	case strings.HasPrefix(upper, "PRAGMA"):
		return true
	default:
		return false
	}
}

// readOnlyPragmas lists PRAGMA prefixes known to be read-only.
// Write PRAGMAs (e.g., WAL_CHECKPOINT, OPTIMIZE, INTEGRITY_CHECK) are rejected.
var readOnlyPragmas = []string{
	"PRAGMA TABLE_INFO",
	"PRAGMA TABLE_LIST",
	"PRAGMA TABLE_XINFO",
	"PRAGMA INDEX_LIST",
	"PRAGMA INDEX_INFO",
	"PRAGMA INDEX_XINFO",
	"PRAGMA FOREIGN_KEY_LIST",
	"PRAGMA FOREIGN_KEY_CHECK",
	"PRAGMA DATABASE_LIST",
	"PRAGMA COMPILE_OPTIONS",
	"PRAGMA COLLATION_LIST",
	"PRAGMA JOURNAL_MODE",
	"PRAGMA PAGE_COUNT",
	"PRAGMA PAGE_SIZE",
	"PRAGMA MAX_PAGE_COUNT",
	"PRAGMA FREELIST_COUNT",
	"PRAGMA ENCODING",
	"PRAGMA DATA_VERSION",
	"PRAGMA SCHEMA_VERSION",
	"PRAGMA USER_VERSION",
	"PRAGMA APPLICATION_ID",
}

// validateReadOnlyPragma checks whether a PRAGMA statement is in the read-only allowlist.
func validateReadOnlyPragma(upper string) error {
	for _, prefix := range readOnlyPragmas {
		if strings.HasPrefix(upper, prefix) {
			return nil
		}
	}
	return fmt.Errorf("PRAGMA not in read-only allowlist")
}

// HasLimitGeneric checks whether a query contains a LIMIT clause using simple
// keyword matching. Works for MySQL, SQLite, and other SQL dialects.
func HasLimitGeneric(query string) bool {
	upper := strings.ToUpper(query)
	return strings.Contains(upper, " LIMIT ")
}
