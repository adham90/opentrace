package mcp

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/store"
)

// watcherSummary is a lightweight representation of an existing watcher
// included in introspection tool responses to help the AI avoid duplicates.
type watcherSummary struct {
	Title       string `json:"title"`
	WatcherType string `json:"watcher_type"`
	Status      string `json:"status"`
	Source      string `json:"source,omitempty"`
	Query       string `json:"query,omitempty"`
}

// fetchExistingWatchers returns a slice of watcher summaries from the WatcherStore.
// Returns nil if the store is nil or an error occurs.
func fetchExistingWatchers(ctx context.Context, ws store.WatcherStore) []watcherSummary {
	if ws == nil {
		return nil
	}

	watchers, err := ws.List(ctx, store.ListWatcherParams{})
	if err != nil || len(watchers) == 0 {
		return nil
	}

	summaries := make([]watcherSummary, 0, len(watchers))
	for _, w := range watchers {
		s := watcherSummary{
			Title:       w.Title,
			WatcherType: string(w.WatcherType),
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

// appendExistingWatchers adds an "existing_watchers" field to the response map
// if there are any watchers to include.
func appendExistingWatchers(resp map[string]any, watchers []watcherSummary) {
	if len(watchers) == 0 {
		return
	}
	resp["existing_watchers"] = watchers
	resp["existing_watchers_hint"] = fmt.Sprintf("There are %d existing watchers. Check for overlap before suggesting new ones.", len(watchers))
}
