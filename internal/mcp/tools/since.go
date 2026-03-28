package tools

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseSince parses a human-friendly relative time string like "1h", "24h", "7d"
// and returns the absolute time (now - duration). Returns zero time if parsing fails.
//
// Supported formats:
//
//	"15m"  → 15 minutes ago
//	"1h"   → 1 hour ago
//	"6h"   → 6 hours ago
//	"24h"  → 24 hours ago
//	"7d"   → 7 days ago
//	"30d"  → 30 days ago
//
// Also accepts ISO 8601 timestamps as passthrough.
func ParseSince(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty since value")
	}

	s = strings.TrimSpace(s)

	// Try ISO 8601 passthrough first.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	// Parse relative duration: number + unit suffix.
	if len(s) < 2 {
		return time.Time{}, fmt.Errorf("invalid since format: %q", s)
	}

	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid since format: %q", s)
	}

	now := time.Now().UTC()
	switch unit {
	case 'm':
		return now.Add(-time.Duration(num) * time.Minute), nil
	case 'h':
		return now.Add(-time.Duration(num) * time.Hour), nil
	case 'd':
		return now.Add(-time.Duration(num) * 24 * time.Hour), nil
	default:
		return time.Time{}, fmt.Errorf("unknown unit %q in since %q (use m/h/d)", string(unit), s)
	}
}

// ParseSinceOr parses a since string, returning the fallback duration if parsing fails.
func ParseSinceOr(s string, fallback time.Duration) time.Time {
	if s == "" {
		return time.Now().UTC().Add(-fallback)
	}
	t, err := ParseSince(s)
	if err != nil {
		return time.Now().UTC().Add(-fallback)
	}
	return t
}

// GetSinceParam extracts and parses the "since" param from args, with a default.
func GetSinceParam(args map[string]any, defaultDuration time.Duration) time.Time {
	// Check "since" first, then "time_range" for backward compat.
	if v, ok := args["since"].(string); ok && v != "" {
		return ParseSinceOr(v, defaultDuration)
	}
	if v, ok := args["time_range"].(string); ok && v != "" {
		return ParseSinceOr(v, defaultDuration)
	}
	if v, ok := args["timeframe"].(string); ok && v != "" {
		return ParseSinceOr(v, defaultDuration)
	}
	return time.Now().UTC().Add(-defaultDuration)
}
