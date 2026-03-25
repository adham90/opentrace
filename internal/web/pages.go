package web

import (
	"embed"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	errorviews "github.com/adham90/opentrace/internal/modules/errors/views"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/views"
	webviews "github.com/adham90/opentrace/internal/web/views"
)

//go:embed static/*
var staticFS embed.FS

type LogFilters struct {
	Query            string
	Service          string
	Level            string
	EventType        string
	TimeRange        string // preset: 15m, 1h, 6h, 24h, 7d, or empty (all)
	Environment      string
	CommitHash       string
	RequestID        string
	TraceID          string
	ExceptionClass   string
	ErrorFingerprint string
	SourceFile       string
}

// parseTimeRange converts a time range preset string to a Start time pointer.
// Returns nil if the preset is empty or unrecognized (meaning "all time").
func parseTimeRange(preset string) *time.Time {
	var d time.Duration
	switch preset {
	case "15m":
		d = 15 * time.Minute
	case "1h":
		d = time.Hour
	case "6h":
		d = 6 * time.Hour
	case "24h":
		d = 24 * time.Hour
	case "7d":
		d = 7 * 24 * time.Hour
	default:
		return nil
	}
	t := time.Now().Add(-d)
	return &t
}

// parseMetadataParams extracts "meta.key=value" query params into a map.
func parseMetadataParams(q url.Values) map[string]string {
	m := make(map[string]string)
	for key, vals := range q {
		if strings.HasPrefix(key, "meta.") && len(vals) > 0 && vals[0] != "" {
			m[strings.TrimPrefix(key, "meta.")] = vals[0]
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// parseExcludeParams extracts "exclude_field=value" query params into a map.
func parseExcludeParams(q url.Values) map[string]string {
	m := make(map[string]string)
	allowed := []string{"service", "level", "environment", "event_type",
		"exception_class", "error_fingerprint", "source_file", "commit_hash"}
	for _, f := range allowed {
		if v := q.Get("exclude_" + f); v != "" {
			m[f] = v
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// ── Dashboard data types ─────────────────────────────────────────

type dashboardSystemStatus struct {
	ServicesUp    int    `json:"services_up"`
	ServicesTotal int    `json:"services_total"`
	ErrorsPerHour int   `json:"errors_per_hour"`
	LogsPerHour   int   `json:"logs_per_hour"`
	WatchersActive int  `json:"watchers_active"`
	InvestOpen     int  `json:"invest_open"`
	OverallStatus  string `json:"overall_status"` // "ok", "warn", "critical"
}

type dashboardLastSession struct {
	ID      string `json:"id"`
	Intent  string `json:"intent"`
	Service string `json:"service"`
	Status  string `json:"status"`
	TimeAgo string `json:"time_ago"`
}

type dashboardAttentionItem struct {
	Severity string `json:"severity"` // "error", "warn", "info"
	Type     string `json:"type"`     // "error", "health", "watch"
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Velocity string `json:"velocity"` // "accelerating", "slowing", "stable"
	Time     string `json:"time"`
	Link     string `json:"link"`
}

type dashboardGlance struct {
	TotalLogs       int     `json:"total_logs"`
	ErrorGroups     int     `json:"error_groups"`
	WatcherTriggers int     `json:"watcher_triggers"`
	Investigations  int     `json:"investigations"`
	Uptime          float64 `json:"uptime"`
}

type dashboardActivityItem struct {
	Status string `json:"status"` // "ok", "error", "warn", "info"
	Type   string `json:"type"`
	Desc   string `json:"desc"`
	Time   string `json:"time"`
}

// dashboardSignal represents a service/endpoint health dot.
type dashboardSignal struct {
	Name   string  `json:"name"`
	Status string  `json:"status"` // "up", "down", "degraded"
	Uptime float64 `json:"uptime"`
	AvgMs  float64 `json:"avg_ms"`
}

// dashboardTimelineItem represents an event in the system timeline.
type dashboardTimelineItem struct {
	Icon    string `json:"icon"`    // "investigation", "alert", "deploy", "error", "resolved"
	Status  string `json:"status"`  // "ok", "error", "warn", "info"
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	Time    string `json:"time"`
	Link    string `json:"link"`
	SortKey int64  `json:"-"` // unix timestamp for sorting
}

type dashboardEndpoint struct {
	Method    string  `json:"method"`
	Path      string  `json:"path"`
	Requests  int     `json:"requests"`
	ErrorRate float64 `json:"error_rate"`
	AvgMs     float64 `json:"avg_ms"`
	P95Ms     float64 `json:"p95_ms"`
}

type dashboardTraffic struct {
	TotalRequests int     `json:"total_requests"`
	ErrorRate     float64 `json:"error_rate"`
	AvgMs         float64 `json:"avg_ms"`
	P95Ms         float64 `json:"p95_ms"`
}

type dashboardServer struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "online", "offline", "unknown"
}

type dashboardSparkBucket struct {
	Hour       int `json:"hour"`
	ErrorCount int `json:"error_count"`
	Total      int `json:"total"`
}

type dashboardData struct {
	SystemStatus   dashboardSystemStatus    `json:"system_status"`
	LastSession    *dashboardLastSession    `json:"last_session"`
	Attention      []dashboardAttentionItem `json:"attention"`
	Glance         dashboardGlance          `json:"glance"`
	QuietMinutes   int                      `json:"quiet_minutes"`
	LastIncident   string                   `json:"last_incident"`
	Activity       []dashboardActivityItem  `json:"activity"`
	Signals        []dashboardSignal        `json:"signals"`
	Timeline       []dashboardTimelineItem  `json:"timeline"`
	Traffic        *dashboardTraffic        `json:"traffic"`
	TopEndpoints   []dashboardEndpoint      `json:"top_endpoints"`
	Servers        []dashboardServer        `json:"servers"`
	ErrorSpark     []dashboardSparkBucket   `json:"error_spark"`
}

func (s *Server) isDevMode() bool {
	return s.cfg != nil && s.cfg.DevMode
}

// layoutData creates a views.LayoutData for templ rendering.
func (s *Server) layoutData(r *http.Request, title, nav string) views.LayoutData {
	user := UserFromContext(r.Context())
	isAdmin := user != nil && user.Role == store.RoleAdmin
	return views.LayoutData{
		Title:   title,
		Nav:     nav,
		User:    user,
		IsAdmin: isAdmin,
		DevMode: s.isDevMode(),
	}
}

// gatherDashboardData collects all data for the dashboard from stores.
// Each store query is independent — nil stores or errors result in zero/empty values.
func (s *Server) gatherDashboardData(r *http.Request) dashboardData {
	ctx := r.Context()
	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)
	oneDayAgo := now.Add(-24 * time.Hour)

	dd := dashboardData{}

	// ── System Status ──────────────────────────────────────────
	ss := &dd.SystemStatus
	ss.OverallStatus = "ok"

	if s.logStore != nil {
		counts, err := s.logStore.CountByLevel(ctx, store.LogCountParams{
			Since: oneHourAgo, Until: now,
		})
		if err == nil {
			for level, count := range counts {
				ss.LogsPerHour += count
				if level == "ERROR" {
					ss.ErrorsPerHour = count
				}
			}
		}
	}

	if s.healthCheckStore != nil {
		checks, err := s.healthCheckStore.List(ctx, store.ListHealthCheckParams{})
		if err == nil {
			ss.ServicesTotal = len(checks)
		}
		results, err := s.healthCheckStore.LatestResults(ctx, "", 100)
		if err == nil {
			seen := make(map[string]bool)
			for _, r := range results {
				if seen[r.HealthCheckID] {
					continue
				}
				seen[r.HealthCheckID] = true
				if r.Status == store.HealthCheckUp {
					ss.ServicesUp++
				}
			}
		}
	}

	if s.watchStore != nil {
		watches, err := s.watchStore.List(ctx, store.ListWatchParams{Status: store.WatchStatusActive})
		if err == nil {
			ss.WatchersActive = len(watches)
		}
	}

	if s.investigationSessionStore != nil {
		stats, err := s.investigationSessionStore.Stats(ctx)
		if err == nil && stats != nil {
			ss.InvestOpen = stats.OpenSessions
		}
	}

	// Derive overall status
	if ss.ErrorsPerHour > 10 || (ss.ServicesTotal > 0 && ss.ServicesUp < ss.ServicesTotal) {
		ss.OverallStatus = "critical"
	} else if ss.ErrorsPerHour > 3 {
		ss.OverallStatus = "warn"
	}

	// ── Last Session ───────────────────────────────────────────
	if s.investigationSessionStore != nil {
		user := UserFromContext(ctx)
		if user != nil {
			sess, err := s.investigationSessionStore.FindRecent(ctx, store.FindRecentSessionParams{
				UserID: user.ID,
				MaxAge: 24 * time.Hour,
			})
			if err == nil && sess != nil {
				intent := sess.Intent
				if intent == "" {
					intent = sess.PrimaryService
				}
				if intent == "" {
					intent = "an issue"
				}
				dd.LastSession = &dashboardLastSession{
					ID:      sess.ID,
					Intent:  intent,
					Service: sess.PrimaryService,
					Status:  string(sess.Status),
					TimeAgo: formatTimeAgo(sess.LastActivityAt),
				}
			}
		}
	}

	// ── Attention Items ────────────────────────────────────────
	if s.errorGroupStore != nil {
		groups, err := s.errorGroupStore.List(ctx, store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  3,
			SortBy: "last_seen_at",
		})
		if err == nil {
			for _, eg := range groups {
				dd.Attention = append(dd.Attention, dashboardAttentionItem{
					Severity: "error",
					Type:     "error",
					Title:    eg.ExceptionClass,
					Detail:   truncateStr(eg.Message, 50),
					Velocity: "stable",
					Time:     formatTimeAgo(eg.LastSeenAt),
					Link:     "/errors",
				})
			}
		}
	}

	if s.healthCheckStore != nil {
		results, err := s.healthCheckStore.LatestResults(ctx, "", 100)
		if err == nil {
			seen := make(map[string]bool)
			for _, r := range results {
				if seen[r.HealthCheckID] {
					continue
				}
				seen[r.HealthCheckID] = true
				if r.Status == store.HealthCheckDown {
					dd.Attention = append(dd.Attention, dashboardAttentionItem{
						Severity: "error",
						Type:     "health",
						Title:    "Health check failing",
						Detail:   r.HealthCheckID,
						Velocity: "stable",
						Time:     formatTimeAgo(r.CheckedAt),
						Link:     "/health",
					})
				}
			}
		}
	}

	if s.watchStore != nil {
		alerts, err := s.watchStore.ListAlerts(ctx, "", "pending", 3)
		if err == nil {
			for _, a := range alerts {
				dd.Attention = append(dd.Attention, dashboardAttentionItem{
					Severity: "warn",
					Type:     "watch",
					Title:    truncateStr(a.Summary, 50),
					Detail:   fmt.Sprintf("%s = %.1f", a.TriggerMetric, a.TriggerValue),
					Velocity: "stable",
					Time:     formatTimeAgo(a.CreatedAt),
					Link:     "/watchers",
				})
			}
		}
	}

	// Cap attention at 5
	if len(dd.Attention) > 5 {
		dd.Attention = dd.Attention[:5]
	}

	// ── Glance (24h) ──────────────────────────────────────────
	if s.logStore != nil {
		counts, err := s.logStore.CountByLevel(ctx, store.LogCountParams{
			Since: oneDayAgo, Until: now,
		})
		if err == nil {
			for _, count := range counts {
				dd.Glance.TotalLogs += count
			}
		}
	}
	if s.errorGroupStore != nil {
		groups, err := s.errorGroupStore.List(ctx, store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
		})
		if err == nil {
			dd.Glance.ErrorGroups = len(groups)
		}
	}
	if s.watchStore != nil {
		triggered, err := s.watchStore.List(ctx, store.ListWatchParams{Status: store.WatchStatusTriggered})
		if err == nil {
			dd.Glance.WatcherTriggers = len(triggered)
		}
	}
	if s.investigationSessionStore != nil {
		stats, err := s.investigationSessionStore.Stats(ctx)
		if err == nil && stats != nil {
			dd.Glance.Investigations = stats.TotalSessions
		}
	}
	if s.healthCheckStore != nil {
		summaries, err := s.healthCheckStore.UptimeSummaries(ctx, oneDayAgo)
		if err == nil && len(summaries) > 0 {
			var total float64
			for _, s := range summaries {
				total += s.UptimePct
			}
			dd.Glance.Uptime = total / float64(len(summaries))
		}
	}

	// ── Quiet Minutes ──────────────────────────────────────────
	dd.QuietMinutes = int(time.Since(now).Minutes()) // 0 by default
	if len(dd.Attention) == 0 {
		dd.QuietMinutes = 60 // show as quiet if no attention items
	}

	// ── Activity ───────────────────────────────────────────────
	if s.investigationSessionStore != nil {
		sessions, err := s.investigationSessionStore.List(ctx, store.ListInvestigationSessionParams{
			Limit: 3,
		})
		if err == nil {
			for _, sess := range sessions {
				status := "info"
				if sess.Status == store.InvestigationStatusResolved {
					status = "ok"
				}
				dd.Activity = append(dd.Activity, dashboardActivityItem{
					Status: status,
					Type:   "investigation",
					Desc:   sess.Intent,
					Time:   formatTimeAgo(sess.LastActivityAt),
				})
			}
		}
	}
	if s.watchStore != nil {
		alerts, err := s.watchStore.ListAlerts(ctx, "", "", 3)
		if err == nil {
			for _, a := range alerts {
				status := "warn"
				if a.Status == "dismissed" {
					status = "ok"
				}
				dd.Activity = append(dd.Activity, dashboardActivityItem{
					Status: status,
					Type:   "alert",
					Desc:   a.Summary,
					Time:   formatTimeAgo(a.CreatedAt),
				})
			}
		}
	}
	// Cap activity at 6
	if len(dd.Activity) > 6 {
		dd.Activity = dd.Activity[:6]
	}

	// ── Signals (service health dots) ─────────────────────────
	if s.healthCheckStore != nil {
		summaries, err := s.healthCheckStore.UptimeSummaries(ctx, oneDayAgo)
		if err == nil {
			for _, sum := range summaries {
				dd.Signals = append(dd.Signals, dashboardSignal{
					Name:   sum.Name,
					Status: sum.CurrentStatus,
					Uptime: sum.UptimePct,
					AvgMs:  sum.AvgResponseMs,
				})
			}
		}
	}

	// ── Timeline (unified event stream) ───────────────────────
	if s.investigationSessionStore != nil {
		sessions, err := s.investigationSessionStore.List(ctx, store.ListInvestigationSessionParams{
			Limit: 5,
		})
		if err == nil {
			// Group consecutive investigations with same status
			investCounts := make(map[string]int) // status -> count
			var lastInvestSession *store.InvestigationSession
			for _, sess := range sessions {
				investCounts[string(sess.Status)]++
				if lastInvestSession == nil {
					lastInvestSession = &sess
				}
			}
			for statusStr, count := range investCounts {
				sess := *lastInvestSession
				icon := "investigation"
				status := "info"
				title := "investigation opened"
				if store.InvestigationSessionStatus(statusStr) == store.InvestigationStatusResolved {
					icon = "resolved"
					status = "ok"
					title = "investigation resolved"
				} else if store.InvestigationSessionStatus(statusStr) == store.InvestigationStatusUnresolved {
					status = "error"
					title = "investigation unresolved"
				}
				if count > 1 {
					title = fmt.Sprintf("%d investigations %s", count, statusStr)
				}
				detail := ""
				if count == 1 {
					if sess.Summary != "" {
						detail = truncateStr(sess.Summary, 80)
					} else if sess.PrimaryService != "" {
						detail = sess.PrimaryService
						if sess.Intent != "" {
							detail += " — " + sess.Intent
						}
					} else if sess.Intent != "" {
						detail = sess.Intent
					} else {
						detail = sess.UserEmail + " via " + sess.ClientName
					}
				} else {
					detail = sess.UserEmail + " via " + sess.ClientName
				}
				link := "/sessions/" + sess.ID
				if count > 1 {
					link = "/sessions"
				}
				dd.Timeline = append(dd.Timeline, dashboardTimelineItem{
					Icon:    icon,
					Status:  status,
					Title:   title,
					Detail:  detail,
					Time:    formatTimeAgo(sess.LastActivityAt),
					Link:    link,
					SortKey: sess.LastActivityAt.Unix(),
				})
			}
		}
	}

	if s.watchStore != nil {
		alerts, err := s.watchStore.ListAlerts(ctx, "", "", 5)
		if err == nil {
			for _, a := range alerts {
				status := "warn"
				icon := "alert"
				if a.Status == "dismissed" {
					status = "ok"
					icon = "resolved"
				}
				dd.Timeline = append(dd.Timeline, dashboardTimelineItem{
					Icon:    icon,
					Status:  status,
					Title:   "watcher: " + truncateStr(a.Summary, 40),
					Detail:  fmt.Sprintf("%s = %.1f", a.TriggerMetric, a.TriggerValue),
					Time:    formatTimeAgo(a.CreatedAt),
					Link:    "/watchers",
					SortKey: a.CreatedAt.Unix(),
				})
			}
		}
	}

	if s.deployStore != nil {
		deploys, err := s.deployStore.GetRecent(ctx, "", 3)
		if err == nil {
			for _, d := range deploys {
				detail := d.CommitHash
				if len(detail) > 8 {
					detail = detail[:8]
				}
				if d.Author != "" {
					detail += " by " + d.Author
				}
				if d.Branch != "" {
					detail += " (" + d.Branch + ")"
				}
				dd.Timeline = append(dd.Timeline, dashboardTimelineItem{
					Icon:    "deploy",
					Status:  "info",
					Title:   "deploy: " + d.Service,
					Detail:  detail,
					Time:    formatTimeAgo(d.DeployedAt),
					Link:    "",
					SortKey: d.DeployedAt.Unix(),
				})
			}
		}
	}

	// Sort timeline by time descending
	sort.Slice(dd.Timeline, func(i, j int) bool {
		return dd.Timeline[i].SortKey > dd.Timeline[j].SortKey
	})
	if len(dd.Timeline) > 10 {
		dd.Timeline = dd.Timeline[:10]
	}

	// ── Traffic Summary ───────────────────────────────────────
	if s.analyticsStore != nil {
		summary, err := s.analyticsStore.TrafficSummary(ctx, store.AnalyticsParams{
			Since: oneDayAgo, Until: now,
		})
		if err == nil && summary != nil && summary.TotalRequests > 0 {
			dd.Traffic = &dashboardTraffic{
				TotalRequests: summary.TotalRequests,
				ErrorRate:     summary.ErrorRate,
				AvgMs:         summary.AvgDurationMs,
				P95Ms:         summary.P95DurationMs,
			}
		}
	}

	// ── Top Endpoints (by p95) ────────────────────────────────
	if s.analyticsStore != nil {
		eps, err := s.analyticsStore.TopEndpoints(ctx, store.TopEndpointParams{
			Since:  oneDayAgo,
			Until:  now,
			SortBy: "p95_duration",
			Limit:  5,
		})
		if err == nil {
			for _, ep := range eps {
				path := ep.PathPattern
				if path == "" {
					path = ep.Controller + "#" + ep.Action
				}
				errRate := 0.0
				if ep.RequestCount > 0 {
					errRate = float64(ep.ErrorCount) / float64(ep.RequestCount) * 100
				}
				dd.TopEndpoints = append(dd.TopEndpoints, dashboardEndpoint{
					Method:    ep.Method,
					Path:      path,
					Requests:  ep.RequestCount,
					ErrorRate: errRate,
					AvgMs:     ep.AvgDurationMs,
					P95Ms:     ep.P95DurationMs,
				})
			}
		}
	}

	// ── Servers ───────────────────────────────────────────────
	if s.serverStore != nil {
		servers, err := s.serverStore.List(ctx, store.ListServerParams{})
		if err == nil {
			for _, srv := range servers {
				name := srv.DisplayName
				if name == "" {
					name = srv.Hostname
				}
				dd.Servers = append(dd.Servers, dashboardServer{
					Name:   name,
					Status: string(srv.Status),
				})
			}
		}
	}

	// ── Error Sparkline (24h, hourly buckets) ─────────────────
	if s.logStore != nil {
		buckets, err := s.logStore.Histogram(ctx, store.LogHistogramParams{
			Since:    oneDayAgo,
			Until:    now,
			Interval: time.Hour,
			Level:    "ERROR",
		})
		if err == nil && len(buckets) > 0 {
			for i, b := range buckets {
				dd.ErrorSpark = append(dd.ErrorSpark, dashboardSparkBucket{
					Hour:       i,
					ErrorCount: b.Total, // when filtered by ERROR level, Total = error count
					Total:      b.Total,
				})
			}
		}
	}

	return dd
}

func formatTimeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (s *Server) handleDashboardPage(w http.ResponseWriter, r *http.Request) {
	dd := s.gatherDashboardData(r)
	layout := s.layoutData(r, "Dashboard", "dashboard")

	dash := webviews.DashboardData{
		QuietMinutes: dd.QuietMinutes,
	}

	// Map system status
	dash.SystemStatus = &webviews.DashboardSystemStatus{
		ServicesUp:     dd.SystemStatus.ServicesUp,
		ServicesTotal:  dd.SystemStatus.ServicesTotal,
		ErrorsPerHour:  dd.SystemStatus.ErrorsPerHour,
		LogsPerHour:    dd.SystemStatus.LogsPerHour,
		WatchersActive: dd.SystemStatus.WatchersActive,
		InvestOpen:     dd.SystemStatus.InvestOpen,
		OverallStatus:  dd.SystemStatus.OverallStatus,
	}

	// Map glance
	dash.Glance = &webviews.DashboardGlance{
		TotalLogs:       dd.Glance.TotalLogs,
		ErrorGroups:     dd.Glance.ErrorGroups,
		WatcherTriggers: dd.Glance.WatcherTriggers,
		Investigations:  dd.Glance.Investigations,
		Uptime:          dd.Glance.Uptime,
	}

	// Map last session
	if dd.LastSession != nil {
		dash.LastSession = &webviews.DashboardLastSession{
			ID:      dd.LastSession.ID,
			Intent:  dd.LastSession.Intent,
			Service: dd.LastSession.Service,
			Status:  dd.LastSession.Status,
			TimeAgo: dd.LastSession.TimeAgo,
		}
	}

	// Map attention items
	for _, item := range dd.Attention {
		dash.Attention = append(dash.Attention, webviews.DashboardAttentionItem{
			Severity: item.Severity,
			Type:     item.Type,
			Title:    item.Title,
			Detail:   item.Detail,
			Time:     item.Time,
			Link:     item.Link,
		})
	}

	// Map signals
	for _, sig := range dd.Signals {
		dash.Signals = append(dash.Signals, webviews.DashboardSignal{
			Name:   sig.Name,
			Status: sig.Status,
		})
	}

	// Map servers
	for _, srv := range dd.Servers {
		dash.Servers = append(dash.Servers, webviews.DashboardServer{
			Name:   srv.Name,
			Status: srv.Status,
		})
	}

	// Map timeline
	for _, item := range dd.Timeline {
		dash.Timeline = append(dash.Timeline, webviews.DashboardTimelineItem{
			Status: item.Status,
			Title:  item.Title,
			Detail: item.Detail,
			Time:   item.Time,
			Link:   item.Link,
		})
	}

	// Map endpoints
	for _, ep := range dd.TopEndpoints {
		dash.Endpoints = append(dash.Endpoints, webviews.DashboardEndpoint{
			Method: ep.Method,
			Path:   ep.Path,
			P95Ms:  ep.P95Ms,
		})
	}

	// Map traffic
	if dd.Traffic != nil {
		dash.Traffic = &webviews.DashboardTraffic{
			TotalRequests: dd.Traffic.TotalRequests,
		}
	}

	webviews.DashboardPage(layout, dash).Render(r.Context(), w)
}

func (s *Server) handleDashboardAPI(w http.ResponseWriter, r *http.Request) {
	dd := s.gatherDashboardData(r)
	writeJSON(w, http.StatusOK, dd)
}

func (s *Server) handleLogsPage(w http.ResponseWriter, r *http.Request) {
	filters := LogFilters{
		Query:            r.URL.Query().Get("query"),
		Service:          r.URL.Query().Get("service"),
		Level:            r.URL.Query().Get("level"),
		EventType:        r.URL.Query().Get("event_type"),
		TimeRange:        r.URL.Query().Get("time_range"),
		Environment:      r.URL.Query().Get("environment"),
		CommitHash:       r.URL.Query().Get("commit_hash"),
		RequestID:        r.URL.Query().Get("request_id"),
		TraceID:          r.URL.Query().Get("trace_id"),
		ExceptionClass:   r.URL.Query().Get("exception_class"),
		ErrorFingerprint: r.URL.Query().Get("error_fingerprint"),
		SourceFile:       r.URL.Query().Get("source_file"),
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 500 {
		limit = 500
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	searchParams := store.LogSearchParams{
		Query:            filters.Query,
		Service:          filters.Service,
		Level:            filters.Level,
		EventType:        filters.EventType,
		Start:            parseTimeRange(filters.TimeRange),
		Limit:            limit + 1,
		Offset:           offset,
		MetadataFilter:   parseMetadataParams(r.URL.Query()),
		Exclude:          parseExcludeParams(r.URL.Query()),
		Environment:      filters.Environment,
		CommitHash:       filters.CommitHash,
		RequestID:        filters.RequestID,
		TraceID:          filters.TraceID,
		ExceptionClass:   filters.ExceptionClass,
		ErrorFingerprint: filters.ErrorFingerprint,
		SourceFile:       filters.SourceFile,
	}
	if startStr := r.URL.Query().Get("start"); startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			searchParams.Start = &t
		}
	}
	if endStr := r.URL.Query().Get("end"); endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			searchParams.End = &t
		}
	}

	var logEntries []store.LogEntry
	var hasMore bool
	var maxID int64
	if s.logStore != nil {
		var err error
		logEntries, err = s.logStore.Search(r.Context(), searchParams)
		if err != nil {
			logEntries = nil
		}
		hasMore = len(logEntries) > limit
		if hasMore {
			logEntries = logEntries[:limit]
		}
		for _, l := range logEntries {
			if l.ID > maxID {
				maxID = l.ID
			}
		}
	}

	layout := s.layoutData(r, "Logs", "logs")
	logsData := webviews.LogsPageData{
		Filters: webviews.LogFilters{
			Query:            filters.Query,
			Service:          filters.Service,
			Level:            filters.Level,
			EventType:        filters.EventType,
			TimeRange:        filters.TimeRange,
			Environment:      filters.Environment,
			CommitHash:       filters.CommitHash,
			RequestID:        filters.RequestID,
			TraceID:          filters.TraceID,
			ExceptionClass:   filters.ExceptionClass,
			ErrorFingerprint: filters.ErrorFingerprint,
			SourceFile:       filters.SourceFile,
		},
		Logs:     logEntries,
		MaxLogID: maxID,
		HasMore:  hasMore,
	}
	webviews.LogsPage(layout, logsData).Render(r.Context(), w)
}

func (s *Server) handleErrorsPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Errors", "errors")
	var errorGroups []store.ErrorGroup
	if s.errorGroupStore != nil {
		status := store.ErrorGroupStatus(r.URL.Query().Get("status"))
		groups, err := s.errorGroupStore.List(r.Context(), store.ListErrorGroupParams{
			Status:  status,
			Service: r.URL.Query().Get("service"),
			SortBy:  "last_seen_at",
			Limit:   100,
		})
		if err == nil {
			errorGroups = groups
		}
	}
	errorviews.ErrorsPage(layout, errorGroups).Render(r.Context(), w)
}

func (s *Server) handleErrorDetailPage(w http.ResponseWriter, r *http.Request) {
	fp := chi.URLParam(r, "fingerprint")
	if fp == "" {
		http.Redirect(w, r, "/errors", http.StatusFound)
		return
	}
	if s.errorGroupStore == nil {
		writeError(w, http.StatusNotFound, "error tracking not available")
		return
	}

	eg, err := s.errorGroupStore.Get(r.Context(), fp)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "error group not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get error group")
		return
	}

	events, _ := s.errorGroupStore.ListEvents(r.Context(), fp, 20)
	eg.Events = events

	layout := s.layoutData(r, eg.ExceptionClass, "errors")

	// Fetch recent log entries for this error fingerprint
	var errorLogs []store.LogEntry
	if s.logStore != nil {
		logs, err := s.logStore.Search(r.Context(), store.LogSearchParams{
			ErrorFingerprint: fp,
			Limit:            25,
		})
		if err == nil {
			errorLogs = logs
		}
	}

	errorviews.ErrorDetailPage(layout, *eg, errorLogs).Render(r.Context(), w)
}

func (s *Server) handleWatchersPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Watchers", "watchers")
	var watches []store.Watch
	var watchAlerts []store.WatchAlert
	var pendingCount int
	if s.watchStore != nil {
		w2, err := s.watchStore.List(r.Context(), store.ListWatchParams{Limit: 100})
		if err == nil {
			watches = w2
		}
		a, err := s.watchStore.ListAlerts(r.Context(), "", "", 20)
		if err == nil {
			watchAlerts = a
		}
		p, err := s.watchStore.CountPendingAlerts(r.Context())
		if err == nil {
			pendingCount = p
		}
	}
	webviews.WatchersPage(layout, watches, watchAlerts, pendingCount).Render(r.Context(), w)
}

func (s *Server) handleWatchDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Redirect(w, r, "/watchers", http.StatusFound)
		return
	}
	if s.watchStore == nil {
		writeError(w, http.StatusNotFound, "watch system not configured")
		return
	}

	wt, err := s.watchStore.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "watch not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get watch")
		return
	}

	layout := s.layoutData(r, "Watch: "+string(wt.Metric), "watchers")

	detail := webviews.WatcherDetailData{
		Watch: wt,
	}

	// Fetch execution runs
	runs, err := s.watchStore.ListRuns(r.Context(), id, 50)
	if err == nil {
		detail.Runs = runs
	}

	// Fetch alerts for this watch
	alerts, err := s.watchStore.ListAlerts(r.Context(), id, "", 20)
	if err == nil {
		detail.Alerts = alerts
	}

	// Look up the investigation session that created this watch
	if wt.SessionID != "" && s.investigationSessionStore != nil {
		sess, err := s.investigationSessionStore.GetByID(r.Context(), wt.SessionID)
		if err == nil {
			detail.Session = sess
		}
	}

	webviews.WatcherDetailPage(layout, detail).Render(r.Context(), w)
}

func (s *Server) handleHealthPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Health", "health")
	var uptimeSummaries []store.UptimeSummary
	var healthChecks []store.HealthCheck
	if s.healthCheckStore != nil {
		checks, err := s.healthCheckStore.List(r.Context(), store.ListHealthCheckParams{})
		if err == nil {
			healthChecks = checks
		}
		oneDayAgo := time.Now().Add(-24 * time.Hour)
		summaries, err := s.healthCheckStore.UptimeSummaries(r.Context(), oneDayAgo)
		if err == nil {
			uptimeSummaries = summaries
		}
	}
	webviews.HealthPage(layout, uptimeSummaries, healthChecks).Render(r.Context(), w)
}

// handleLogsFragment serves HTMX log fragments for the logs page.
func (s *Server) handleLogsFragment(w http.ResponseWriter, r *http.Request) {
	if !isHTMX(r) {
		http.Redirect(w, r, "/logs", http.StatusMovedPermanently)
		return
	}

	filters := LogFilters{
		Query:            r.URL.Query().Get("query"),
		Service:          r.URL.Query().Get("service"),
		Level:            r.URL.Query().Get("level"),
		EventType:        r.URL.Query().Get("event_type"),
		TimeRange:        r.URL.Query().Get("time_range"),
		Environment:      r.URL.Query().Get("environment"),
		CommitHash:       r.URL.Query().Get("commit_hash"),
		RequestID:        r.URL.Query().Get("request_id"),
		TraceID:          r.URL.Query().Get("trace_id"),
		ExceptionClass:   r.URL.Query().Get("exception_class"),
		ErrorFingerprint: r.URL.Query().Get("error_fingerprint"),
		SourceFile:       r.URL.Query().Get("source_file"),
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 500 {
		limit = 500
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	searchParams := store.LogSearchParams{
		Query:            filters.Query,
		Service:          filters.Service,
		Level:            filters.Level,
		EventType:        filters.EventType,
		Start:            parseTimeRange(filters.TimeRange),
		Limit:            limit + 1,
		Offset:           offset,
		MetadataFilter:   parseMetadataParams(r.URL.Query()),
		Exclude:          parseExcludeParams(r.URL.Query()),
		Environment:      filters.Environment,
		CommitHash:       filters.CommitHash,
		RequestID:        filters.RequestID,
		TraceID:          filters.TraceID,
		ExceptionClass:   filters.ExceptionClass,
		ErrorFingerprint: filters.ErrorFingerprint,
		SourceFile:       filters.SourceFile,
	}
	if s := r.URL.Query().Get("start"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			searchParams.Start = &t
		}
	}
	if e := r.URL.Query().Get("end"); e != "" {
		if t, err := time.Parse(time.RFC3339, e); err == nil {
			searchParams.End = &t
		}
	}

	logs, err := s.logStore.Search(r.Context(), searchParams)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search logs")
		return
	}

	hasMore := len(logs) > limit
	if hasMore {
		logs = logs[:limit]
	}

	var maxID int64
	for _, l := range logs {
		if l.ID > maxID {
			maxID = l.ID
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"logs":     logs,
		"offset":   offset,
		"limit":    limit,
		"has_more": hasMore,
		"max_id":   maxID,
	})
}

func (s *Server) handleLogsPoll(w http.ResponseWriter, r *http.Request) {
	sinceID := int64(0)
	if v := r.URL.Query().Get("since_id"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
			sinceID = parsed
		}
	}

	if sinceID == 0 {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		return
	}

	filters := LogFilters{
		Query:            r.URL.Query().Get("query"),
		Service:          r.URL.Query().Get("service"),
		Level:            r.URL.Query().Get("level"),
		EventType:        r.URL.Query().Get("event_type"),
		TimeRange:        r.URL.Query().Get("time_range"),
		Environment:      r.URL.Query().Get("environment"),
		CommitHash:       r.URL.Query().Get("commit_hash"),
		RequestID:        r.URL.Query().Get("request_id"),
		TraceID:          r.URL.Query().Get("trace_id"),
		ExceptionClass:   r.URL.Query().Get("exception_class"),
		ErrorFingerprint: r.URL.Query().Get("error_fingerprint"),
		SourceFile:       r.URL.Query().Get("source_file"),
	}

	pollParams := store.LogSearchParams{
		Query:            filters.Query,
		Service:          filters.Service,
		Level:            filters.Level,
		EventType:        filters.EventType,
		Start:            parseTimeRange(filters.TimeRange),
		SinceID:          sinceID,
		Limit:            200,
		MetadataFilter:   parseMetadataParams(r.URL.Query()),
		Exclude:          parseExcludeParams(r.URL.Query()),
		Environment:      filters.Environment,
		CommitHash:       filters.CommitHash,
		RequestID:        filters.RequestID,
		TraceID:          filters.TraceID,
		ExceptionClass:   filters.ExceptionClass,
		ErrorFingerprint: filters.ErrorFingerprint,
		SourceFile:       filters.SourceFile,
	}
	if s := r.URL.Query().Get("start"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			pollParams.Start = &t
		}
	}
	if e := r.URL.Query().Get("end"); e != "" {
		if t, err := time.Parse(time.RFC3339, e); err == nil {
			pollParams.End = &t
		}
	}
	logs, err := s.logStore.Search(r.Context(), pollParams)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if len(logs) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	var maxID int64
	for _, l := range logs {
		if l.ID > maxID {
			maxID = l.ID
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"logs":   logs,
		"max_id": maxID,
	})
}

func (s *Server) handleLogsHistogram(w http.ResponseWriter, r *http.Request) {
	if s.logStore == nil {
		writeJSON(w, http.StatusOK, []store.LogHistogramBucket{})
		return
	}

	timeRange := r.URL.Query().Get("time_range")
	if timeRange == "" {
		timeRange = "1h"
	}

	var duration time.Duration
	switch timeRange {
	case "15m":
		duration = 15 * time.Minute
	case "1h":
		duration = time.Hour
	case "6h":
		duration = 6 * time.Hour
	case "24h":
		duration = 24 * time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	default:
		duration = time.Hour
	}

	// Auto interval based on time range
	var interval time.Duration
	switch timeRange {
	case "15m":
		interval = time.Minute
	case "1h":
		interval = 5 * time.Minute
	case "6h":
		interval = 15 * time.Minute
	case "24h":
		interval = time.Hour
	case "7d":
		interval = 6 * time.Hour
	default:
		interval = 5 * time.Minute
	}

	now := time.Now()
	params := store.LogHistogramParams{
		Since:    now.Add(-duration),
		Until:    now,
		Interval: interval,
		Service:  r.URL.Query().Get("service"),
		Level:    r.URL.Query().Get("level"),
	}

	buckets, err := s.logStore.Histogram(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute histogram")
		return
	}
	if buckets == nil {
		buckets = []store.LogHistogramBucket{}
	}
	writeJSON(w, http.StatusOK, buckets)
}

func (s *Server) handleGetLogDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid log id")
		return
	}
	entry, err := s.logStore.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "log not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get log")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}


func (s *Server) handleToolsAPI(w http.ResponseWriter, r *http.Request) {
	type toolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Access      string `json:"access"`
		Requires    string `json:"requires,omitempty"`
	}

	type toolCategory struct {
		Name        string     `json:"name"`
		Description string     `json:"description"`
		Tools       []toolInfo `json:"tools"`
	}

	var categories []toolCategory

	// Use the auto-detected catalog when available.
	if s.toolCatalog != nil {
		for _, cat := range s.toolCatalog.Categories() {
			tc := toolCategory{Name: cat.Name, Description: cat.Description}
			for _, t := range cat.Tools {
				tc.Tools = append(tc.Tools, toolInfo{
					Name:        t.Name,
					Description: t.Description,
					Access:      t.Access,
					Requires:    t.Requires,
				})
			}
			categories = append(categories, tc)
		}
	}

	// Append dynamic connector tools from the registry (added after startup).
	if s.registry != nil {
		dynamicTools := s.registry.AllTools()
		if len(dynamicTools) > 0 {
			dynCat := toolCategory{
				Name:        "Connector Queries",
				Description: "Dynamic tools registered by active database connectors",
			}
			for _, t := range dynamicTools {
				dynCat.Tools = append(dynCat.Tools, toolInfo{
					Name:        t.Name,
					Description: t.Description,
					Access:      "admin",
					Requires:    "database connector",
				})
			}
			categories = append(categories, dynCat)
		}
	}

	writeJSON(w, http.StatusOK, categories)
}


func (s *Server) handleOverviewAPI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type overviewStats struct {
		Logs       map[string]int `json:"logs"`
		Connectors map[string]int `json:"connectors"`
		Servers    map[string]int `json:"servers"`
	}

	stats := overviewStats{
		Logs:       map[string]int{"last_hour": 0, "errors_last_hour": 0},
		Connectors: map[string]int{"total": 0, "connected": 0, "error": 0},
		Servers:    map[string]int{"total": 0, "online": 0, "offline": 0},
	}

	// Logs stats (last hour) — use COUNT query instead of fetching rows
	if s.logStore != nil {
		now := time.Now()
		logOneHourAgo := now.Add(-1 * time.Hour)
		counts, err := s.logStore.CountByLevel(ctx, store.LogCountParams{
			Since: logOneHourAgo,
			Until: now,
		})
		if err == nil {
			total := 0
			for level, count := range counts {
				total += count
				if level == "ERROR" {
					stats.Logs["errors_last_hour"] = count
				}
			}
			stats.Logs["last_hour"] = total
		}
	}

	// Connectors stats
	if s.dsStore != nil {
		connectors, err := s.dsStore.List(ctx, store.ListDataSourceParams{})
		if err == nil {
			stats.Connectors["total"] = len(connectors)
			for _, c := range connectors {
				switch c.Status {
				case store.StatusConnected:
					stats.Connectors["connected"]++
				case store.StatusError:
					stats.Connectors["error"]++
				}
			}
		}
	}

	// Servers stats
	if s.serverStore != nil {
		servers, err := s.serverStore.List(ctx, store.ListServerParams{})
		if err == nil {
			stats.Servers["total"] = len(servers)
			for _, srv := range servers {
				switch srv.Status {
				case store.ServerOnline:
					stats.Servers["online"]++
				case store.ServerOffline:
					stats.Servers["offline"]++
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	services, err := store.ListServices(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list services")
		return
	}
	if services == nil {
		services = []string{}
	}
	writeJSON(w, http.StatusOK, services)
}

func (s *Server) handleListEventTypes(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	types, err := store.ListEventTypes(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list event types")
		return
	}
	if types == nil {
		types = []string{}
	}
	writeJSON(w, http.StatusOK, types)
}

func (s *Server) handleLogValues(w http.ResponseWriter, r *http.Request) {
	if s.logStore == nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	field := r.URL.Query().Get("field")
	if field == "" {
		writeError(w, http.StatusBadRequest, "field parameter required")
		return
	}
	now := time.Now().UTC()
	params := store.LogCountParams{
		Since: now.Add(-7 * 24 * time.Hour),
		Until: now,
	}
	if field == "metadata_key" {
		keys, err := s.logStore.MetadataKeys(r.Context(), params)
		if err != nil {
			writeJSON(w, http.StatusOK, []string{})
			return
		}
		if keys == nil {
			keys = []string{}
		}
		writeJSON(w, http.StatusOK, keys)
		return
	}
	values, err := s.logStore.DistinctValues(r.Context(), field, params)
	if err != nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	if values == nil {
		values = []string{}
	}
	writeJSON(w, http.StatusOK, values)
}

func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Settings", "settings")

	sd := webviews.SettingsData{
		CSRFToken:      CSRFToken(r.Context()),
		RetentionDays:  30,
		MaxQueryRows:   500,
		StatementTimeoutMS: 5000,
		MCPName:        "opentrace",
	}

	if s.settingsStore != nil {
		settings, err := s.settingsStore.GetRetention(r.Context())
		if err == nil {
			sd.RetentionDays = settings.RetentionDays
			sd.MetricRetentionDays = settings.MetricRetentionDays
		}
	}

	sd.EnvKeyOverride = s.cfg != nil && s.cfg.APIKey != ""
	sd.CORSEnvOverride = s.cfg != nil && len(s.cfg.CORSAllowedOrigins) > 0
	if sd.CORSEnvOverride {
		sd.CORSOrigins = strings.Join(s.cfg.CORSAllowedOrigins, ",")
	} else if s.settingsStore != nil {
		if val, err := s.settingsStore.GetCORSOrigins(r.Context()); err == nil {
			sd.CORSOrigins = val
		}
	}

	// Query guardrails
	sd.QueryEnvOverride = os.Getenv("OPENTRACE_MAX_QUERY_ROWS") != "" || os.Getenv("OPENTRACE_STATEMENT_TIMEOUT_MS") != ""
	if sd.QueryEnvOverride && s.cfg != nil {
		sd.MaxQueryRows = s.cfg.MaxQueryRows
		sd.StatementTimeoutMS = s.cfg.StatementTimeoutMS
	} else if s.settingsStore != nil {
		if v, err := s.settingsStore.GetMaxQueryRows(r.Context()); err == nil && v > 0 {
			sd.MaxQueryRows = v
		}
		if v, err := s.settingsStore.GetStatementTimeout(r.Context()); err == nil && v > 0 {
			sd.StatementTimeoutMS = v
		}
	}

	// MCP name
	sd.MCPNameEnvOverride = os.Getenv("OPENTRACE_MCP_NAME") != ""
	if sd.MCPNameEnvOverride {
		sd.MCPName = os.Getenv("OPENTRACE_MCP_NAME")
	} else if s.settingsStore != nil {
		if v, err := s.settingsStore.GetMCPName(r.Context()); err == nil && v != "" {
			sd.MCPName = v
		}
	}

	webviews.SettingsPage(layout, sd).Render(r.Context(), w)
}

func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Setup", "setup")
	setup := webviews.SetupData{
		APIKey: s.getEffectiveAPIKey(r.Context()),
	}
	webviews.SetupPage(layout, setup).Render(r.Context(), w)
}

func (s *Server) handleUsersPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Users", "users")
	var users []store.User
	if s.userStore != nil {
		u, err := s.userStore.List(r.Context())
		if err == nil {
			users = u
		}
	}
	webviews.UsersPage(layout, users, layout.IsAdmin).Render(r.Context(), w)
}

func (s *Server) handleConnectorsPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Connectors", "connectors")
	var dataSources []store.DataSource
	if s.dsStore != nil {
		ds, err := s.dsStore.List(r.Context(), store.ListDataSourceParams{})
		if err == nil {
			dataSources = ds
		}
	}
	webviews.ConnectorsPage(layout, dataSources).Render(r.Context(), w)
}

func (s *Server) handleToolsPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Tools", "tools")

	var categories []webviews.ToolCategory
	if s.toolCatalog != nil {
		for _, cat := range s.toolCatalog.Categories() {
			tc := webviews.ToolCategory{Name: cat.Name, Description: cat.Description}
			for _, t := range cat.Tools {
				tc.Tools = append(tc.Tools, webviews.ToolInfo{
					Name:        t.Name,
					Description: t.Description,
					Access:      t.Access,
					Requires:    t.Requires,
				})
			}
			categories = append(categories, tc)
		}
	}
	if s.registry != nil {
		dynamicTools := s.registry.AllTools()
		if len(dynamicTools) > 0 {
			dynCat := webviews.ToolCategory{
				Name:        "Connector Queries",
				Description: "Dynamic tools registered by active database connectors",
			}
			for _, t := range dynamicTools {
				dynCat.Tools = append(dynCat.Tools, webviews.ToolInfo{
					Name:        t.Name,
					Description: t.Description,
					Access:      "admin",
					Requires:    "database connector",
				})
			}
			categories = append(categories, dynCat)
		}
	}

	webviews.ToolsPage(layout, categories).Render(r.Context(), w)
}

func (s *Server) handleSessionsPage(w http.ResponseWriter, r *http.Request) {
	layout := s.layoutData(r, "Sessions", "sessions")

	page := webviews.SessionsPageData{
		Status: r.URL.Query().Get("status"),
	}

	if s.investigationSessionStore != nil {
		statusFilter := store.InvestigationSessionStatus(r.URL.Query().Get("status"))

		sessions, err := s.investigationSessionStore.List(r.Context(), store.ListInvestigationSessionParams{
			Status: statusFilter,
			Limit:  100,
		})
		if err == nil {
			page.Sessions = sessions
		}

		stats, err := s.investigationSessionStore.Stats(r.Context())
		if err == nil {
			page.Stats = stats
		}
	}

	webviews.SessionsPage(layout, page).Render(r.Context(), w)
}

func (s *Server) handleSessionDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	if s.investigationSessionStore == nil {
		writeError(w, http.StatusNotFound, "sessions not available")
		return
	}

	sess, err := s.investigationSessionStore.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get session")
		return
	}

	var activities []store.MCPActivityEvent
	if s.mcpActivityStore != nil {
		activities, _ = s.mcpActivityStore.ListByInvestigationSession(r.Context(), id)
	}

	layout := s.layoutData(r, "Session Detail", "sessions")
	detail := webviews.SessionDetailData{
		Session: sess,
		Events:  activities,
	}

	webviews.SessionDetailPage(layout, detail).Render(r.Context(), w)
}

