package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// SessionContext is the context payload sent to an MCP client on connect.
// It tells the agent about open investigations, recent alerts, and system health
// so it can pick up where the last session left off.
type SessionContext struct {
	OpenInvestigations []InvestigationSummary `json:"open_investigations,omitempty"`
	RecentAlerts       []AlertSummary         `json:"recent_alerts,omitempty"`
	SystemStatus       SystemStatusSummary    `json:"system_status"`
	RecentDeploy       *DeploySummary         `json:"recent_deploy,omitempty"`
}

// InvestigationSummary is a compact view of an open investigation.
type InvestigationSummary struct {
	ID             string   `json:"id"`
	Intent         string   `json:"intent"`
	Service        string   `json:"service"`
	Status         string   `json:"status"`
	LastActivity   string   `json:"last_activity"`
	Summary        string   `json:"summary,omitempty"`
	NextSteps      []string `json:"next_steps,omitempty"`
	RelatedErrors  int      `json:"related_errors"`
}

// AlertSummary is a compact view of a recent alert.
type AlertSummary struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Time     string `json:"time"`
	Status   string `json:"status"`
}

// SystemStatusSummary is a quick health snapshot.
type SystemStatusSummary struct {
	Overall              string `json:"overall"` // "ok", "warn", "critical"
	ErrorsLastHour       int    `json:"errors_last_hour"`
	UnresolvedErrors     int    `json:"unresolved_error_groups"`
	WatchersTriggered    int    `json:"watchers_triggered"`
	HealthChecksDown     int    `json:"health_checks_down"`
}

// DeploySummary is a compact view of the most recent deploy.
type DeploySummary struct {
	Service    string `json:"service"`
	Commit     string `json:"commit"`
	Author     string `json:"author"`
	DeployedAt string `json:"deployed_at"`
	TimeAgo    string `json:"time_ago"`
}

// BuildSessionContext assembles the context to send to an MCP client on connect.
func BuildSessionContext(ctx context.Context, stores store.Stores) *SessionContext {
	sc := &SessionContext{}

	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)

	// Open investigations
	if stores.InvestigationSessionStore != nil {
		sessions, err := stores.InvestigationSessionStore.List(ctx, store.ListInvestigationSessionParams{
			Status: store.InvestigationStatusOpen,
			Limit:  5,
		})
		if err == nil {
			for _, s := range sessions {
				is := InvestigationSummary{
					ID:           s.ID,
					Intent:       s.Intent,
					Service:      s.PrimaryService,
					Status:       string(s.Status),
					LastActivity: formatTimeAgo(s.LastActivityAt),
					Summary:      s.Summary,
				}
				sc.OpenInvestigations = append(sc.OpenInvestigations, is)
			}
		}
	}

	// Recent alerts
	if stores.WatchStore != nil {
		alerts, err := stores.WatchStore.ListAlerts(ctx, "", "pending", 5)
		if err == nil {
			for _, a := range alerts {
				sc.RecentAlerts = append(sc.RecentAlerts, AlertSummary{
					Type:   "watcher",
					Title:  a.Summary,
					Time:   formatTimeAgo(a.CreatedAt),
					Status: a.Status,
				})
			}
		}
	}

	// System status
	status := SystemStatusSummary{Overall: "ok"}

	if stores.LogStore != nil {
		counts, err := stores.LogStore.CountByLevel(ctx, store.LogCountParams{
			Since: oneHourAgo, Until: now,
		})
		if err == nil {
			for level, count := range counts {
				if level == "error" || level == "fatal" {
					status.ErrorsLastHour += count
				}
			}
		}
	}

	if stores.ErrorGroupStore != nil {
		count, err := stores.ErrorGroupStore.Count(ctx, store.ErrorGroupUnresolved)
		if err == nil {
			status.UnresolvedErrors = count
		}
	}

	if stores.WatchStore != nil {
		triggered, err := stores.WatchStore.List(ctx, store.ListWatchParams{Status: store.WatchStatusTriggered})
		if err == nil {
			status.WatchersTriggered = len(triggered)
		}
	}

	// Determine overall status
	if status.ErrorsLastHour > 10 || status.WatchersTriggered > 0 || status.HealthChecksDown > 0 {
		status.Overall = "critical"
	} else if status.ErrorsLastHour > 3 || status.UnresolvedErrors > 10 {
		status.Overall = "warn"
	}

	sc.SystemStatus = status

	// Recent deploy (within last 2 hours)
	if stores.DeployStore != nil {
		deploys, err := stores.DeployStore.GetRecent(ctx, "", 1)
		if err == nil && len(deploys) > 0 {
			d := deploys[0]
			if time.Since(d.DeployedAt) < 2*time.Hour {
				commit := d.CommitHash
				if len(commit) > 8 {
					commit = commit[:8]
				}
				sc.RecentDeploy = &DeploySummary{
					Service:    d.Service,
					Commit:     commit,
					Author:     d.Author,
					DeployedAt: d.DeployedAt.Format(time.RFC3339),
					TimeAgo:    formatTimeAgo(d.DeployedAt),
				}
			}
		}
	}

	return sc
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
