package mcp

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/store"
)

// monitorSummary is a lightweight representation of an existing monitor
// included in introspection tool responses to help the AI avoid duplicates.
type monitorSummary struct {
	Title       string `json:"title"`
	MonitorType string `json:"monitor_type"`
	Status      string `json:"status"`
	Source      string `json:"source,omitempty"`
	Query       string `json:"query,omitempty"`
}

// fetchExistingMonitors returns a slice of monitor summaries from the WatcherStore.
// Returns nil if the store is nil or an error occurs.
func fetchExistingMonitors(ctx context.Context, ws store.WatcherStore) []monitorSummary {
	if ws == nil {
		return nil
	}

	watchers, err := ws.List(ctx, store.ListWatcherParams{})
	if err != nil || len(watchers) == 0 {
		return nil
	}

	summaries := make([]monitorSummary, 0, len(watchers))
	for _, w := range watchers {
		s := monitorSummary{
			Title:       w.Title,
			MonitorType: string(w.MonitorType),
			Status:      string(w.Status),
		}
		if w.RuleConfig != nil {
			s.Source = string(w.RuleConfig.Source)
			if w.RuleConfig.Query != "" {
				q := w.RuleConfig.Query
				if len(q) > 100 {
					q = q[:100] + "..."
				}
				s.Query = q
			}
		}
		summaries = append(summaries, s)
	}
	return summaries
}

// appendExistingMonitors adds an "existing_monitors" field to the response map
// if there are any monitors to include.
func appendExistingMonitors(resp map[string]any, monitors []monitorSummary) {
	if len(monitors) == 0 {
		return
	}
	resp["existing_monitors"] = monitors
	resp["existing_monitors_hint"] = fmt.Sprintf("There are %d existing monitors. Check for overlap before suggesting new ones.", len(monitors))
}
