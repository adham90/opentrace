// Package cli provides HTTP API endpoints for the OpenTrace CLI tools
// (opentrace status, opentrace logs, opentrace top).
//
// All endpoints are mounted under /api/cli/ with API key authentication.
package cli

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/domain/overview"
	"github.com/adham90/opentrace/internal/version"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

// Module describes the CLI API domain.
var Module = server.Module{
	Name:  "cli",
	Mount: mount,
}

func mount(_ chi.Router, deps *server.Deps) {
	h := &handler{
		deps:      deps,
		startedAt: deps.StartedAt,
	}

	// Build overview service from available stores
	var opts []overview.Option
	if deps.LogStore != nil {
		opts = append(opts, overview.WithLogCounter(deps.LogStore))
	}
	if deps.ErrorGroupStore != nil {
		opts = append(opts, overview.WithErrorCounter(deps.ErrorGroupStore))
	}
	if deps.WatchStore != nil {
		opts = append(opts, overview.WithAlertCounter(deps.WatchStore))
	}
	if deps.HealthCheckStore != nil {
		opts = append(opts, overview.WithHealthSummariser(deps.HealthCheckStore))
	}
	if deps.DSStore != nil {
		opts = append(opts, overview.WithConnectorLister(deps.DSStore))
	}
	if deps.ServerStore != nil {
		opts = append(opts, overview.WithServerLister(deps.ServerStore))
	}
	h.overview = overview.NewService(opts...)

	// Mount all CLI API routes on the API router with API key auth
	if deps.APIRouter != nil && deps.APIKeyAuth != nil && deps.APIRateLimiter != nil {
		deps.APIRouter.Route("/cli", func(r chi.Router) {
			r.Use(deps.APIRateLimiter)
			r.Use(deps.APIKeyAuth)

			r.Get("/status", h.handleStatus)
			r.Get("/logs/tail", h.handleLogsTail)
			r.Get("/logs/stream", h.handleLogsStream)
			r.Get("/errors/top", h.handleErrorsTop)
			r.Get("/watches", h.handleWatches)
			r.Get("/ingestion/stats", h.handleIngestionStats)
		})
	}
}

type handler struct {
	deps      *server.Deps
	overview  *overview.Service
	startedAt time.Time
}

// statusResponse is the JSON response for GET /api/cli/status.
type statusResponse struct {
	Version       string                   `json:"version"`
	UptimeSeconds int64                    `json:"uptime_seconds"`
	Database      *databaseStatus          `json:"database,omitempty"`
	Logs          *overview.LogStats       `json:"logs,omitempty"`
	ErrorGroups   *errorGroupsStatus       `json:"error_groups,omitempty"`
	Services      []serviceInfo            `json:"services,omitempty"`
	Watches       *watchesStatus           `json:"watches,omitempty"`
	WatchAlerts   *overview.AlertStats     `json:"watch_alerts,omitempty"`
	Connectors    *overview.ConnectorStats `json:"connectors,omitempty"`
	Servers       *overview.ServerStats    `json:"servers,omitempty"`
	HealthChecks  *overview.HealthStats    `json:"healthchecks,omitempty"`
}

type databaseStatus struct {
	Healthy   bool  `json:"healthy"`
	SizeBytes int64 `json:"size_bytes"`
}

type errorGroupsStatus struct {
	Unresolved int `json:"unresolved"`
}

type serviceInfo struct {
	Name     string     `json:"name"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
	Status   string     `json:"status"`
}

type watchesStatus struct {
	Active    int `json:"active"`
	Triggered int `json:"triggered"`
	Resolved  int `json:"resolved"`
	Expired   int `json:"expired"`
}

func (h *handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get overview report (gracefully handles nil stores)
	report, err := h.overview.Status(ctx)
	if err != nil {
		slog.Error("cli status: overview failed", "error", err)
	}
	if report == nil {
		report = &overview.StatusReport{}
	}

	resp := statusResponse{
		Version:       version.Version,
		UptimeSeconds: int64(time.Since(h.startedAt).Seconds()),
	}

	// Database health
	if h.deps.DB != nil {
		dbHealthy := h.deps.DB.PingContext(ctx) == nil
		sizeBytes := getDatabaseSize(ctx, h.deps.DB)
		resp.Database = &databaseStatus{
			Healthy:   dbHealthy,
			SizeBytes: sizeBytes,
		}
	}

	// Logs
	resp.Logs = report.Logs

	// Error groups
	if report.ErrorGroups != nil {
		resp.ErrorGroups = &errorGroupsStatus{Unresolved: report.ErrorGroups.Unresolved}
	}

	// Services (from ServerStore)
	if h.deps.ServerStore != nil {
		servers, err := h.deps.ServerStore.List(ctx, store.ListServerParams{})
		if err == nil {
			for _, srv := range servers {
				name := srv.DisplayName
				if name == "" {
					name = srv.Hostname
				}
				resp.Services = append(resp.Services, serviceInfo{
					Name:     name,
					LastSeen: srv.LastSeenAt,
					Status:   string(srv.Status),
				})
			}
		}
	}

	// Watches (count by status)
	if h.deps.WatchStore != nil {
		resp.Watches = countWatchesByStatus(ctx, h.deps.WatchStore)
	}

	// Watch alerts
	resp.WatchAlerts = report.WatchAlerts
	resp.Connectors = report.Connectors
	resp.Servers = report.Servers
	resp.HealthChecks = report.HealthChecks

	// Content negotiation
	accept := r.Header.Get("Accept")
	if accept == "text/plain" {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		writeStatusPlainText(w, &resp)
		return
	}

	server.WriteJSON(w, http.StatusOK, resp)
}

func countWatchesByStatus(ctx context.Context, ws store.WatchStore) *watchesStatus {
	result := &watchesStatus{}
	watches, err := ws.List(ctx, store.ListWatchParams{Limit: 500})
	if err != nil {
		return result
	}
	for _, w := range watches {
		switch w.Status {
		case store.WatchStatusActive:
			result.Active++
		case store.WatchStatusTriggered:
			result.Triggered++
		case store.WatchStatusResolved:
			result.Resolved++
		case store.WatchStatusExpired:
			result.Expired++
		}
	}
	return result
}

func getDatabaseSize(ctx context.Context, db *sql.DB) int64 {
	var pageCount, pageSize int64
	row := db.QueryRowContext(ctx, "PRAGMA page_count")
	if err := row.Scan(&pageCount); err != nil {
		return 0
	}
	row = db.QueryRowContext(ctx, "PRAGMA page_size")
	if err := row.Scan(&pageSize); err != nil {
		return 0
	}
	return pageCount * pageSize
}

func writeStatusPlainText(w http.ResponseWriter, s *statusResponse) {
	fmt.Fprintf(w, "OpenTrace Status\n")
	fmt.Fprintf(w, "  Version:     %s\n", s.Version)

	days := s.UptimeSeconds / 86400
	hours := (s.UptimeSeconds % 86400) / 3600
	minutes := (s.UptimeSeconds % 3600) / 60
	if days > 0 {
		fmt.Fprintf(w, "  Uptime:      %dd %dh %dm\n", days, hours, minutes)
	} else if hours > 0 {
		fmt.Fprintf(w, "  Uptime:      %dh %dm\n", hours, minutes)
	} else {
		fmt.Fprintf(w, "  Uptime:      %dm\n", minutes)
	}

	if s.Database != nil {
		health := "healthy"
		if !s.Database.Healthy {
			health = "unhealthy"
		}
		fmt.Fprintf(w, "  Database:    %s (%dMB)\n", health, s.Database.SizeBytes/(1024*1024))
	}

	if s.Logs != nil {
		fmt.Fprintf(w, "  Logs:        %d (last 1h), %d errors\n", s.Logs.LastHour, s.Logs.ErrorsLastHour)
	}

	if s.Watches != nil {
		fmt.Fprintf(w, "  Watches:     %d active, %d triggered\n", s.Watches.Active, s.Watches.Triggered)
	}

	if s.WatchAlerts != nil {
		fmt.Fprintf(w, "  Alerts:      %d pending\n", s.WatchAlerts.Pending)
	}
}
