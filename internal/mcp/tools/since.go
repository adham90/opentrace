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

// GetSinceParam extracts and parses the time-range parameter from MCP tool args,
// returning the computed absolute time or falling back to now - defaultDuration.
//
// For backward compatibility with existing MCP clients, three parameter names
// are accepted in the following priority order:
//
//  1. "since"      — the canonical/preferred name for new tools
//  2. "time_range" — used by logs_search, logs_stats, logs_summary, logs_context, logs_performance
//  3. "timeframe"  — used by overview_diagnose
//
// When writing new tools, always use "since" as the parameter name and call this
// helper rather than reading the argument directly. Do NOT rename the legacy
// parameter names — that would break MCP clients that already use them.
func GetSinceParam(args map[string]any, defaultDuration time.Duration) time.Time {
	if t, ok := OptionalSinceParam(args); ok {
		return t
	}
	return time.Now().UTC().Add(-defaultDuration)
}

// OptionalSinceParam reports whether the caller supplied a time window at all,
// alongside the parsed value. Tools that are unbounded by default — listings
// that should return everything unless asked otherwise — need to tell "no
// window given" apart from "a window that happens to equal the default", which
// GetSinceParam cannot express.
//
// Accepts the same three names in the same priority order as GetSinceParam:
// "since" > "time_range" > "timeframe". An unparseable value counts as absent.
func OptionalSinceParam(args map[string]any) (time.Time, bool) {
	for _, key := range []string{"since", "time_range", "timeframe"} {
		v, ok := args[key].(string)
		if !ok || v == "" {
			continue
		}
		t, err := ParseSince(v)
		if err != nil {
			continue
		}
		return t, true
	}
	return time.Time{}, false
}

// SinceLabel renders a window's start for a response body, or "all time" when
// no window was applied.
func SinceLabel(t time.Time, ok bool) string {
	if !ok {
		return "all time"
	}
	return t.Format(time.RFC3339)
}
