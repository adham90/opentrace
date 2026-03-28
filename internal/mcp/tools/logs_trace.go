package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"


	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// action: trace — distributed trace assembly (from traceLookupHandler)
// ---------------------------------------------------------------------------

func LogsTrace(ctx context.Context, args map[string]any, deps LogsDeps) (*CallToolResult, error) {
	traceID, _ := args["trace_id"].(string)
	if traceID == "" {
		return NewToolResultError("trace_id is required"), nil
	}

	includeContext := false
	if v, ok := args["include_context"].(bool); ok {
		includeContext = v
	}

	// Fetch all log entries for this trace.
	entries, err := deps.LogStore.Search(ctx, store.LogSearchParams{
		TraceID: traceID,
		Limit:   1000,
	})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to search logs: %v", err)), nil
	}

	// Sort by timestamp ascending.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	if len(entries) == 0 {
		resp := map[string]any{
			"trace_id":      traceID,
			"total_entries": 0,
			"message":       "No log entries found for this trace ID",
		}
		data, _ := json.Marshal(resp)
		return NewToolResultText(string(data)), nil
	}

	firstTime := entries[0].Timestamp
	var lastTime time.Time
	if len(entries) > 0 {
		lastTime = entries[len(entries)-1].Timestamp
	}

	// Build timeline.
	var timeline []map[string]any
	servicesSet := map[string]bool{}
	hasErrors := false

	for _, e := range entries {
		elapsed := e.Timestamp.Sub(firstTime).Milliseconds()
		entry := map[string]any{
			"timestamp":  e.Timestamp.Format(time.RFC3339Nano),
			"service":    e.Service,
			"level":      e.Level,
			"message":    e.Message,
			"elapsed_ms": elapsed,
		}
		timeline = append(timeline, entry)
		if e.Service != "" {
			servicesSet[e.Service] = true
		}
		if e.Level == "error" || e.Level == "fatal" {
			hasErrors = true
		}
	}

	// Build service summary.
	type svcStats struct {
		entries   int
		errors    int
		firstTime time.Time
		lastTime  time.Time
	}
	svcMap := map[string]*svcStats{}
	for _, e := range entries {
		svc := e.Service
		if svc == "" {
			svc = "(unknown)"
		}
		s, ok := svcMap[svc]
		if !ok {
			s = &svcStats{firstTime: e.Timestamp, lastTime: e.Timestamp}
			svcMap[svc] = s
		}
		s.entries++
		if e.Timestamp.Before(s.firstTime) {
			s.firstTime = e.Timestamp
		}
		if e.Timestamp.After(s.lastTime) {
			s.lastTime = e.Timestamp
		}
		if e.Level == "error" || e.Level == "fatal" {
			s.errors++
		}
	}

	var serviceSummary []map[string]any
	var servicesList []string
	for svc := range servicesSet {
		servicesList = append(servicesList, svc)
	}
	sort.Strings(servicesList)

	for _, svc := range servicesList {
		s := svcMap[svc]
		timeSpent := s.lastTime.Sub(s.firstTime).Milliseconds()
		serviceSummary = append(serviceSummary, map[string]any{
			"service":       svc,
			"entries":       s.entries,
			"errors":        s.errors,
			"time_spent_ms": timeSpent,
		})
	}

	// Detect warnings.
	var warnings []string

	// Find first error as root cause.
	for _, e := range entries {
		if e.Level == "error" || e.Level == "fatal" {
			elapsed := e.Timestamp.Sub(firstTime).Milliseconds()
			warnings = append(warnings, fmt.Sprintf("Error in %s at +%dms — this is likely the root cause", e.Service, elapsed))
			break
		}
	}

	// Gap detection: flag gaps >500ms between consecutive entries.
	for i := 1; i < len(entries); i++ {
		gap := entries[i].Timestamp.Sub(entries[i-1].Timestamp)
		if gap > 500*time.Millisecond {
			prevSvc := entries[i-1].Service
			currSvc := entries[i].Service
			if prevSvc != currSvc {
				warnings = append(warnings, fmt.Sprintf("Gap of %dms between %s→%s — possible network or queue delay", gap.Milliseconds(), prevSvc, currSvc))
			}
		}
	}

	totalDuration := lastTime.Sub(firstTime).Milliseconds()

	resp := map[string]any{
		"trace_id":          traceID,
		"total_entries":     len(entries),
		"total_duration_ms": totalDuration,
		"services_touched":  servicesList,
		"has_errors":        hasErrors,
		"timeline":          timeline,
		"service_summary":   serviceSummary,
	}

	// Context entries (surrounding logs from each service).
	if includeContext && len(entries) > 0 {
		var contextEntries []map[string]any
		for svc, stats := range svcMap {
			ctxStart := stats.firstTime.Add(-2 * time.Second)
			ctxEnd := stats.lastTime.Add(2 * time.Second)
			ctxLogs, err := deps.LogStore.Search(ctx, store.LogSearchParams{
				Service: svc,
				Start:   &ctxStart,
				End:     &ctxEnd,
				Limit:   50,
			})
			if err != nil {
				continue
			}
			for _, cl := range ctxLogs {
				if cl.TraceID == traceID {
					continue // skip entries already in the trace
				}
				contextEntries = append(contextEntries, map[string]any{
					"timestamp": cl.Timestamp.Format(time.RFC3339Nano),
					"service":   cl.Service,
					"level":     cl.Level,
					"message":   cl.Message,
				})
			}
		}
		resp["context_entries"] = contextEntries
	}

	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}

	// Record trace ID in session if callback is provided.
	if deps.TraceSessionRecorder != nil {
		deps.TraceSessionRecorder(traceID)
	}

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}
