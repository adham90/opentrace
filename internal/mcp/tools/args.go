package tools

import "time"

// ---------------------------------------------------------------------------
// Typed argument extraction helpers
//
// Every MCP tool receives args as map[string]any. JSON numbers arrive as
// float64, booleans as bool, everything else as string. These helpers
// eliminate the repetitive 3-line type-assertion-with-default pattern that
// appears 200+ times across handler files.
// ---------------------------------------------------------------------------

// ArgString extracts a string argument. Returns "" if missing or wrong type.
func ArgString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// ArgStringDefault extracts a string argument with a default.
func ArgStringDefault(args map[string]any, key, def string) string {
	v, ok := args[key].(string)
	if !ok || v == "" {
		return def
	}
	return v
}

// ArgInt extracts an integer argument (JSON numbers arrive as float64).
// Returns def if missing, zero, or negative. Caps at max.
func ArgInt(args map[string]any, key string, def, max int) int {
	v, ok := args[key].(float64)
	if !ok || v <= 0 {
		return def
	}
	n := int(v)
	if n > max {
		return max
	}
	return n
}

// ArgFloat extracts a float64 argument. Returns def if missing.
func ArgFloat(args map[string]any, key string, def float64) float64 {
	v, ok := args[key].(float64)
	if !ok {
		return def
	}
	return v
}

// ArgBool extracts a boolean argument. Returns false if missing or wrong type.
func ArgBool(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

// ArgStringSlice extracts a []string from an args value that may be either
// a []any (from JSON arrays) or a []string.
func ArgStringSlice(args map[string]any, key string) []string {
	switch v := args[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// ArgDuration extracts a duration string like "1h", "30m", "7d" and parses it.
// Falls back to def if missing or unparseable. Supports Go duration strings
// plus "Nd" for days.
func ArgDuration(args map[string]any, key string, def time.Duration) time.Duration {
	s, ok := args[key].(string)
	if !ok || s == "" {
		return def
	}
	// Try Go's standard parser first (handles "1h", "30m", "1h30m", etc.)
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	// Handle "Nd" day syntax.
	if len(s) > 1 && s[len(s)-1] == 'd' {
		var n int
		for _, c := range s[:len(s)-1] {
			if c < '0' || c > '9' {
				return def
			}
			n = n*10 + int(c-'0')
		}
		return time.Duration(n) * 24 * time.Hour
	}
	return def
}
