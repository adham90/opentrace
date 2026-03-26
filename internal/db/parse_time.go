package db

import (
	"log/slog"
	"time"
)

// parseTime parses an RFC3339 timestamp string. Since all timestamps in the
// database are written by this application in RFC3339 format, parse failures
// indicate data corruption. We log a warning and return the zero value rather
// than propagating errors through every scan function (60+ call sites).
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil && s != "" {
		slog.Warn("failed to parse timestamp from database",
			"value", s,
			"error", err,
		)
	}
	return t
}
