package tools

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// --- timeline action ---

type timelineEvent struct {
	Time     string `json:"time"`
	Type     string `json:"type"`
	Severity string `json:"severity,omitempty"`
	Summary  string `json:"summary"`
	Source   string `json:"source,omitempty"`
	ID       string `json:"id,omitempty"`
}

func HandleTimeline(ctx context.Context, d OverviewDeps, args map[string]any) (*CallToolResult, error) {
	startStr := ArgString(args, "start")
	if startStr == "" {
		return NewToolResultError("start is required (ISO 8601 format)"), nil
	}
	endStr := ArgString(args, "end")
	if endStr == "" {
		return NewToolResultError("end is required (ISO 8601 format)"), nil
	}

	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid start time: %v", err)), nil
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid end time: %v", err)), nil
	}

	if end.Before(start) {
		return NewToolResultError("end must be after start"), nil
	}

	serviceFilter := ArgString(args, "service")

	var events []timelineEvent

	// 1. Error/fatal log entries.
	if d.LogStore != nil {
		for _, level := range []string{"error", "fatal"} {
			params := store.LogSearchParams{
				Level:   level,
				Service: serviceFilter,
				Start:   &start,
				End:     &end,
				Limit:   200,
				SortAsc: true,
			}
			logs, err := d.LogStore.Search(ctx, params)
			if err != nil {
				continue
			}
			for _, l := range logs {
				msg := l.Message
				if len(msg) > 150 {
					msg = msg[:150] + "..."
				}
				ev := timelineEvent{
					Time:     l.Timestamp.Format(time.RFC3339),
					Type:     "error",
					Severity: l.Level,
					Summary:  msg,
					Source:   l.Service,
				}
				if l.ErrorFingerprint != "" {
					ev.ID = l.ErrorFingerprint
				}
				events = append(events, ev)
			}
		}

		// Deploy events: detect distinct commit hashes appearing in the window.
		deployParams := store.LogSearchParams{
			Service: serviceFilter,
			Start:   &start,
			End:     &end,
			Limit:   500,
			SortAsc: true,
		}
		allLogs, err := d.LogStore.Search(ctx, deployParams)
		if err == nil {
			commitFirstSeen := make(map[string]time.Time)
			for _, l := range allLogs {
				if l.CommitHash == "" {
					continue
				}
				if _, seen := commitFirstSeen[l.CommitHash]; !seen {
					commitFirstSeen[l.CommitHash] = l.Timestamp
				}
			}
			for hash, ts := range commitFirstSeen {
				short := hash
				if len(short) > 7 {
					short = short[:7]
				}
				events = append(events, timelineEvent{
					Time:    ts.Format(time.RFC3339),
					Type:    "deploy",
					Summary: fmt.Sprintf("Commit %s first seen", short),
					ID:      hash,
				})
			}
		}
	}

	// 2. Error group lifecycle events (resolved, ignored, reopened).
	if d.ErrorGroupStore != nil {
		groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
			Service: serviceFilter,
			Limit:   50,
		})
		if err == nil {
			for _, eg := range groups {
				egEvents, err := d.ErrorGroupStore.ListEvents(ctx, eg.Fingerprint, 20)
				if err != nil {
					continue
				}
				for _, ev := range egEvents {
					if ev.CreatedAt.Before(start) || ev.CreatedAt.After(end) {
						continue
					}
					summary := fmt.Sprintf("%s: %s", eg.ExceptionClass, eg.Message)
					if len(summary) > 120 {
						summary = summary[:120] + "..."
					}
					if ev.Reason != "" {
						summary += " — " + ev.Reason
					}
					events = append(events, timelineEvent{
						Time:    ev.CreatedAt.Format(time.RFC3339),
						Type:    ev.Action,
						Summary: summary,
						Source:  eg.Service,
						ID:      eg.Fingerprint,
					})
				}
			}
		}
	}

	// 3. Watch alerts.
	if d.WatchStore != nil {
		alerts, err := d.WatchStore.ListAlerts(ctx, "", "", 50)
		if err == nil {
			for _, a := range alerts {
				if a.CreatedAt.Before(start) || a.CreatedAt.After(end) {
					continue
				}
				events = append(events, timelineEvent{
					Time:     a.CreatedAt.Format(time.RFC3339),
					Type:     "watch",
					Severity: string(a.Urgency),
					Summary:  a.Summary,
					ID:       a.WatchID,
				})
			}
		}
	}

	// 4. Healthcheck status changes.
	if d.HealthCheckStore != nil {
		checks, err := d.HealthCheckStore.List(ctx, store.ListHealthCheckParams{})
		if err == nil {
			for _, hc := range checks {
				results, err := d.HealthCheckStore.LatestResults(ctx, hc.ID, 50)
				if err != nil {
					continue
				}
				var prev store.HealthCheckStatus
				for i := len(results) - 1; i >= 0; i-- {
					r := results[i]
					if r.CheckedAt.Before(start) || r.CheckedAt.After(end) {
						if r.CheckedAt.Before(start) {
							prev = r.Status
						}
						continue
					}
					if prev != "" && r.Status != prev {
						var sev string
						switch r.Status {
						case store.HealthCheckDown:
							sev = "critical"
						case store.HealthCheckDegraded:
							sev = "warning"
						default:
							sev = "info"
						}
						events = append(events, timelineEvent{
							Time:     r.CheckedAt.Format(time.RFC3339),
							Type:     "healthcheck",
							Severity: sev,
							Summary:  fmt.Sprintf("%s went %s (was %s)", hc.Name, r.Status, prev),
							Source:   hc.URL,
							ID:       hc.ID,
						})
					}
					prev = r.Status
				}
			}
		}
	}

	// Sort by time.
	sort.Slice(events, func(i, j int) bool {
		return events[i].Time < events[j].Time
	})

	if len(events) > 200 {
		events = events[:200]
	}

	// Build summary stats.
	typeCounts := make(map[string]int)
	affectedServices := make(map[string]bool)
	for _, e := range events {
		typeCounts[e.Type]++
		if e.Source != "" {
			affectedServices[e.Source] = true
		}
	}

	serviceList := make([]string, 0, len(affectedServices))
	for svc := range affectedServices {
		serviceList = append(serviceList, svc)
	}

	var rootCause map[string]any
	if len(events) > 0 {
		first := events[0]
		rootCause = map[string]any{
			"time":    first.Time,
			"type":    first.Type,
			"summary": first.Summary,
			"source":  first.Source,
			"note":    "Earliest event in the window — may be the root cause or first symptom.",
		}
		if first.Severity != "" {
			rootCause["severity"] = first.Severity
		}
	}

	resp := map[string]any{
		"period": map[string]string{
			"start": start.Format(time.RFC3339),
			"end":   end.Format(time.RFC3339),
		},
		"total_events": len(events),
		"event_types":  typeCounts,
		"blast_radius": map[string]any{
			"affected_services": serviceList,
			"service_count":     len(serviceList),
		},
		"timeline": events,
	}

	if rootCause != nil {
		resp["probable_root_cause"] = rootCause
	}

	var suggestions []ToolSuggestion
	if len(events) > 0 {
		suggestions = append(suggestions, Suggest("overview", "Deep dive into root cause", map[string]any{"action": "diagnose"}))
	}
	if typeCounts["error"] > 5 {
		suggestions = append(suggestions, Suggest("errors", "Review aggregated errors", map[string]any{"action": "list", "status": "unresolved"}))
	}
	return JSONResult(resp, suggestions...)
}
