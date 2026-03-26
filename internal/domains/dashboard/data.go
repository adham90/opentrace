package dashboard

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
)

// ── Dashboard data types ─────────────────────────────────────────

type systemStatus struct {
	ServicesUp    int    `json:"services_up"`
	ServicesTotal int    `json:"services_total"`
	ErrorsPerHour int   `json:"errors_per_hour"`
	LogsPerHour   int   `json:"logs_per_hour"`
	WatchersActive int  `json:"watchers_active"`
	InvestOpen     int  `json:"invest_open"`
	OverallStatus  string `json:"overall_status"` // "ok", "warn", "critical"
}

type lastSession struct {
	ID      string `json:"id"`
	Intent  string `json:"intent"`
	Service string `json:"service"`
	Status  string `json:"status"`
	TimeAgo string `json:"time_ago"`
}

type attentionItem struct {
	Severity string `json:"severity"` // "error", "warn", "info"
	Type     string `json:"type"`     // "error", "health", "watch"
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Velocity string `json:"velocity"` // "accelerating", "slowing", "stable"
	Time     string `json:"time"`
	Link     string `json:"link"`
}

type glance struct {
	TotalLogs       int     `json:"total_logs"`
	ErrorGroups     int     `json:"error_groups"`
	WatcherTriggers int     `json:"watcher_triggers"`
	Investigations  int     `json:"investigations"`
	Uptime          float64 `json:"uptime"`
}

type activityItem struct {
	Status string `json:"status"` // "ok", "error", "warn", "info"
	Type   string `json:"type"`
	Desc   string `json:"desc"`
	Time   string `json:"time"`
}

// signal represents a service/endpoint health dot.
type signal struct {
	Name   string  `json:"name"`
	Status string  `json:"status"` // "up", "down", "degraded"
	Uptime float64 `json:"uptime"`
	AvgMs  float64 `json:"avg_ms"`
}

// timelineItem represents an event in the system timeline.
type timelineItem struct {
	Icon    string `json:"icon"`    // "investigation", "alert", "deploy", "error", "resolved"
	Status  string `json:"status"`  // "ok", "error", "warn", "info"
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	Time    string `json:"time"`
	Link    string `json:"link"`
	SortKey int64  `json:"-"` // unix timestamp for sorting
}

type endpoint struct {
	Method    string  `json:"method"`
	Path      string  `json:"path"`
	Requests  int     `json:"requests"`
	ErrorRate float64 `json:"error_rate"`
	AvgMs     float64 `json:"avg_ms"`
	P95Ms     float64 `json:"p95_ms"`
}

type traffic struct {
	TotalRequests int     `json:"total_requests"`
	ErrorRate     float64 `json:"error_rate"`
	AvgMs         float64 `json:"avg_ms"`
	P95Ms         float64 `json:"p95_ms"`
}

type serverInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "online", "offline", "unknown"
}

type sparkBucket struct {
	Hour       int `json:"hour"`
	ErrorCount int `json:"error_count"`
	Total      int `json:"total"`
}

type dashboardData struct {
	SystemStatus   systemStatus    `json:"system_status"`
	LastSession    *lastSession    `json:"last_session"`
	Attention      []attentionItem `json:"attention"`
	Glance         glance          `json:"glance"`
	QuietMinutes   int             `json:"quiet_minutes"`
	LastIncident   string          `json:"last_incident"`
	Activity       []activityItem  `json:"activity"`
	Signals        []signal        `json:"signals"`
	Timeline       []timelineItem  `json:"timeline"`
	Traffic        *traffic        `json:"traffic"`
	TopEndpoints   []endpoint      `json:"top_endpoints"`
	Servers        []serverInfo    `json:"servers"`
	ErrorSpark     []sparkBucket   `json:"error_spark"`
}

// gatherDashboardData collects all data for the dashboard from stores.
// Each store query is independent -- nil stores or errors result in zero/empty values.
func (h *handler) gatherDashboardData(r *http.Request) dashboardData {
	ctx := r.Context()
	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)
	oneDayAgo := now.Add(-24 * time.Hour)

	dd := dashboardData{}

	// ── System Status ──────────────────────────────────────────
	ss := &dd.SystemStatus
	ss.OverallStatus = "ok"

	if h.logStore != nil {
		counts, err := h.logStore.CountByLevel(ctx, store.LogCountParams{
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

	if h.healthCheckStore != nil {
		checks, err := h.healthCheckStore.List(ctx, store.ListHealthCheckParams{})
		if err == nil {
			ss.ServicesTotal = len(checks)
		}
		results, err := h.healthCheckStore.LatestResults(ctx, "", 100)
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

	if h.watchStore != nil {
		watches, err := h.watchStore.List(ctx, store.ListWatchParams{Status: store.WatchStatusActive})
		if err == nil {
			ss.WatchersActive = len(watches)
		}
	}

	if h.investigationSessionStore != nil {
		stats, err := h.investigationSessionStore.Stats(ctx)
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
	if h.investigationSessionStore != nil {
		user := server.UserFromContext(ctx)
		if user != nil {
			sess, err := h.investigationSessionStore.FindRecent(ctx, store.FindRecentSessionParams{
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
				dd.LastSession = &lastSession{
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
	if h.errorGroupStore != nil {
		groups, err := h.errorGroupStore.List(ctx, store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  3,
			SortBy: "last_seen_at",
		})
		if err == nil {
			for _, eg := range groups {
				dd.Attention = append(dd.Attention, attentionItem{
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

	if h.healthCheckStore != nil {
		results, err := h.healthCheckStore.LatestResults(ctx, "", 100)
		if err == nil {
			seen := make(map[string]bool)
			for _, r := range results {
				if seen[r.HealthCheckID] {
					continue
				}
				seen[r.HealthCheckID] = true
				if r.Status == store.HealthCheckDown {
					dd.Attention = append(dd.Attention, attentionItem{
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

	if h.watchStore != nil {
		alerts, err := h.watchStore.ListAlerts(ctx, "", "pending", 3)
		if err == nil {
			for _, a := range alerts {
				dd.Attention = append(dd.Attention, attentionItem{
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
	if h.logStore != nil {
		counts, err := h.logStore.CountByLevel(ctx, store.LogCountParams{
			Since: oneDayAgo, Until: now,
		})
		if err == nil {
			for _, count := range counts {
				dd.Glance.TotalLogs += count
			}
		}
	}
	if h.errorGroupStore != nil {
		groups, err := h.errorGroupStore.List(ctx, store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
		})
		if err == nil {
			dd.Glance.ErrorGroups = len(groups)
		}
	}
	if h.watchStore != nil {
		triggered, err := h.watchStore.List(ctx, store.ListWatchParams{Status: store.WatchStatusTriggered})
		if err == nil {
			dd.Glance.WatcherTriggers = len(triggered)
		}
	}
	if h.investigationSessionStore != nil {
		stats, err := h.investigationSessionStore.Stats(ctx)
		if err == nil && stats != nil {
			dd.Glance.Investigations = stats.TotalSessions
		}
	}
	if h.healthCheckStore != nil {
		summaries, err := h.healthCheckStore.UptimeSummaries(ctx, oneDayAgo)
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
	if h.investigationSessionStore != nil {
		sessions, err := h.investigationSessionStore.List(ctx, store.ListInvestigationSessionParams{
			Limit: 3,
		})
		if err == nil {
			for _, sess := range sessions {
				status := "info"
				if sess.Status == store.InvestigationStatusResolved {
					status = "ok"
				}
				dd.Activity = append(dd.Activity, activityItem{
					Status: status,
					Type:   "investigation",
					Desc:   sess.Intent,
					Time:   formatTimeAgo(sess.LastActivityAt),
				})
			}
		}
	}
	if h.watchStore != nil {
		alerts, err := h.watchStore.ListAlerts(ctx, "", "", 3)
		if err == nil {
			for _, a := range alerts {
				status := "warn"
				if a.Status == "dismissed" {
					status = "ok"
				}
				dd.Activity = append(dd.Activity, activityItem{
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
	if h.healthCheckStore != nil {
		summaries, err := h.healthCheckStore.UptimeSummaries(ctx, oneDayAgo)
		if err == nil {
			for _, sum := range summaries {
				dd.Signals = append(dd.Signals, signal{
					Name:   sum.Name,
					Status: sum.CurrentStatus,
					Uptime: sum.UptimePct,
					AvgMs:  sum.AvgResponseMs,
				})
			}
		}
	}

	// ── Timeline (unified event stream) ───────────────────────
	if h.investigationSessionStore != nil {
		sessions, err := h.investigationSessionStore.List(ctx, store.ListInvestigationSessionParams{
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
				dd.Timeline = append(dd.Timeline, timelineItem{
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

	if h.watchStore != nil {
		alerts, err := h.watchStore.ListAlerts(ctx, "", "", 5)
		if err == nil {
			for _, a := range alerts {
				status := "warn"
				icon := "alert"
				if a.Status == "dismissed" {
					status = "ok"
					icon = "resolved"
				}
				dd.Timeline = append(dd.Timeline, timelineItem{
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

	if h.deployStore != nil {
		deploys, err := h.deployStore.GetRecent(ctx, "", 3)
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
				dd.Timeline = append(dd.Timeline, timelineItem{
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
	if h.analyticsStore != nil {
		summary, err := h.analyticsStore.TrafficSummary(ctx, store.AnalyticsParams{
			Since: oneDayAgo, Until: now,
		})
		if err == nil && summary != nil && summary.TotalRequests > 0 {
			dd.Traffic = &traffic{
				TotalRequests: summary.TotalRequests,
				ErrorRate:     summary.ErrorRate,
				AvgMs:         summary.AvgDurationMs,
				P95Ms:         summary.P95DurationMs,
			}
		}
	}

	// ── Top Endpoints (by p95) ────────────────────────────────
	if h.analyticsStore != nil {
		eps, err := h.analyticsStore.TopEndpoints(ctx, store.TopEndpointParams{
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
				dd.TopEndpoints = append(dd.TopEndpoints, endpoint{
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
	if h.serverStore != nil {
		servers, err := h.serverStore.List(ctx, store.ListServerParams{})
		if err == nil {
			for _, srv := range servers {
				name := srv.DisplayName
				if name == "" {
					name = srv.Hostname
				}
				dd.Servers = append(dd.Servers, serverInfo{
					Name:   name,
					Status: string(srv.Status),
				})
			}
		}
	}

	// ── Error Sparkline (24h, hourly buckets) ─────────────────
	if h.logStore != nil {
		buckets, err := h.logStore.Histogram(ctx, store.LogHistogramParams{
			Since:    oneDayAgo,
			Until:    now,
			Interval: time.Hour,
			Level:    "ERROR",
		})
		if err == nil && len(buckets) > 0 {
			for i, b := range buckets {
				dd.ErrorSpark = append(dd.ErrorSpark, sparkBucket{
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
