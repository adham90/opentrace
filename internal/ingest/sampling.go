package ingest

import (
	"math/rand"
	"strings"

	"github.com/adham90/opentrace/pkg/store"
)

// ApplySamplingRules filters log entries according to the given sampling rules.
// Entries whose service matches a rule are randomly kept based on the rule's rate.
// Error/warn/fatal logs are always kept when KeepErrors is true for the matching rule.
func ApplySamplingRules(entries []store.LogEntry, rules []store.SamplingRule) []store.LogEntry {
	// Build a map of service -> rule for fast lookup
	ruleMap := make(map[string]*store.SamplingRule, len(rules))
	var defaultRule *store.SamplingRule
	for i := range rules {
		if rules[i].Service == "*" {
			defaultRule = &rules[i]
		} else {
			ruleMap[rules[i].Service] = &rules[i]
		}
	}

	if defaultRule == nil && len(ruleMap) == 0 {
		return entries
	}

	filtered := make([]store.LogEntry, 0, len(entries))
	for _, e := range entries {
		rule := ruleMap[e.Service]
		if rule == nil {
			rule = defaultRule
		}
		if rule == nil {
			// No rule for this service, keep all
			filtered = append(filtered, e)
			continue
		}

		// Always keep error/warn/fatal if KeepErrors is set
		if rule.KeepErrors && IsErrorLevel(e.Level) {
			filtered = append(filtered, e)
			continue
		}

		// Sample based on rate
		if rule.Rate >= 1.0 || rand.Float64() < rule.Rate {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// IsErrorLevel returns true if the log level indicates an error, warning, or fatal.
func IsErrorLevel(level string) bool {
	switch strings.ToLower(level) {
	case "error", "warn", "warning", "fatal":
		return true
	default:
		return false
	}
}
