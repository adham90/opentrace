package tools

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// --- timeline action ---

// Bounds for the timeline fan-out. Timeline reads from five stores and used to
// issue one extra query per error group and per healthcheck with no ceiling on
// either, so a busy instance turned a single tool call into a hundred sequential
// round-trips. The caps below bound the work; the fan-out runs concurrently
// under timelineFanout so the remaining queries overlap instead of queueing.
const (
	maxTimelineErrorGroups   = 20
	maxTimelineHealthChecks  = 20
	maxTimelineGroupEvents   = 20
	maxTimelineCheckResults  = 50
	maxTimelineLogs          = 200
	maxTimelineDeployScan    = 500
	maxTimelineAlerts        = 50
	maxTimelineEvents        = 200
	timelineFanoutConcurrent = 8
)

type timelineEvent struct {
	Time     string `json:"time"`
	Type     string `json:"type"`
	Severity string `json:"severity,omitempty"`
	Summary  string `json:"summary"`
	Source   string `json:"source,omitempty"`
	ID       string `json:"id,omitempty"`
}

// timelineWindow carries the resolved scope of one timeline call. Every
// collector takes it so no query can accidentally skip the environment filter.
type timelineWindow struct {
	start   time.Time
	end     time.Time
	service string
	env     string
}

func (w timelineWindow) contains(t time.Time) bool {
	return !t.Before(w.start) && !t.After(w.end)
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

	// Timeline is a listing like every other overview action, so it takes the
	// caller's env scope. It used to query unfiltered, which handed a
	// staging-scoped token production error groups, logs, alerts and
	// healthcheck transitions — data no drill-down tool would then let it open.
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	w := timelineWindow{
		start:   start,
		end:     end,
		service: ArgString(args, "service"),
		env:     env,
	}

	var events []timelineEvent
	var notes []string

	if d.LogStore != nil {
		logEvents, truncated := collectTimelineLogEvents(ctx, d, w)
		events = append(events, logEvents...)
		if truncated {
			notes = append(notes, fmt.Sprintf(
				"log scan hit its %d-row cap — error/deploy events in this window may be incomplete", maxTimelineLogs))
		}
	}
	if d.ErrorGroupStore != nil {
		groupEvents, truncated := collectTimelineErrorGroupEvents(ctx, d, w)
		events = append(events, groupEvents...)
		if truncated {
			notes = append(notes, fmt.Sprintf(
				"only the %d most recent error groups were scanned for lifecycle events", maxTimelineErrorGroups))
		}
	}
	if d.WatchStore != nil {
		events = append(events, collectTimelineAlertEvents(ctx, d, w)...)
	}
	if d.HealthCheckStore != nil {
		hcEvents, truncated := collectTimelineHealthCheckEvents(ctx, d, w)
		events = append(events, hcEvents...)
		if truncated {
			notes = append(notes, fmt.Sprintf(
				"only %d healthchecks were scanned for status transitions", maxTimelineHealthChecks))
		}
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].Time < events[j].Time
	})

	if len(events) > maxTimelineEvents {
		events = events[:maxTimelineEvents]
		notes = append(notes, fmt.Sprintf("timeline truncated to the first %d events in the window", maxTimelineEvents))
	}

	return timelineResult(w, events, notes)
}

// collectTimelineLogEvents gathers error/fatal log lines plus first-seen commit
// hashes. The bool reports whether a log query hit its row cap, so the caller
// can say the window may be incomplete instead of implying it is exhaustive.
func collectTimelineLogEvents(ctx context.Context, d OverviewDeps, w timelineWindow) ([]timelineEvent, bool) {
	var events []timelineEvent
	truncated := false

	for _, level := range []string{"error", "fatal"} {
		logs, err := d.LogStore.Search(ctx, store.LogSearchParams{
			Level:       level,
			Service:     w.service,
			Environment: w.env,
			Start:       &w.start,
			End:         &w.end,
			Limit:       maxTimelineLogs,
			SortAsc:     true,
		})
		if err != nil {
			continue
		}
		if len(logs) >= maxTimelineLogs {
			truncated = true
		}
		for _, l := range logs {
			ev := timelineEvent{
				Time:     l.Timestamp.Format(time.RFC3339),
				Type:     "error",
				Severity: l.Level,
				Summary:  Truncate(l.Message, 150),
				Source:   l.Service,
			}
			if l.ErrorFingerprint != "" {
				ev.ID = l.ErrorFingerprint
			}
			events = append(events, ev)
		}
	}

	// Deploy events: detect distinct commit hashes appearing in the window.
	allLogs, err := d.LogStore.Search(ctx, store.LogSearchParams{
		Service:     w.service,
		Environment: w.env,
		Start:       &w.start,
		End:         &w.end,
		Limit:       maxTimelineDeployScan,
		SortAsc:     true,
	})
	if err != nil {
		return events, truncated
	}
	if len(allLogs) >= maxTimelineDeployScan {
		truncated = true
	}

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
	return events, truncated
}

// collectTimelineErrorGroupEvents fetches lifecycle events for the error groups
// in scope. The per-group ListEvents calls run concurrently under a bounded
// worker pool rather than one after another.
func collectTimelineErrorGroupEvents(ctx context.Context, d OverviewDeps, w timelineWindow) ([]timelineEvent, bool) {
	groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
		Service:     w.service,
		Environment: w.env,
		Limit:       maxTimelineErrorGroups,
	})
	if err != nil {
		return nil, false
	}

	perGroup := make([][]timelineEvent, len(groups))
	runTimelineFanout(len(groups), func(i int) {
		eg := groups[i]
		// eg.Environment (not w.env) so an unscoped caller still reads each
		// group's own env history instead of every env's.
		egEvents, err := d.ErrorGroupStore.ListEvents(ctx, eg.Fingerprint, eg.Environment, maxTimelineGroupEvents)
		if err != nil {
			return
		}
		var out []timelineEvent
		for _, ev := range egEvents {
			if !w.contains(ev.CreatedAt) {
				continue
			}
			summary := Truncate(fmt.Sprintf("%s: %s", eg.ExceptionClass, eg.Message), 120)
			if ev.Reason != "" {
				summary += " — " + ev.Reason
			}
			out = append(out, timelineEvent{
				Time:    ev.CreatedAt.Format(time.RFC3339),
				Type:    ev.Action,
				Summary: summary,
				Source:  eg.Service,
				ID:      eg.Fingerprint,
			})
		}
		perGroup[i] = out
	})

	var events []timelineEvent
	for _, evs := range perGroup {
		events = append(events, evs...)
	}
	return events, len(groups) >= maxTimelineErrorGroups
}

func collectTimelineAlertEvents(ctx context.Context, d OverviewDeps, w timelineWindow) []timelineEvent {
	alerts, err := d.WatchStore.ListAlerts(ctx, "", "", maxTimelineAlerts)
	if err != nil {
		return nil
	}
	var events []timelineEvent
	for _, a := range alerts {
		// ListAlerts has no env filter, so drop out-of-scope alerts here (same
		// as HandleTriage).
		if w.env != "" && a.Environment != w.env {
			continue
		}
		if !w.contains(a.CreatedAt) {
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
	return events
}

// collectTimelineHealthCheckEvents finds status transitions inside the window.
// The per-check LatestResults calls run concurrently under a bounded pool, and
// the check list itself is capped — it used to be unbounded.
func collectTimelineHealthCheckEvents(ctx context.Context, d OverviewDeps, w timelineWindow) ([]timelineEvent, bool) {
	checks, err := d.HealthCheckStore.List(ctx, store.ListHealthCheckParams{
		Environment: w.env,
		Limit:       maxTimelineHealthChecks,
	})
	if err != nil {
		return nil, false
	}

	perCheck := make([][]timelineEvent, len(checks))
	runTimelineFanout(len(checks), func(i int) {
		hc := checks[i]
		results, err := d.HealthCheckStore.LatestResults(ctx, hc.ID, maxTimelineCheckResults)
		if err != nil {
			return
		}
		perCheck[i] = healthCheckTransitions(hc, results, w)
	})

	var events []timelineEvent
	for _, evs := range perCheck {
		events = append(events, evs...)
	}
	return events, len(checks) >= maxTimelineHealthChecks
}

func healthCheckTransitions(hc store.HealthCheck, results []store.HealthCheckResult, w timelineWindow) []timelineEvent {
	var events []timelineEvent
	var prev store.HealthCheckStatus
	// LatestResults is newest-first; walk oldest-first so `prev` is the
	// preceding status.
	for i := len(results) - 1; i >= 0; i-- {
		r := results[i]
		if !w.contains(r.CheckedAt) {
			if r.CheckedAt.Before(w.start) {
				prev = r.Status
			}
			continue
		}
		if prev != "" && r.Status != prev {
			events = append(events, timelineEvent{
				Time:     r.CheckedAt.Format(time.RFC3339),
				Type:     "healthcheck",
				Severity: healthCheckSeverity(r.Status),
				Summary:  fmt.Sprintf("%s went %s (was %s)", hc.Name, r.Status, prev),
				Source:   hc.URL,
				ID:       hc.ID,
			})
		}
		prev = r.Status
	}
	return events
}

func healthCheckSeverity(s store.HealthCheckStatus) string {
	switch s {
	case store.HealthCheckDown:
		return "critical"
	case store.HealthCheckDegraded:
		return "warning"
	default:
		return "info"
	}
}

// runTimelineFanout runs fn(0..n-1) with at most timelineFanoutConcurrent in
// flight. Each fn writes to its own index, so no locking is needed.
func runTimelineFanout(n int, fn func(i int)) {
	if n == 0 {
		return
	}
	sem := make(chan struct{}, timelineFanoutConcurrent)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}

func timelineResult(w timelineWindow, events []timelineEvent, notes []string) (*CallToolResult, error) {
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
	sort.Strings(serviceList)

	resp := map[string]any{
		"period": map[string]string{
			"start": w.start.Format(time.RFC3339),
			"end":   w.end.Format(time.RFC3339),
		},
		"environment":  envLabel(w.env),
		"total_events": len(events),
		"event_types":  typeCounts,
		"blast_radius": map[string]any{
			"affected_services": serviceList,
			"service_count":     len(serviceList),
		},
		"timeline": events,
	}
	if w.service != "" {
		resp["service"] = w.service
	}
	if len(notes) > 0 {
		resp["coverage_notes"] = notes
	}

	if len(events) > 0 {
		first := events[0]
		rootCause := map[string]any{
			"time":    first.Time,
			"type":    first.Type,
			"summary": first.Summary,
			"source":  first.Source,
			"note":    "Earliest event in the window — may be the root cause or first symptom.",
		}
		if first.Severity != "" {
			rootCause["severity"] = first.Severity
		}
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
