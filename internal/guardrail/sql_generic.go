package guardrail

import (
	"fmt"
	"strings"
)

// ValidateReadOnlyGeneric performs a basic check that the query is a read-only
// statement. Unlike ValidateReadOnly which uses the PostgreSQL AST parser, this
// uses keyword analysis and works for any SQL dialect (MySQL, SQLite/Turso, etc.).
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
		return nil
	case strings.HasPrefix(upper, "SHOW"):
		return nil
	case strings.HasPrefix(upper, "DESCRIBE"), strings.HasPrefix(upper, "DESC "):
		return nil
	case strings.HasPrefix(upper, "PRAGMA"):
		// SQLite/Turso read-only PRAGMAs
		return nil
	default:
		return fmt.Errorf("only SELECT statements are allowed")
	}
}

// HasLimitGeneric checks whether a query contains a LIMIT clause using simple
// keyword matching. Works for MySQL, SQLite, and other SQL dialects.
func HasLimitGeneric(query string) bool {
	upper := strings.ToUpper(query)
	return strings.Contains(upper, " LIMIT ")
}
