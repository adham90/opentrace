package web

import (
	"net/http"
	"sort"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// triageItem represents a single item in the triage inbox.
type triageItem struct {
	Type     string `json:"type"`     // "alert", "run", "connector", "server"
	Severity string `json:"severity"` // "critical", "warning", "info"
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Time     string `json:"time"`
	Link     string `json:"link"`
	ID       string `json:"id"`
}

// handleTriageAPI returns the triage inbox merging alerts, failed runs, error connectors, and offline servers.
func (s *Server) handleTriageAPI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var items []triageItem

	// 1. Unread alerts
	if s.alertStore != nil {
		alerts, err := s.alertStore.List(ctx, store.ListAlertParams{
			UnreadOnly: true,
			Limit:      10,
		})
		if err == nil {
			for _, a := range alerts {
				items = append(items, triageItem{
					Type:     "alert",
					Severity: severityToTriage(string(a.Severity)),
					Title:    a.Title,
					Detail:   a.WatcherTitle,
					Time:     a.CreatedAt.Format(time.RFC3339),
					Link:     "/alerts",
					ID:       a.ID.String(),
				})
			}
		}
	}

	// 2. Recent failed runs
	if s.runStore != nil {
		runs, err := s.runStore.ListRecentFailed(ctx, 5)
		if err == nil {
			for _, r := range runs {
				errDetail := ""
				if r.Error != nil {
					errDetail = *r.Error
					if len(errDetail) > 100 {
						errDetail = errDetail[:100]
					}
				}
				items = append(items, triageItem{
					Type:     "run",
					Severity: "warning",
					Title:    "Watcher run failed",
					Detail:   errDetail,
					Time:     r.CreatedAt.Format(time.RFC3339),
					Link:     "/watchers/" + r.WatcherID.String() + "/runs",
					ID:       r.ID.String(),
				})
			}
		}
	}

	// 3. Error connectors
	if s.dsStore != nil {
		connectors, err := s.dsStore.List(ctx, store.ListDataSourceParams{})
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
					items = append(items, triageItem{
						Type:     "connector",
						Severity: "warning",
						Title:    "Connector '" + c.Name + "' error",
						Detail:   detail,
						Time:     c.UpdatedAt.Format(time.RFC3339),
						Link:     "/connectors",
						ID:       c.ID.String(),
					})
				}
			}
		}
	}

	// 4. Offline servers
	if s.serverStore != nil {
		servers, err := s.serverStore.List(ctx)
		if err == nil {
			for _, srv := range servers {
				if srv.Status == store.ServerOffline {
					detail := "last seen: never"
					t := srv.CreatedAt
					if srv.LastSeenAt != nil {
						detail = "last seen " + srv.LastSeenAt.Format(time.RFC3339)
						t = *srv.LastSeenAt
					}
					items = append(items, triageItem{
						Type:     "server",
						Severity: "info",
						Title:    "Server '" + srv.Hostname + "' offline",
						Detail:   detail,
						Time:     t.Format(time.RFC3339),
						Link:     "/servers/" + srv.ID.String(),
						ID:       srv.ID.String(),
					})
				}
			}
		}
	}

	// Sort by severity (critical > warning > info) then by time (newest first)
	sort.Slice(items, func(i, j int) bool {
		si, sj := severityOrder(items[i].Severity), severityOrder(items[j].Severity)
		if si != sj {
			return si < sj
		}
		return items[i].Time > items[j].Time
	})

	// Limit to 20 items
	if len(items) > 20 {
		items = items[:20]
	}

	resp := map[string]any{
		"needs_attention": items,
	}

	// Include MCP stats if available
	if s.mcpActivityStore != nil {
		stats, err := s.mcpActivityStore.Stats(ctx)
		if err == nil {
			resp["mcp"] = stats
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func severityToTriage(sev string) string {
	switch sev {
	case "critical":
		return "critical"
	case "warning":
		return "warning"
	default:
		return "info"
	}
}

func severityOrder(sev string) int {
	switch sev {
	case "critical":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}
