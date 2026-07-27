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
	case strings.HasPrefix(upper, "SELECT"), strings.HasPrefix(upper, "WITH"):
		// Plain SELECT or a CTE (WITH ...). A read-only transaction does NOT
		// block side-effecting functions, so keyword matching alone is unsafe.
		if fn, bad := callsSideEffectingFunction(upper); bad {
			return fmt.Errorf("query rejected: calls side-effecting function %s()", fn)
		}
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
		if fn, bad := callsSideEffectingFunction(inner); bad {
			return fmt.Errorf("query rejected: calls side-effecting function %s()", fn)
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

// pragmaAllowsArg lists read-only PRAGMAs that accept a name argument in
// parentheses (e.g. PRAGMA table_info(users)). Their bare form is also allowed;
// the assignment form (PRAGMA x = ...) is not.
var pragmaAllowsArg = map[string]bool{
	"TABLE_INFO":        true,
	"TABLE_XINFO":       true,
	"TABLE_LIST":        true,
	"INDEX_LIST":        true,
	"INDEX_INFO":        true,
	"INDEX_XINFO":       true,
	"FOREIGN_KEY_LIST":  true,
	"FOREIGN_KEY_CHECK": true,
}

// pragmaReadOnlyScalar lists PRAGMAs allowed ONLY in the bare read/query form.
// Several of these (journal_mode, user_version, schema_version, page_size,
// max_page_count, application_id, encoding) also have a write form via
// "PRAGMA name = value" or "PRAGMA name(value)" — those are rejected here.
var pragmaReadOnlyScalar = map[string]bool{
	"DATABASE_LIST":   true,
	"COMPILE_OPTIONS": true,
	"COLLATION_LIST":  true,
	"JOURNAL_MODE":    true,
	"PAGE_COUNT":      true,
	"PAGE_SIZE":       true,
	"MAX_PAGE_COUNT":  true,
	"FREELIST_COUNT":  true,
	"ENCODING":        true,
	"DATA_VERSION":    true,
	"SCHEMA_VERSION":  true,
	"USER_VERSION":    true,
	"APPLICATION_ID":  true,
}

// validateReadOnlyPragma allows only read/query-form PRAGMAs. The previous
// HasPrefix allowlist let writes like "PRAGMA journal_mode=DELETE" or
// "PRAGMA user_version=99" slip through because they share a read PRAGMA's
// prefix; here the assignment ("=" / "(value)") form is rejected.
func validateReadOnlyPragma(upper string) error {
	rest := strings.TrimSpace(strings.TrimPrefix(upper, "PRAGMA"))
	name, after := scanPragmaName(rest)
	if name == "" {
		return fmt.Errorf("PRAGMA name required")
	}
	after = strings.TrimSpace(after)

	switch {
	case pragmaAllowsArg[name]:
		// Bare read form or "(arg)" read form; assignment form is rejected.
		if after == "" || strings.HasPrefix(after, "(") {
			return nil
		}
		return fmt.Errorf("PRAGMA %s: only the read/query form is allowed", name)
	case pragmaReadOnlyScalar[name]:
		// Bare read form only — reject "= value" and "(value)" write forms.
		if after == "" {
			return nil
		}
		return fmt.Errorf("PRAGMA %s: only the read/query form is allowed (assignment rejected)", name)
	default:
		return fmt.Errorf("PRAGMA not in read-only allowlist")
	}
}

// scanPragmaName extracts the leading pragma name from the text following
// "PRAGMA", tolerating an optional "schema." qualifier (e.g. main.user_version).
func scanPragmaName(s string) (name, rest string) {
	i := 0
	for i < len(s) && isIdentByte(s[i]) {
		i++
	}
	name, rest = s[:i], s[i:]
	if strings.HasPrefix(rest, ".") {
		return scanPragmaName(rest[1:])
	}
	return name, rest
}

// deniedExactFuncs are function names rejected when invoked as a call (the
// identifier, bounded on both sides, followed by "("). Postgres executes these
// even inside a read-only transaction, so "starts with SELECT" is not enough.
var deniedExactFuncs = []string{
	"PG_TERMINATE_BACKEND",
	"PG_CANCEL_BACKEND",
	"PG_RELOAD_CONF",
	"PG_ROTATE_LOGFILE",
	"PG_SWITCH_WAL",
	"PG_SWITCH_XLOG", // pre-v10 name
	"SETVAL",
	"NEXTVAL",
}

// deniedFuncPrefixes reject whole families of side-effecting / filesystem /
// external functions matched by identifier prefix: large objects (lo_*),
// dblink (dblink, dblink_exec, ...), stat resets (pg_stat_reset*), and file /
// directory access (pg_read_file, pg_read_binary_file, pg_ls_*).
var deniedFuncPrefixes = []string{
	"PG_STAT_RESET",
	"PG_READ_FILE",
	"PG_READ_BINARY_FILE",
	"PG_LS_",
	"LO_",
	"DBLINK",
}

// callsSideEffectingFunction returns the first denied function invoked by the
// (uppercased) statement, if any.
func callsSideEffectingFunction(upper string) (string, bool) {
	for _, fn := range deniedExactFuncs {
		if hasFunctionCall(upper, fn, false) {
			return fn, true
		}
	}
	for _, fn := range deniedFuncPrefixes {
		if hasFunctionCall(upper, fn, true) {
			return fn, true
		}
	}
	return "", false
}

// hasFunctionCall reports whether upper contains a call to a function whose
// identifier equals name (prefix=false) or starts with name (prefix=true). The
// match must be bounded on the left by a non-identifier byte and followed —
// after the rest of the identifier and optional whitespace — by "(".
func hasFunctionCall(upper, name string, prefix bool) bool {
	from := 0
	for {
		i := strings.Index(upper[from:], name)
		if i < 0 {
			return false
		}
		pos := from + i
		from = pos + len(name)

		// Left boundary: previous byte must not be an identifier byte.
		if pos > 0 && isIdentByte(upper[pos-1]) {
			continue
		}

		j := pos + len(name)
		if prefix {
			// Consume the rest of the identifier (the family suffix).
			for j < len(upper) && isIdentByte(upper[j]) {
				j++
			}
		} else if j < len(upper) && isIdentByte(upper[j]) {
			// Exact match: the identifier must end right here.
			continue
		}

		// Skip whitespace, then require "(".
		for j < len(upper) && isSpaceByte(upper[j]) {
			j++
		}
		if j < len(upper) && upper[j] == '(' {
			return true
		}
	}
}

func isIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9')
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// HasLimitGeneric checks whether a query contains a LIMIT clause using simple
// keyword matching. Works for MySQL, SQLite, and other SQL dialects.
func HasLimitGeneric(query string) bool {
	upper := strings.ToUpper(query)
	return strings.Contains(upper, " LIMIT ")
}
