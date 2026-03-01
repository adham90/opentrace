package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var tmplFuncs = template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"truncate": func(s string, n int) string {
		if len(s) <= n {
			return s
		}
		return s[:n] + "..."
	},
	"fmtDuration": func(secs int) string {
		if secs < 60 {
			return fmt.Sprintf("%ds", secs)
		}
		if secs < 3600 {
			return fmt.Sprintf("%dm %ds", secs/60, secs%60)
		}
		return fmt.Sprintf("%dh %dm", secs/3600, (secs%3600)/60)
	},
	"fmtFloat": func(f float64) string {
		return fmt.Sprintf("%.1f", f)
	},
	"deref": func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	},
	"derefStr": func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	},
	"derefInt": func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	},
	"derefFloat": func(p *float64) float64 {
		if p == nil {
			return 0
		}
		return *p
	},
	"derefTime": func(p *time.Time) time.Time {
		if p == nil {
			return time.Time{}
		}
		return *p
	},
	"timeAgo": func(t time.Time) string {
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
	},
}

var (
	dashboardTmpl  *template.Template
	loginTmpl      *template.Template
	registerTmpl   *template.Template
	profileTmpl    *template.Template
	settingsTmpl   *template.Template
	setupTmpl      *template.Template
	usersTmpl       *template.Template
	connectorsTmpl  *template.Template
	toolsTmpl       *template.Template
	sessionsTmpl       *template.Template
	sessionDetailTmpl  *template.Template
	onboardingTmpl     *template.Template
	logsPageTmpl       *template.Template
	errorsPageTmpl       *template.Template
	errorDetailPageTmpl  *template.Template
	watchersPageTmpl   *template.Template
	healthPageTmpl     *template.Template
)

func init() {
	// Each page gets layout + its own content template
	dashboardTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS,
		"templates/layout.html", "templates/dashboard.html"))
	loginTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/login.html"))
	registerTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/register.html"))
	profileTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/profile.html"))
	settingsTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/settings.html"))
	setupTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/setup.html"))
	usersTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/users.html"))
	connectorsTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/connectors.html"))
	toolsTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/tools.html"))
	sessionsTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS,
		"templates/layout.html", "templates/sessions.html"))
	sessionDetailTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS,
		"templates/layout.html", "templates/session_detail.html"))
	onboardingTmpl = template.Must(template.ParseFS(templateFS,
		"templates/layout_minimal.html", "templates/onboarding.html"))
	logsPageTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS,
		"templates/layout.html", "templates/logs.html"))
	errorsPageTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS,
		"templates/layout.html", "templates/errors.html"))
	errorDetailPageTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS,
		"templates/layout.html", "templates/error_detail.html"))
	watchersPageTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS,
		"templates/layout.html", "templates/watchers.html"))
	healthPageTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS,
		"templates/layout.html", "templates/health.html"))
}

// logsFragmentTmpl is used for rendering HTMX fragment responses for logs-list
var logsFragmentTmpl *template.Template

func init() {
	logsFragmentTmpl = template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS, "templates/logs.html"))
}

type LogFilters struct {
	Query            string
	Service          string
	Level            string
	EventType        string
	TimeRange        string // preset: 15m, 1h, 6h, 24h, 7d, or empty (all)
	Environment      string
	CommitHash       string
	RequestID        string
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

type dashboardData struct {
	SystemStatus dashboardSystemStatus    `json:"system_status"`
	LastSession  *dashboardLastSession    `json:"last_session"`
	Attention    []dashboardAttentionItem `json:"attention"`
	Glance       dashboardGlance          `json:"glance"`
	QuietMinutes int                      `json:"quiet_minutes"`
	LastIncident string                   `json:"last_incident"`
	Activity     []dashboardActivityItem  `json:"activity"`
}

type pageData struct {
	Title          string
	Nav            string
	ServerID       string
	DevMode        bool
	CSPNonce       string
	CSRFToken      string
	Connectors     interface{}
	Logs           []store.LogEntry
	LogFilters     LogFilters
	LogOffset      int
	LogLimit       int
	HasMore        bool
	MaxLogID       int64
	User           *store.User
	Users          []store.User
	DataSources    []store.DataSource
	ToolCategories interface{}
	IsAdmin        bool
	RetentionDays       int
	MetricRetentionDays int
	APIKey              string
	EnvKeyOverride      bool
	CORSOrigins         string
	CORSEnvOverride     bool
	MaxQueryRows        int
	StatementTimeoutMS  int
	QueryEnvOverride    bool
	MCPName             string
	MCPNameEnvOverride  bool
	// Sessions page
	Sessions       []store.InvestigationSession
	SessionStats   *store.InvestigationSessionStats
	SessionDetail  *store.InvestigationSession
	ActivityEvents []store.MCPActivityEvent

	// Dashboard
	DashSystemStatus *dashboardSystemStatus
	DashLastSession  *dashboardLastSession
	DashAttention    []dashboardAttentionItem
	DashGlance       *dashboardGlance
	DashQuietMinutes int
	DashLastIncident string
	DashActivity     []dashboardActivityItem

	// Errors page
	ErrorGroups []store.ErrorGroup
	ErrorGroup  *store.ErrorGroup
	ErrorLogs   []store.LogEntry

	// Watchers page
	Watches      []store.Watch
	WatchAlerts  []store.WatchAlert
	PendingCount int

	// Health page
	HealthChecks    []store.HealthCheck
	UptimeSummaries []store.UptimeSummary
}

func (s *Server) isDevMode() bool {
	return s.cfg != nil && s.cfg.DevMode
}

// newPageData creates a pageData with common fields populated from the request context.
func (s *Server) newPageData(r *http.Request, title, nav string) pageData {
	user := UserFromContext(r.Context())
	isAdmin := user != nil && user.Role == store.RoleAdmin
	var apiKey string
	if isAdmin {
		apiKey = s.getEffectiveAPIKey(r.Context())
	}
	return pageData{
		Title:     title,
		Nav:       nav,
		DevMode:   s.isDevMode(),
		CSPNonce:  CSPNonce(r.Context()),
		CSRFToken: CSRFToken(r.Context()),
		User:      user,
		IsAdmin:   isAdmin,
		APIKey:    apiKey,
	}
}

// getTemplate returns a freshly-parsed template from disk in dev mode,
// or the pre-compiled embedded template in production.
func (s *Server) getTemplate(fallback *template.Template, files ...string) *template.Template {
	if !s.isDevMode() {
		return fallback
	}
	t, err := template.New("").Funcs(tmplFuncs).ParseFiles(files...)
	if err != nil {
		return fallback
	}
	return t
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
				dd.LastSession = &dashboardLastSession{
					ID:      sess.ID,
					Intent:  sess.Intent,
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
					Detail:   truncateStr(eg.Message, 80),
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
					Title:    a.Summary,
					Detail:   fmt.Sprintf("%s = %.1f (threshold: %.1f)", a.TriggerMetric, a.TriggerValue, a.ThresholdValue),
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

	data := s.newPageData(r, "Dashboard", "dashboard")
	data.DashSystemStatus = &dd.SystemStatus
	data.DashLastSession = dd.LastSession
	data.DashAttention = dd.Attention
	data.DashGlance = &dd.Glance
	data.DashQuietMinutes = dd.QuietMinutes
	data.DashLastIncident = dd.LastIncident
	data.DashActivity = dd.Activity

	tmpl := s.getTemplate(dashboardTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/dashboard.html")
	tmpl.ExecuteTemplate(w, "layout", data)
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
		Environment:      filters.Environment,
		CommitHash:       filters.CommitHash,
		RequestID:        filters.RequestID,
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

	data := s.newPageData(r, "Logs", "logs")
	data.Logs = logEntries
	data.LogFilters = filters
	data.LogOffset = offset
	data.LogLimit = limit
	data.HasMore = hasMore
	data.MaxLogID = maxID

	tmpl := s.getTemplate(logsPageTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/logs.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleErrorsPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Errors", "errors")
	if s.errorGroupStore != nil {
		status := store.ErrorGroupStatus(r.URL.Query().Get("status"))
		groups, err := s.errorGroupStore.List(r.Context(), store.ListErrorGroupParams{
			Status:  status,
			Service: r.URL.Query().Get("service"),
			SortBy:  "last_seen_at",
			Limit:   100,
		})
		if err == nil {
			data.ErrorGroups = groups
		}
	}
	tmpl := s.getTemplate(errorsPageTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/errors.html")
	tmpl.ExecuteTemplate(w, "layout", data)
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

	data := s.newPageData(r, eg.ExceptionClass, "errors")
	data.ErrorGroup = eg

	// Fetch recent log entries for this error fingerprint
	if s.logStore != nil {
		logs, err := s.logStore.Search(r.Context(), store.LogSearchParams{
			ErrorFingerprint: fp,
			Limit:            25,
		})
		if err == nil {
			data.ErrorLogs = logs
		}
	}

	tmpl := s.getTemplate(errorDetailPageTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/error_detail.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleWatchersPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Watchers", "watchers")
	if s.watchStore != nil {
		watches, err := s.watchStore.List(r.Context(), store.ListWatchParams{Limit: 100})
		if err == nil {
			data.Watches = watches
		}
		alerts, err := s.watchStore.ListAlerts(r.Context(), "", "", 20)
		if err == nil {
			data.WatchAlerts = alerts
		}
		pending, err := s.watchStore.CountPendingAlerts(r.Context())
		if err == nil {
			data.PendingCount = pending
		}
	}
	tmpl := s.getTemplate(watchersPageTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/watchers.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleHealthPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Health", "health")
	if s.healthCheckStore != nil {
		checks, err := s.healthCheckStore.List(r.Context(), store.ListHealthCheckParams{})
		if err == nil {
			data.HealthChecks = checks
		}
		oneDayAgo := time.Now().Add(-24 * time.Hour)
		summaries, err := s.healthCheckStore.UptimeSummaries(r.Context(), oneDayAgo)
		if err == nil {
			data.UptimeSummaries = summaries
		}
	}
	tmpl := s.getTemplate(healthPageTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/health.html")
	tmpl.ExecuteTemplate(w, "layout", data)
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
		Environment:      filters.Environment,
		CommitHash:       filters.CommitHash,
		RequestID:        filters.RequestID,
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

	data := pageData{
		Logs:       logs,
		LogFilters: filters,
		LogOffset:  offset,
		LogLimit:   limit,
		HasMore:    hasMore,
		MaxLogID:   maxID,
	}

	w.Header().Set("Content-Type", "text/html")
	ft := s.getTemplate(logsFragmentTmpl, "internal/web/templates/logs.html")
	if offset > 0 {
		ft.ExecuteTemplate(w, "logs-rows", data)
	} else {
		ft.ExecuteTemplate(w, "logs-list", data)
	}
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
		Environment:      filters.Environment,
		CommitHash:       filters.CommitHash,
		RequestID:        filters.RequestID,
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

	ft := s.getTemplate(logsFragmentTmpl, "internal/web/templates/logs.html")
	ft.ExecuteTemplate(w, "logs-new", pageData{Logs: logs, MaxLogID: maxID})
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

func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Settings", "settings")
	if s.settingsStore != nil {
		settings, err := s.settingsStore.GetRetention(r.Context())
		if err == nil {
			data.RetentionDays = settings.RetentionDays
			data.MetricRetentionDays = settings.MetricRetentionDays
		} else {
			data.RetentionDays = 30
		}
	} else {
		data.RetentionDays = 30
	}
	data.EnvKeyOverride = s.cfg != nil && s.cfg.APIKey != ""
	data.CORSEnvOverride = s.cfg != nil && len(s.cfg.CORSAllowedOrigins) > 0
	if data.CORSEnvOverride {
		data.CORSOrigins = strings.Join(s.cfg.CORSAllowedOrigins, ",")
	} else if s.settingsStore != nil {
		if val, err := s.settingsStore.GetCORSOrigins(r.Context()); err == nil {
			data.CORSOrigins = val
		}
	}

	// Query guardrails
	data.QueryEnvOverride = os.Getenv("OPENTRACE_MAX_QUERY_ROWS") != "" || os.Getenv("OPENTRACE_STATEMENT_TIMEOUT_MS") != ""
	data.MaxQueryRows = 500
	data.StatementTimeoutMS = 5000
	if data.QueryEnvOverride && s.cfg != nil {
		data.MaxQueryRows = s.cfg.MaxQueryRows
		data.StatementTimeoutMS = s.cfg.StatementTimeoutMS
	} else if s.settingsStore != nil {
		if v, err := s.settingsStore.GetMaxQueryRows(r.Context()); err == nil && v > 0 {
			data.MaxQueryRows = v
		}
		if v, err := s.settingsStore.GetStatementTimeout(r.Context()); err == nil && v > 0 {
			data.StatementTimeoutMS = v
		}
	}

	// MCP name
	data.MCPNameEnvOverride = os.Getenv("OPENTRACE_MCP_NAME") != ""
	data.MCPName = "opentrace"
	if data.MCPNameEnvOverride {
		data.MCPName = os.Getenv("OPENTRACE_MCP_NAME")
	} else if s.settingsStore != nil {
		if v, err := s.settingsStore.GetMCPName(r.Context()); err == nil && v != "" {
			data.MCPName = v
		}
	}

	tmpl := s.getTemplate(settingsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/settings.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Setup", "setup")
	tmpl := s.getTemplate(setupTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/setup.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleUsersPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Users", "users")
	if s.userStore != nil {
		users, err := s.userStore.List(r.Context())
		if err == nil {
			data.Users = users
		}
	}
	tmpl := s.getTemplate(usersTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/users.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleConnectorsPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Connectors", "connectors")
	if s.dsStore != nil {
		ds, err := s.dsStore.List(r.Context(), store.ListDataSourceParams{})
		if err == nil {
			data.DataSources = ds
		}
	}
	tmpl := s.getTemplate(connectorsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/connectors.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleToolsPage(w http.ResponseWriter, r *http.Request) {
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

	data := s.newPageData(r, "Tools", "tools")
	data.ToolCategories = categories
	tmpl := s.getTemplate(toolsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/tools.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handleSessionsPage(w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(r, "Sessions", "sessions")

	if s.investigationSessionStore != nil {
		statusFilter := store.InvestigationSessionStatus(r.URL.Query().Get("status"))

		sessions, err := s.investigationSessionStore.List(r.Context(), store.ListInvestigationSessionParams{
			Status: statusFilter,
			Limit:  100,
		})
		if err == nil {
			data.Sessions = sessions
		}

		stats, err := s.investigationSessionStore.Stats(r.Context())
		if err == nil {
			data.SessionStats = stats
		}
	}

	tmpl := s.getTemplate(sessionsTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/sessions.html")
	tmpl.ExecuteTemplate(w, "layout", data)
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

	data := s.newPageData(r, "Session Detail", "sessions")
	data.SessionDetail = sess
	data.ActivityEvents = activities

	tmpl := s.getTemplate(sessionDetailTmpl,
		"internal/web/templates/layout.html",
		"internal/web/templates/session_detail.html")
	tmpl.ExecuteTemplate(w, "layout", data)
}

