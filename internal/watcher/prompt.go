package watcher

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// WatcherFilters represents the parsed filter configuration for a watcher.
type WatcherFilters struct {
	Service     string `json:"service,omitempty"`
	Level       string `json:"level,omitempty"`
	Environment string `json:"environment,omitempty"`
	Query       string `json:"query,omitempty"`
}

// ParseTimeRange parses a time range string like "5m", "15m", "1h", "6h", "24h"
// into a time.Duration. Defaults to 15 minutes for unrecognized values.
func ParseTimeRange(s string) time.Duration {
	if s == "" {
		return 15 * time.Minute
	}

	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 15 * time.Minute
	}

	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	num, err := strconv.Atoi(numStr)
	if err != nil || num <= 0 {
		return 15 * time.Minute
	}

	switch unit {
	case 'm':
		return time.Duration(num) * time.Minute
	case 'h':
		return time.Duration(num) * time.Hour
	case 's':
		return time.Duration(num) * time.Second
	default:
		return 15 * time.Minute
	}
}

// EffortConfig holds the agent loop limits for a given effort level.
type EffortConfig struct {
	MaxSteps     int
	MaxToolCalls int
}

// EffortSettings returns the agent loop limits for the given effort level.
func EffortSettings(effort store.WatcherEffort) EffortConfig {
	switch effort {
	case store.EffortLow:
		return EffortConfig{MaxSteps: 3, MaxToolCalls: 2}
	case store.EffortHigh:
		return EffortConfig{MaxSteps: 15, MaxToolCalls: 12}
	default: // medium
		return EffortConfig{MaxSteps: 8, MaxToolCalls: 6}
	}
}

// BuildQuery translates a watcher definition into an agent query string.
// It includes the watcher's instructions, filters, and optionally the previous run's summary.
// If hasServers is true, the prompt mentions that server metric tools are available.
func BuildQuery(w store.Watcher, lastRunSummary string, hasServers ...bool) string {
	var b strings.Builder

	b.WriteString("You are a monitoring agent. Your job is to evaluate a specific condition and determine if it warrants an alert.\n\n")

	b.WriteString(fmt.Sprintf("## Watcher: %s\n", w.Title))
	b.WriteString(w.Description)
	b.WriteString("\n\n")

	// Parse and render filters
	filters := parseFilters(w.Filters)
	hasScope := hasFilters(filters) || w.TimeRange != ""
	if hasScope {
		b.WriteString("## Search Scope\n")
		if filters.Service != "" {
			b.WriteString(fmt.Sprintf("- Service: %s\n", filters.Service))
		}
		if filters.Level != "" {
			b.WriteString(fmt.Sprintf("- Level: %s\n", filters.Level))
		}
		if filters.Environment != "" {
			b.WriteString(fmt.Sprintf("- Environment: %s\n", filters.Environment))
		}
		if w.TimeRange != "" {
			b.WriteString(fmt.Sprintf("- Time range: last %s\n", w.TimeRange))
		}
		if filters.Query != "" {
			b.WriteString(fmt.Sprintf("- Query: %s\n", filters.Query))
		}
		b.WriteString("\n")
	}

	// Include previous run context if available
	if lastRunSummary != "" {
		b.WriteString("## Previous Run\n")
		b.WriteString(lastRunSummary)
		b.WriteString("\n\n")
	}

	// Mention server metrics if servers are being monitored
	if len(hasServers) > 0 && hasServers[0] {
		b.WriteString("## Available Server Metrics\n")
		b.WriteString("Monitored servers are available. You can use these tools to correlate log issues with infrastructure health:\n")
		b.WriteString("- `list_servers` — see all monitored servers and their status\n")
		b.WriteString("- `query_server_metrics` — query CPU, memory, disk, network, load metrics by server\n")
		b.WriteString("- `server_health_summary` — get a quick health snapshot of a server\n")
		b.WriteString("Consider checking server health when you find log anomalies — high CPU, memory pressure, or disk saturation can explain application errors.\n\n")
	}

	// Adjust instructions based on effort level
	effort := w.Effort
	if effort == "" {
		effort = store.EffortMedium
	}

	switch effort {
	case store.EffortLow:
		b.WriteString(`## Instructions
1. Use log_search with the filters above to find matching logs.
2. Quickly assess the results — focus on obvious anomalies or threshold breaches.
3. Respond with your assessment:
   - Start with "ALERT:" if the condition is met and action is needed.
   - Start with "OK:" if everything looks normal.
   Keep your summary brief (1-2 sentences).`)

	case store.EffortHigh:
		b.WriteString("## Instructions\n")
		b.WriteString("1. Use log_search with the filters above to find matching logs.\n")
		b.WriteString("2. Analyze the results thoroughly for anomalies, spikes, or concerning patterns.\n")
		if lastRunSummary != "" {
			b.WriteString("3. Compare your findings to the previous run — note any changes, trends, or escalation.\n")
			b.WriteString("4. Use memory_read to check for known patterns, memory_write to save new ones.\n")
			b.WriteString("5. Cross-reference across services or time ranges if relevant — look for correlations.\n")
			b.WriteString("6. Provide a detailed assessment:\n")
		} else {
			b.WriteString("3. Use memory_read to check for known patterns, memory_write to save new ones.\n")
			b.WriteString("4. Cross-reference across services or time ranges if relevant — look for correlations.\n")
			b.WriteString("5. Provide a detailed assessment:\n")
		}
		b.WriteString(`   - Start with "ALERT:" if the condition is met and action is needed.
   - Start with "OK:" if everything looks normal.
   Include a detailed summary with root cause analysis where possible.`)

	default: // medium
		b.WriteString(`## Instructions
1. Use log_search with the filters above to find matching logs.
2. Analyze the results for anomalies, spikes, or concerning patterns.
`)
		if lastRunSummary != "" {
			b.WriteString("3. Compare your findings to the previous run — note any changes or trends.\n")
			b.WriteString("4. Use memory_read to check for known patterns, memory_write to save new ones.\n")
			b.WriteString("5. Respond with your assessment:\n")
		} else {
			b.WriteString("3. Use memory_read to check for known patterns, memory_write to save new ones.\n")
			b.WriteString("4. Respond with your assessment:\n")
		}
		b.WriteString(`   - Start with "ALERT:" if the condition is met and action is needed.
   - Start with "OK:" if everything looks normal.
   Include a brief summary of what you found.`)
	}

	return b.String()
}

// EvaluateFindings checks if the agent's response indicates an alert should fire.
func EvaluateFindings(answer string) bool {
	upper := strings.ToUpper(strings.TrimSpace(answer))
	return strings.HasPrefix(upper, "ALERT:")
}

func parseFilters(raw json.RawMessage) WatcherFilters {
	var f WatcherFilters
	if len(raw) > 0 {
		json.Unmarshal(raw, &f)
	}
	return f
}

func hasFilters(f WatcherFilters) bool {
	return f.Service != "" || f.Level != "" || f.Environment != "" || f.Query != ""
}
