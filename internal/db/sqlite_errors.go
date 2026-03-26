package db

import "strings"

// isUniqueConstraintError checks if the error is a SQLite UNIQUE constraint violation.
// It checks both the standard error message format and common variations.
func isUniqueConstraintError(err error, constraint string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// modernc.org/sqlite format
	if strings.Contains(msg, "UNIQUE constraint failed: "+constraint) {
		return true
	}
	// mattn/go-sqlite3 format
	if strings.Contains(msg, "UNIQUE constraint failed") && strings.Contains(msg, constraint) {
		return true
	}
	return false
}
