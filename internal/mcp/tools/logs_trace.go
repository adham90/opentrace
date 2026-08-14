package tools

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// action: trace — distributed trace assembly (from traceLookupHandler)
// ---------------------------------------------------------------------------

const (
	// defaultTraceWindow is how far back a trace lookup reaches when the caller
	// names no window. Traces are looked up by exact ID, so the window exists
	// only to bound the scan.
	defaultTraceWindow = "30d"

	// maxTraceEntries matches the store's own per-search ceiling. Asking for
	// more does not raise it, it just hides the truncation.
	maxTraceEntries = 500
)

func LogsTrace(ctx context.Context, args map[string]any, deps LogsDeps) (*CallToolResult, error) {
	InitLogsDeps(&deps)
	traceID := ArgString(args, "trace_id")
	if traceID == "" {
		return NewToolResultError("trace_id is required"), nil
	}

	includeContext := ArgBool(args, "include_context")

	// Resolve env scope so a trace assembled from another environment's logs
	// cannot be read by a token scoped elsewhere.
	environment, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	// Window. A trace ID is an exact identifier, not a time query, so the
	// natural window is "as far back as we keep logs" — but the store defaults
	// Start to now-1h when it is nil, which made every trace older than an hour
	// report "no log entries found for this trace ID" as if it never happened.
	// Derive the window explicitly and echo it back.
	since, _, err := ResolveWindow(args, defaultTraceWindow)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}
	now := time.Now().UTC()

	// Fetch all log entries for this trace.
	traceResult, err := deps.Logs.Search(ctx, store.LogSearchParams{
		TraceID:     traceID,
		Environment: environment,
		Start:       &since,
		End:         &now,
		Limit:       maxTraceEntries,
		SortAsc:     true,
	})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to search logs: %v", err)), nil
	}
	entries := traceResult.Entries
	truncated := len(entries) >= maxTraceEntries

	// Sort by timestamp ascending.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	if len(entries) == 0 {
		return JSONResult(map[string]any{
			"trace_id":       traceID,
			"total_entries":  0,
			"searched_since": since.Format(time.RFC3339),
			"searched_until": now.Format(time.RFC3339),
			"message": fmt.Sprintf(
				"No log entries found for this trace ID in the window searched (%s to now). Pass since (e.g. since=\"30d\") to look further back.",
				since.Format(time.RFC3339)),
		})
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

	if truncated {
		warnings = append(warnings, fmt.Sprintf(
			"Trace truncated to the %d oldest entries the store will return — total_duration_ms and service_summary describe that prefix, not the whole trace.",
			maxTraceEntries))
	}

	resp := map[string]any{
		"trace_id":          traceID,
		"total_entries":     len(entries),
		"total_duration_ms": totalDuration,
		"services_touched":  servicesList,
		"has_errors":        hasErrors,
		"timeline":          timeline,
		"service_summary":   serviceSummary,
		"searched_since":    since.Format(time.RFC3339),
		"searched_until":    now.Format(time.RFC3339),
		"complete":          !truncated,
	}

	// Context entries (surrounding logs from each service).
	if includeContext && len(entries) > 0 {
		var contextEntries []map[string]any
		for svc, stats := range svcMap {
			ctxStart := stats.firstTime.Add(-2 * time.Second)
			ctxEnd := stats.lastTime.Add(2 * time.Second)
			ctxResult, err := deps.Logs.Search(ctx, store.LogSearchParams{
				Service:     svc,
				Environment: environment,
				Start:       &ctxStart,
				End:         &ctxEnd,
				Limit:       50,
			})
			if err != nil {
				continue
			}
			for _, cl := range ctxResult.Entries {
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

	return JSONResult(resp)
}
