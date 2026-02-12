package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/store"
)

// timelineEvent represents a single event in the incident timeline.
type timelineEvent struct {
	Time        string `json:"time"`
	Type        string `json:"type"` // "alert", "log", "metric"
	Severity    string `json:"severity,omitempty"`
	Summary     string `json:"summary"`
	Source      string `json:"source,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// incidentTimelineHandler returns a handler that builds a chronological incident timeline.
func incidentTimelineHandler(as store.AlertStore, ls store.LogStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		startStr, _ := args["start"].(string)
		if startStr == "" {
			return mcp.NewToolResultError("start is required (ISO 8601 format)"), nil
		}
		endStr, _ := args["end"].(string)
		if endStr == "" {
			return mcp.NewToolResultError("end is required (ISO 8601 format)"), nil
		}

		start, err := time.Parse(time.RFC3339, startStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid start time: %v", err)), nil
		}
		end, err := time.Parse(time.RFC3339, endStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid end time: %v", err)), nil
		}

		if end.Before(start) {
			return mcp.NewToolResultError("end must be after start"), nil
		}

		environment, _ := args["environment"].(string)

		var events []timelineEvent

		// Fetch alerts in the time window.
		alerts, err := as.List(ctx, store.ListAlertParams{
			Environment: environment,
			Limit:       200,
		})
		if err == nil {
			for _, a := range alerts {
				if a.CreatedAt.Before(start) || a.CreatedAt.After(end) {
					continue
				}
				events = append(events, timelineEvent{
					Time:        a.CreatedAt.Format(time.RFC3339),
					Type:        "alert",
					Severity:    string(a.Severity),
					Summary:     a.Title + ": " + a.Summary,
					Source:      a.WatcherTitle,
					Environment: a.Environment,
				})
			}
		}

		// Fetch error/fatal logs in the time window.
		for _, level := range []string{"error", "fatal"} {
			logs, err := ls.Search(ctx, store.LogSearchParams{
				Level:       level,
				Environment: environment,
				Start:       &start,
				End:         &end,
				Limit:       100,
			})
			if err != nil {
				continue
			}
			for _, l := range logs {
				events = append(events, timelineEvent{
					Time:        l.Timestamp.Format(time.RFC3339),
					Type:        "log",
					Severity:    l.Level,
					Summary:     l.Message,
					Source:      l.Service,
					Environment: l.Environment,
				})
			}
		}

		// Sort by time.
		sort.Slice(events, func(i, j int) bool {
			return events[i].Time < events[j].Time
		})

		// Build summary stats.
		alertCount := 0
		logLevels := make(map[string]int)
		for _, e := range events {
			if e.Type == "alert" {
				alertCount++
			}
			if e.Type == "log" {
				logLevels[e.Severity]++
			}
		}

		resp := map[string]any{
			"period": map[string]string{
				"start": start.Format(time.RFC3339),
				"end":   end.Format(time.RFC3339),
			},
			"total_events": len(events),
			"alert_count":  alertCount,
			"log_levels":   logLevels,
			"timeline":     events,
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal timeline: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}
