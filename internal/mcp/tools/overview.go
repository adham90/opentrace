package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/pkg/store"
)

// OverviewDeps holds the stores needed by the overview tool.
type OverviewDeps struct {
	LogStore         store.LogStore
	DSStore          store.DataSourceStore
	ServerStore      store.ServerStore
	ErrorGroupStore  store.ErrorGroupStore
	WatchStore       store.WatchStore
	HealthCheckStore store.HealthCheckStore
}

// OverviewTool returns the consolidated tool definition for system overview,
// triage, diagnose, and incident timeline.
func OverviewTool() mcp.Tool {
	return mcp.NewTool("overview",
		mcp.WithDescription(`System overview, triage, diagnosis, and incident timeline.

Actions:
- status: High-level system health dashboard (error groups, watch alerts, healthchecks, connectors, servers)
- triage: Prioritized list of items needing attention, sorted by severity then recency
- diagnose: All-in-one investigation with error summary, log volume, request performance, watch alerts, and healthchecks
- timeline: Chronological incident timeline from multiple data sources`),
		mcp.WithString("action", mcp.Required(), mcp.Description("Action: status, triage, diagnose, timeline")),
		mcp.WithString("service", mcp.Description("Service name to scope investigation")),
		mcp.WithString("timeframe", mcp.Description("Time window for diagnose (default: 1h). Examples: 30m, 2h, 24h, 7d")),
		mcp.WithString("start", mcp.Description("Start time for timeline (ISO 8601 / RFC3339)")),
		mcp.WithString("end", mcp.Description("End time for timeline (ISO 8601 / RFC3339)")),
	)
}

// OverviewHandler returns a handler for the consolidated overview tool.
func OverviewHandler(d OverviewDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		action, _ := args["action"].(string)

		switch action {
		case "status":
			return handleStatus(ctx, d)
		case "triage":
			return handleTriage(ctx, d)
		case "diagnose":
			return handleDiagnose(ctx, d, args)
		case "timeline":
			return handleTimeline(ctx, d, args)
		default:
			return mcp.NewToolResultError(fmt.Sprintf("unknown action: %s (use status, triage, diagnose, timeline)", action)), nil
		}
	}
}

// --- status action ---

func handleStatus(ctx context.Context, d OverviewDeps) (*mcp.CallToolResult, error) {
	overview := map[string]any{}

	// Logs (last hour)
	if d.LogStore != nil {
		now := time.Now()
		counts, err := d.LogStore.CountByLevel(ctx, store.LogCountParams{
			Since: now.Add(-1 * time.Hour),
			Until: now,
		})
		if err == nil {
			total := 0
			errCount := 0
			for level, count := range counts {
				total += count
				if level == "ERROR" || level == "error" || level == "fatal" || level == "FATAL" {
					errCount += count
				}
			}
			overview["logs"] = map[string]int{
				"last_hour":        total,
				"errors_last_hour": errCount,
			}
		}
	}

	// Unresolved error groups
	if d.ErrorGroupStore != nil {
		unresolvedCount, err := d.ErrorGroupStore.Count(ctx, store.ErrorGroupUnresolved)
		if err == nil && unresolvedCount > 0 {
			overview["error_groups"] = map[string]any{
				"unresolved": unresolvedCount,
			}
		}
	}

	// Active watch alerts
	if d.WatchStore != nil {
		pendingCount, err := d.WatchStore.CountPendingAlerts(ctx)
		if err == nil && pendingCount > 0 {
			overview["watch_alerts"] = map[string]any{
				"pending": pendingCount,
			}
		}
	}

	// Health checks
	if d.HealthCheckStore != nil {
		summaries, err := d.HealthCheckStore.UptimeSummaries(ctx, time.Now().Add(-1*time.Hour))
		if err == nil && len(summaries) > 0 {
			total := len(summaries)
			down := 0
			degraded := 0
			for _, s := range summaries {
				switch store.HealthCheckStatus(s.CurrentStatus) {
				case store.HealthCheckDown:
					down++
				case store.HealthCheckDegraded:
					degraded++
				}
			}
			hc := map[string]int{"total": total, "down": down}
			if degraded > 0 {
				hc["degraded"] = degraded
			}
			overview["healthchecks"] = hc
		}
	}

	// Connectors
	if d.DSStore != nil {
		connectors, err := d.DSStore.List(ctx, store.ListDataSourceParams{})
		if err == nil {
			cStats := map[string]int{"total": len(connectors), "connected": 0, "error": 0}
			for _, c := range connectors {
				switch c.Status {
				case store.StatusConnected:
					cStats["connected"]++
				case store.StatusError:
					cStats["error"]++
				}
			}
			overview["connectors"] = cStats
		}
	}

	// Servers
	if d.ServerStore != nil {
		servers, err := d.ServerStore.List(ctx, store.ListServerParams{})
		if err == nil {
			sStats := map[string]int{"total": len(servers), "online": 0, "offline": 0}
			for _, srv := range servers {
				switch srv.Status {
				case store.ServerOnline:
					sStats["online"]++
				case store.ServerOffline:
					sStats["offline"]++
				}
			}
			overview["servers"] = sStats
		}
	}

	// Suggested next tools based on findings.
	var suggestions []ToolSuggestion
	if eg, ok := overview["error_groups"].(map[string]any); ok {
		if u, _ := eg["unresolved"].(int); u > 0 {
			suggestions = append(suggestions, Suggest("error_groups", fmt.Sprintf("%d unresolved errors", u), map[string]any{"status": "unresolved"}))
		}
	}
	if logs, ok := overview["logs"].(map[string]int); ok {
		if logs["errors_last_hour"] > 0 {
			suggestions = append(suggestions, Suggest("log_summary", "Investigate errors in last hour", nil))
		}
	}
	if hc, ok := overview["healthchecks"].(map[string]int); ok {
		if hc["down"] > 0 {
			suggestions = append(suggestions, Suggest("uptime_status", fmt.Sprintf("%d endpoints down", hc["down"]), nil))
		}
	}
	suggestions = append(suggestions, Suggest("overview", "Deep dive into a specific service", map[string]any{"action": "diagnose"}))
	WithSuggestions(overview, suggestions...)

	data, err := json.Marshal(overview)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal overview: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// --- triage action ---

type triageEntry struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Time     string `json:"time"`
	ID       string `json:"id"`
}

func handleTriage(ctx context.Context, d OverviewDeps) (*mcp.CallToolResult, error) {
	var items []triageEntry

	// Unresolved error groups (highest priority)
	if d.ErrorGroupStore != nil {
		groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			SortBy: "occurrence_count",
			Limit:  10,
		})
		if err == nil {
			for _, eg := range groups {
				msg := eg.Message
				if len(msg) > 80 {
					msg = msg[:80] + "..."
				}
				title := msg
				if eg.ExceptionClass != "" {
					title = eg.ExceptionClass + ": " + msg
				}
				items = append(items, triageEntry{
					Type:     "error_group",
					Severity: "critical",
					Title:    title,
					Detail:   fmt.Sprintf("%d occurrences, last seen %s", eg.OccurrenceCount, eg.LastSeenAt.Format(time.RFC3339)),
					Time:     eg.LastSeenAt.Format(time.RFC3339),
					ID:       eg.Fingerprint,
				})
			}
		}
	}

	// Active watch alerts
	if d.WatchStore != nil {
		alerts, err := d.WatchStore.ListAlerts(ctx, "", "pending", 10)
		if err == nil {
			for _, a := range alerts {
				items = append(items, triageEntry{
					Type:     "watch_alert",
					Severity: "warning",
					Title:    a.Summary,
					Detail:   fmt.Sprintf("%s: %.2f (threshold: %.2f)", a.TriggerMetric, a.TriggerValue, a.ThresholdValue),
					Time:     a.CreatedAt.Format(time.RFC3339),
					ID:       a.ID,
				})
			}
		}
	}

	// Down healthchecks
	if d.HealthCheckStore != nil {
		summaries, err := d.HealthCheckStore.UptimeSummaries(ctx, time.Now().Add(-1*time.Hour))
		if err == nil {
			for _, s := range summaries {
				if store.HealthCheckStatus(s.CurrentStatus) == store.HealthCheckDown {
					items = append(items, triageEntry{
						Type:     "healthcheck",
						Severity: "critical",
						Title:    fmt.Sprintf("Endpoint '%s' is DOWN", s.Name),
						Detail:   s.URL,
						Time:     time.Now().Format(time.RFC3339),
						ID:       s.HealthCheckID,
					})
				}
			}
		}
	}

	// Error connectors
	if d.DSStore != nil {
		connectors, err := d.DSStore.List(ctx, store.ListDataSourceParams{})
		if err == nil {
			for _, c := range connectors {
				if c.Status == store.StatusError {
					detail := ""
					if c.StatusMessage != nil {
						detail = *c.StatusMessage
						if len(detail) > 100 {
							detail = detail[:100]
						}
					}
					items = append(items, triageEntry{
						Type:     "connector",
						Severity: "warning",
						Title:    "Connector '" + c.Name + "' error",
						Detail:   detail,
						Time:     c.UpdatedAt.Format(time.RFC3339),
						ID:       c.ID.String(),
					})
				}
			}
		}
	}

	// Offline servers
	if d.ServerStore != nil {
		servers, err := d.ServerStore.List(ctx, store.ListServerParams{})
		if err == nil {
			for _, srv := range servers {
				if srv.Status == store.ServerOffline {
					detail := "last seen: never"
					if srv.LastSeenAt != nil {
						detail = "last seen " + srv.LastSeenAt.Format(time.RFC3339)
					}
					items = append(items, triageEntry{
						Type:     "server",
						Severity: "info",
						Title:    "Server '" + srv.Hostname + "' offline",
						Detail:   detail,
						Time:     srv.CreatedAt.Format(time.RFC3339),
						ID:       srv.ID.String(),
					})
				}
			}
		}
	}

	// Sort by severity then time
	sort.Slice(items, func(i, j int) bool {
		si, sj := sevOrder(items[i].Severity), sevOrder(items[j].Severity)
		if si != sj {
			return si < sj
		}
		return items[i].Time > items[j].Time
	})

	if len(items) > 20 {
		items = items[:20]
	}

	if len(items) == 0 {
		return mcp.NewToolResultText("Nothing needs attention. System looks healthy."), nil
	}

	resp := map[string]any{
		"count": len(items),
		"items": items,
	}

	// Suggest investigating the top item.
	var suggestions []ToolSuggestion
	top := items[0]
	switch top.Type {
	case "error_group":
		suggestions = append(suggestions, Suggest("error_detail", "Investigate top error", map[string]any{
			"fingerprint": top.ID,
		}))
	case "watch_alert":
		suggestions = append(suggestions, Suggest("watches", "Investigate top alert", map[string]any{
			"action":   "investigate",
			"alert_id": top.ID,
		}))
	case "healthcheck":
		suggestions = append(suggestions, Suggest("healthchecks", "Check endpoint uptime details", map[string]any{
			"action": "uptime",
		}))
	}
	if len(items) > 1 {
		suggestions = append(suggestions, Suggest("overview", "Full system investigation", map[string]any{"action": "diagnose"}))
	}
	WithSuggestions(resp, suggestions...)

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal triage: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func sevOrder(sev string) int {
	switch sev {
	case "critical":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}

// --- diagnose action ---

func handleDiagnose(ctx context.Context, d OverviewDeps, args map[string]any) (*mcp.CallToolResult, error) {
	service, _ := args["service"].(string)

	// Parse timeframe (default 1h)
	timeframe := "1h"
	if v, _ := args["timeframe"].(string); v != "" {
		timeframe = v
	}
	duration, err := ParseTimeframe(timeframe)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid timeframe %q: %v", timeframe, err)), nil
	}

	now := time.Now().UTC()
	since := now.Add(-duration)

	// Collect sections in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex
	resp := map[string]any{
		"service":   service,
		"timeframe": timeframe,
		"since":     since.Format(time.RFC3339),
		"until":     now.Format(time.RFC3339),
	}

	// 1. Error summary
	if d.ErrorGroupStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			section := collectErrors(ctx, d, service, since)
			mu.Lock()
			resp["error_summary"] = section
			mu.Unlock()
		}()
	}

	// 2. Log volume
	if d.LogStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			section := collectLogVolume(ctx, d, service, since, now)
			mu.Lock()
			resp["log_volume"] = section
			mu.Unlock()
		}()
	}

	// 3. Request performance
	if d.LogStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			section := collectPerformance(ctx, d, service, since, now)
			mu.Lock()
			resp["request_performance"] = section
			mu.Unlock()
		}()
	}

	// 4. Watch alerts
	if d.WatchStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			section := collectWatchAlerts(ctx, d, service)
			mu.Lock()
			resp["watch_alerts"] = section
			mu.Unlock()
		}()
	}

	// 5. Health check status
	if d.HealthCheckStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			section := collectHealthChecks(ctx, d, since)
			mu.Lock()
			resp["healthcheck_status"] = section
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Build suggested next tools
	resp["suggested_tools"] = buildDiagnoseSuggestions(resp, service)

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func collectErrors(ctx context.Context, d OverviewDeps, service string, since time.Time) map[string]any {
	params := store.ListErrorGroupParams{
		Status: store.ErrorGroupUnresolved,
		SortBy: "occurrence_count",
		Limit:  5,
	}
	if service != "" {
		params.Service = service
	}

	groups, err := d.ErrorGroupStore.List(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	totalUnresolved, _ := d.ErrorGroupStore.Count(ctx, store.ErrorGroupUnresolved)

	newCount := 0
	topErrors := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		if g.FirstSeenAt.After(since) {
			newCount++
		}
		topErrors = append(topErrors, map[string]any{
			"fingerprint":      g.Fingerprint,
			"exception_class":  g.ExceptionClass,
			"message":          truncateMsg(g.Message, 100),
			"occurrence_count": g.OccurrenceCount,
			"last_seen_at":     g.LastSeenAt.Format(time.RFC3339),
		})
	}

	return map[string]any{
		"total_unresolved": totalUnresolved,
		"new_fingerprints": newCount,
		"top_errors":       topErrors,
	}
}

func collectLogVolume(ctx context.Context, d OverviewDeps, service string, since, until time.Time) map[string]any {
	params := store.LogCountParams{Since: since, Until: until}
	if service != "" {
		params.Service = service
	}

	byLevel, err := d.LogStore.CountByLevel(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	total := 0
	for _, c := range byLevel {
		total += c
	}
	errorCount := byLevel["ERROR"] + byLevel["FATAL"]

	var errorRate float64
	if total > 0 {
		errorRate = float64(errorCount) / float64(total) * 100
	}

	trend := "stable"
	if total > 1000 {
		trend = "high"
	}

	return map[string]any{
		"total":          total,
		"by_level":       byLevel,
		"error_count":    errorCount,
		"error_rate_pct": fmt.Sprintf("%.1f", errorRate),
		"trend":          trend,
	}
}

func collectPerformance(ctx context.Context, d OverviewDeps, service string, since, until time.Time) map[string]any {
	params := store.RequestSummarySearchParams{
		Start:  &since,
		End:    &until,
		SortBy: "duration_ms",
		Limit:  5,
	}

	results, err := d.LogStore.SearchRequestSummaries(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	if len(results) == 0 {
		return map[string]any{"total_requests": 0}
	}

	var filtered []store.RequestSummaryResult
	for _, r := range results {
		if service == "" || r.Service == service {
			filtered = append(filtered, r)
		}
	}

	var totalDuration float64
	var totalSQL int
	nPlusOne := 0
	slowest := make([]map[string]any, 0, minInt(5, len(filtered)))

	for _, r := range filtered {
		totalDuration += r.DurationMs
		totalSQL += r.SQLCount
		if r.NPlusOne {
			nPlusOne++
		}
	}

	for i, r := range filtered {
		if i >= 5 {
			break
		}
		entry := map[string]any{
			"path":        r.Path,
			"duration_ms": r.DurationMs,
			"sql_count":   r.SQLCount,
		}
		if r.NPlusOne {
			entry["n_plus_one"] = true
		}
		if r.Controller != "" {
			entry["controller"] = r.Controller + "#" + r.Action
		}
		slowest = append(slowest, entry)
	}

	var avgDuration float64
	var avgSQL float64
	if len(filtered) > 0 {
		avgDuration = totalDuration / float64(len(filtered))
		avgSQL = float64(totalSQL) / float64(len(filtered))
	}

	return map[string]any{
		"total_requests":    len(filtered),
		"avg_duration_ms":   fmt.Sprintf("%.1f", avgDuration),
		"avg_sql_count":     fmt.Sprintf("%.1f", avgSQL),
		"n_plus_one_count":  nPlusOne,
		"slowest_endpoints": slowest,
	}
}

func collectWatchAlerts(ctx context.Context, d OverviewDeps, service string) map[string]any {
	params := store.ListWatchParams{Status: store.WatchStatusTriggered}
	if service != "" {
		params.Service = service
	}

	triggered, err := d.WatchStore.List(ctx, params)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	alerts := make([]map[string]any, 0, len(triggered))
	for _, w := range triggered {
		entry := map[string]any{
			"id":        w.ID,
			"metric":    string(w.Metric),
			"threshold": w.Threshold,
			"urgency":   string(w.Urgency),
		}
		if w.CurrentValue != nil {
			entry["current_value"] = *w.CurrentValue
		}
		if w.Service != "" {
			entry["service"] = w.Service
		}
		alerts = append(alerts, entry)
	}

	pendingCount, _ := d.WatchStore.CountPendingAlerts(ctx)

	return map[string]any{
		"triggered_count": len(triggered),
		"pending_alerts":  pendingCount,
		"alerts":          alerts,
	}
}

func collectHealthChecks(ctx context.Context, d OverviewDeps, since time.Time) map[string]any {
	summaries, err := d.HealthCheckStore.UptimeSummaries(ctx, since)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	downCount := 0
	degradedCount := 0
	checks := make([]map[string]any, 0, len(summaries))
	for _, s := range summaries {
		entry := map[string]any{
			"name":            s.Name,
			"url":             s.URL,
			"status":          s.CurrentStatus,
			"uptime_pct":      fmt.Sprintf("%.1f", s.UptimePct),
			"avg_response_ms": fmt.Sprintf("%.0f", s.AvgResponseMs),
		}
		checks = append(checks, entry)
		if s.CurrentStatus == "down" {
			downCount++
		}
		if s.CurrentStatus == "degraded" {
			degradedCount++
		}
	}

	return map[string]any{
		"total":    len(summaries),
		"down":     downCount,
		"degraded": degradedCount,
		"endpoints": checks,
	}
}

func buildDiagnoseSuggestions(resp map[string]any, service string) []map[string]any {
	var suggestions []map[string]any

	if es, ok := resp["error_summary"].(map[string]any); ok {
		if topErrors, ok := es["top_errors"].([]map[string]any); ok && len(topErrors) > 0 {
			if fp, ok := topErrors[0]["fingerprint"].(string); ok && fp != "" {
				suggestions = append(suggestions, map[string]any{
					"tool": "error_detail",
					"args": map[string]any{"fingerprint": fp},
				})
			}
		}
	}

	if lv, ok := resp["log_volume"].(map[string]any); ok {
		if ec, ok := lv["error_count"].(int); ok && ec > 0 {
			args := map[string]any{"level": "error", "limit": float64(10)}
			if service != "" {
				args["service"] = service
			}
			suggestions = append(suggestions, map[string]any{
				"tool": "log_search",
				"args": args,
			})
		}
	}

	if wa, ok := resp["watch_alerts"].(map[string]any); ok {
		if tc, ok := wa["triggered_count"].(int); ok && tc > 0 {
			suggestions = append(suggestions, map[string]any{
				"tool": "watches",
				"args": map[string]any{"action": "status", "status": "triggered"},
			})
		}
	}

	if hc, ok := resp["healthcheck_status"].(map[string]any); ok {
		if dc, ok := hc["down"].(int); ok && dc > 0 {
			suggestions = append(suggestions, map[string]any{
				"tool": "healthchecks",
				"args": map[string]any{"action": "list"},
			})
		}
	}

	return suggestions
}

// --- timeline action ---

type timelineEvent struct {
	Time     string `json:"time"`
	Type     string `json:"type"`
	Severity string `json:"severity,omitempty"`
	Summary  string `json:"summary"`
	Source   string `json:"source,omitempty"`
	ID       string `json:"id,omitempty"`
}

func handleTimeline(ctx context.Context, d OverviewDeps, args map[string]any) (*mcp.CallToolResult, error) {
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

	serviceFilter, _ := args["service"].(string)

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
		suggestions = append(suggestions, Suggest("error_groups", "Review aggregated errors", map[string]any{"status": "unresolved"}))
	}
	WithSuggestions(resp, suggestions...)

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal timeline: %v", err)), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// ParseTimeframe converts strings like "1h", "30m", "24h", "7d" to a duration.
func ParseTimeframe(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("too short")
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	var num float64
	if _, err := fmt.Sscanf(numStr, "%f", &num); err != nil {
		return 0, err
	}
	switch unit {
	case 'm':
		return time.Duration(num) * time.Minute, nil
	case 'h':
		return time.Duration(num) * time.Hour, nil
	case 'd':
		return time.Duration(num) * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown unit %c (use m, h, or d)", unit)
	}
}

func truncateMsg(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
