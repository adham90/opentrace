package cli

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

type watchesResponse struct {
	Watches []watchEntry  `json:"watches"`
	Alerts  alertsSummary `json:"alerts"`
}

type watchEntry struct {
	ID                  string    `json:"id"`
	Conditions          string    `json:"conditions"`
	Service             string    `json:"service,omitempty"`
	Status              string    `json:"status"`
	Urgency             string    `json:"urgency"`
	CurrentValue        *float64  `json:"current_value,omitempty"`
	ConsecutiveBreaches int       `json:"consecutive_breaches"`
	LastCheckedAt       *string   `json:"last_checked_at,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

type alertsSummary struct {
	Pending int          `json:"pending"`
	Recent  []alertEntry `json:"recent"`
}

type alertEntry struct {
	ID        string    `json:"id"`
	WatchID   string    `json:"watch_id"`
	Urgency   string    `json:"urgency"`
	Summary   string    `json:"summary"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *handler) handleWatches(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	resp := watchesResponse{}

	if h.deps.WatchStore != nil {
		watches, err := h.deps.WatchStore.List(ctx, store.ListWatchParams{Limit: 100})
		if err != nil {
			slog.Error("cli watches: list failed", "error", err)
		} else {
			for _, w := range watches {
				entry := watchEntry{
					ID:                  w.ID,
					Conditions:          store.ConditionsSummary(w.ConditionsJSON),
					Service:             w.Service,
					Status:              string(w.Status),
					Urgency:             string(w.Urgency),
					CurrentValue:        w.CurrentValue,
					ConsecutiveBreaches: w.ConsecutiveBreaches,
					CreatedAt:           w.CreatedAt,
				}
				if w.LastCheckedAt != nil {
					s := w.LastCheckedAt.Format(time.RFC3339)
					entry.LastCheckedAt = &s
				}
				resp.Watches = append(resp.Watches, entry)
			}
		}

		pendingCount, err := h.deps.WatchStore.CountPendingAlerts(ctx)
		if err == nil {
			resp.Alerts.Pending = pendingCount
		}

		// Get recent alerts (last 10 across all watches)
		alertWatches, _ := h.deps.WatchStore.List(ctx, store.ListWatchParams{
			Status: store.WatchStatusTriggered,
			Limit:  10,
		})
		for _, aw := range alertWatches {
			alerts, err := h.deps.WatchStore.ListAlerts(ctx, aw.ID, "pending", 5)
			if err != nil {
				continue
			}
			for _, a := range alerts {
				resp.Alerts.Recent = append(resp.Alerts.Recent, alertEntry{
					ID:        a.ID,
					WatchID:   a.WatchID,
					Urgency:   string(a.Urgency),
					Summary:   a.Summary,
					Status:    a.Status,
					CreatedAt: a.CreatedAt,
				})
			}
		}
	}

	server.WriteJSON(w, http.StatusOK, resp)
}
