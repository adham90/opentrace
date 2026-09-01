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
	return compileSamplingRules(rules).apply(entries)
}

type compiledSamplingRules struct {
	byService   map[string]store.SamplingRule
	defaultRule *store.SamplingRule
}

func compileSamplingRules(rules []store.SamplingRule) *compiledSamplingRules {
	compiled := &compiledSamplingRules{byService: make(map[string]store.SamplingRule, len(rules))}
	for i := range rules {
		if rules[i].Service == "*" {
			rule := rules[i]
			compiled.defaultRule = &rule
		} else {
			compiled.byService[rules[i].Service] = rules[i]
		}
	}
	return compiled
}

func (r *compiledSamplingRules) apply(entries []store.LogEntry) []store.LogEntry {
	if r == nil || (r.defaultRule == nil && len(r.byService) == 0) {
		return entries
	}

	filtered := make([]store.LogEntry, 0, len(entries))
	for _, e := range entries {
		rule, ok := r.byService[e.Service]
		if !ok && r.defaultRule != nil {
			rule = *r.defaultRule
			ok = true
		}
		if !ok {
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
