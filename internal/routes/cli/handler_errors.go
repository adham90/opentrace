package cli

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

type errorGroupsResponse struct {
	ErrorGroups []errorGroupEntry `json:"error_groups"`
}

type errorGroupEntry struct {
	Fingerprint     string    `json:"fingerprint"`
	ExceptionClass  string    `json:"exception_class"`
	Message         string    `json:"message"`
	Service         string    `json:"service"`
	OccurrenceCount int       `json:"occurrence_count"`
	FirstSeenAt     time.Time `json:"first_seen_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	ImpactScore     float64   `json:"impact_score"`
	UniqueUsers     int       `json:"unique_users"`
}

func (h *handler) handleErrorsTop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.deps.ErrorGroupStore == nil {
		server.WriteJSON(w, http.StatusOK, errorGroupsResponse{})
		return
	}

	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	since := time.Now().Add(-1 * time.Hour)
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := server.ParseSinceParam(v); err == nil {
			since = t
		}
	}

	service := r.URL.Query().Get("service")

	params := store.ListErrorGroupParams{
		Status:  store.ErrorGroupUnresolved,
		Since:   &since,
		SortBy:  "occurrence_count",
		Limit:   limit,
		Service: service,
	}

	groups, err := h.deps.ErrorGroupStore.List(ctx, params)
	if err != nil {
		slog.Error("cli errors top: list failed", "error", err)
		server.WriteError(w, http.StatusInternalServerError, "error group query failed")
		return
	}

	entries := make([]errorGroupEntry, 0, len(groups))
	for _, g := range groups {
		entries = append(entries, errorGroupEntry{
			Fingerprint:     g.Fingerprint,
			ExceptionClass:  g.ExceptionClass,
			Message:         g.Message,
			Service:         g.Service,
			OccurrenceCount: g.OccurrenceCount,
			FirstSeenAt:     g.FirstSeenAt,
			LastSeenAt:      g.LastSeenAt,
			ImpactScore:     g.ImpactScore,
			UniqueUsers:     g.UniqueUsers,
		})
	}

	server.WriteJSON(w, http.StatusOK, errorGroupsResponse{ErrorGroups: entries})
}
