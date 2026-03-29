package sqlite

import "database/sql"

// boolToInt64 converts a Go bool to SQLite INTEGER (0/1).
func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// nullString wraps a string in sql.NullString (valid if non-empty).
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
