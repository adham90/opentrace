package tools

import "strings"

// isCriticalPath reports whether any configured money-path pattern appears in
// any of the given fields.
//
// One flag covers a route, a service and a watch because the match is a plain
// case-insensitive substring over whatever identifies the item: "checkout"
// matches the /checkout route, the checkout service and a CheckoutError alike.
// That is deliberately loose — the cost of a false positive is one item sorted
// too high, the cost of a false negative is a billing outage ranked below a
// noisy health endpoint.
func isCriticalPath(patterns []string, fields ...string) bool {
	if len(patterns) == 0 {
		return false
	}
	for _, f := range fields {
		if f == "" {
			continue
		}
		lower := strings.ToLower(f)
		for _, p := range patterns {
			if strings.Contains(lower, strings.ToLower(p)) {
				return true
			}
		}
	}
	return false
}
