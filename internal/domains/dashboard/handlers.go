package dashboard

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/views"
	webviews "github.com/adham90/opentrace/internal/web/views"
)

type handler struct {
	logStore                 store.LogStore
	watchStore               store.WatchStore
	healthCheckStore         store.HealthCheckStore
	errorGroupStore          store.ErrorGroupStore
	serverStore              store.ServerStore
	metricStore              store.MetricStore
	investigationSessionStore store.InvestigationSessionStore
	mcpActivityStore         store.MCPActivityStore
	analyticsStore           store.AnalyticsStore
	deployStore              store.DeployStore
	dsStore                  store.DataSourceStore
	db                       *sql.DB
	cfg                      *config.Config
}

func (h *handler) page(w http.ResponseWriter, r *http.Request) {
	dd := h.gatherDashboardData(r)

	user := server.UserFromContext(r.Context())
	isAdmin := user != nil && user.Role == store.RoleAdmin
	devMode := h.cfg != nil && h.cfg.DevMode
	layout := views.LayoutData{
		Title:     "Dashboard",
		Nav:       "dashboard",
		User:      user,
		IsAdmin:   isAdmin,
		CSRFToken: server.CSRFToken(r.Context()),
		DevMode:   devMode,
	}

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

func (h *handler) api(w http.ResponseWriter, r *http.Request) {
	dd := h.gatherDashboardData(r)
	server.WriteJSON(w, http.StatusOK, dd)
}

func (h *handler) overview(w http.ResponseWriter, r *http.Request) {
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

	// Logs stats (last hour) -- use COUNT query instead of fetching rows
	if h.logStore != nil {
		now := time.Now()
		logOneHourAgo := now.Add(-1 * time.Hour)
		counts, err := h.logStore.CountByLevel(ctx, store.LogCountParams{
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
	if h.dsStore != nil {
		connectors, err := h.dsStore.List(ctx, store.ListDataSourceParams{})
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
	if h.serverStore != nil {
		servers, err := h.serverStore.List(ctx, store.ListServerParams{})
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

	server.WriteJSON(w, http.StatusOK, stats)
}
