package sqlite

import (
	"database/sql"
	"time"
)

// rfc3339 renders t in the canonical on-disk timestamp format. Every TEXT
// timestamp column in this schema stores RFC3339 UTC, and every range
// comparison and prune in this package compares those columns
// lexicographically against an RFC3339 cutoff string.
//
// Timestamps must therefore never be handed to bun as a time.Time: bun's
// SQLite dialect renders time values as "2006-01-02 15:04:05.999999-07:00",
// which sorts *below* the RFC3339 rendering of the same instant because
// ' ' (0x20) < 'T' (0x54). A row written that way looks older than any cutoff
// sharing its UTC date, which silently breaks staleness sweeps, expiry checks
// and retention prunes. Format with this helper instead.
func rfc3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

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
