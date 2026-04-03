package deepcapture

import (
	"database/sql"
	"encoding/json"
)

// nullableStr returns a sql.NullString that is NULL when s is empty.
func nullableStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// nullableInt returns a sql.NullInt64 that is NULL when v is zero.
func nullableInt(v int) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(v), Valid: true}
}

// nullableJSON returns a sql.NullString containing the JSON text,
// or NULL when the raw message is nil or empty.
func nullableJSON(raw json.RawMessage) sql.NullString {
	if len(raw) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: string(raw), Valid: true}
}
