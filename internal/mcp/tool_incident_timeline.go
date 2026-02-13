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
	Time     string `json:"time"`
	Type     string `json:"type"` // "alert", "log", "metric"
	Severity string `json:"severity,omitempty"`
	Summary  string `json:"summary"`
	Source   string `json:"source,omitempty"`
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

		var events []timelineEvent

		// Fetch alerts in the time window.
		alerts, err := as.List(ctx, store.ListAlertParams{
			Limit: 200,
		})
		if err == nil {
			for _, a := range alerts {
				if a.CreatedAt.Before(start) || a.CreatedAt.After(end) {
					continue
				}
				events = append(events, timelineEvent{
					Time:     a.CreatedAt.Format(time.RFC3339),
					Type:     "alert",
					Severity: string(a.Severity),
					Summary:  a.Title + ": " + a.Summary,
					Source:   a.WatcherTitle,
				})
			}
		}

		// Fetch error/fatal logs in the time window.
		for _, level := range []string{"error", "fatal"} {
			logs, err := ls.Search(ctx, store.LogSearchParams{
				Level: level,
				Start: &start,
				End:   &end,
				Limit: 100,
			})
			if err != nil {
				continue
			}
			for _, l := range logs {
				events = append(events, timelineEvent{
					Time:     l.Timestamp.Format(time.RFC3339),
					Type:     "log",
					Severity: l.Level,
					Summary:  l.Message,
					Source:   l.Service,
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
		affectedServices := make(map[string]bool)
		severityCounts := make(map[string]int)
		for _, e := range events {
			if e.Type == "alert" {
				alertCount++
				severityCounts[e.Severity]++
			}
			if e.Type == "log" {
				logLevels[e.Severity]++
			}
			if e.Source != "" {
				affectedServices[e.Source] = true
			}
		}

		// Blast radius: how many services/watchers are affected.
		serviceList := make([]string, 0, len(affectedServices))
		for svc := range affectedServices {
			serviceList = append(serviceList, svc)
		}

		// Probable root cause: first event in the timeline.
		var rootCause map[string]any
		if len(events) > 0 {
			first := events[0]
			rootCause = map[string]any{
				"time":     first.Time,
				"type":     first.Type,
				"severity": first.Severity,
				"summary":  first.Summary,
				"source":   first.Source,
				"note":     "This is the earliest event in the window — may be the root cause or the first symptom.",
			}
		}

		resp := map[string]any{
			"period": map[string]string{
				"start": start.Format(time.RFC3339),
				"end":   end.Format(time.RFC3339),
			},
			"total_events":      len(events),
			"alert_count":       alertCount,
			"alert_by_severity": severityCounts,
			"log_levels":        logLevels,
			"blast_radius": map[string]any{
				"affected_services": serviceList,
				"service_count":     len(serviceList),
			},
			"timeline": events,
		}

		if rootCause != nil {
			resp["probable_root_cause"] = rootCause
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal timeline: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}
