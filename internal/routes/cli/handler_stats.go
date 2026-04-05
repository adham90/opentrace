package cli

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

type ingestionStatsResponse struct {
	Buckets []store.LogHistogramBucket `json:"buckets"`
}

func (h *handler) handleIngestionStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.deps.LogStore == nil {
		server.WriteJSON(w, http.StatusOK, ingestionStatsResponse{})
		return
	}

	since := time.Now().Add(-1 * time.Hour)
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := server.ParseSinceParam(v); err == nil {
			since = t
		}
	}

	interval := time.Minute
	if v := r.URL.Query().Get("bucket"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		}
	}

	service := r.URL.Query().Get("service")

	buckets, err := h.deps.LogStore.Histogram(ctx, store.LogHistogramParams{
		Since:    since,
		Until:    time.Now(),
		Interval: interval,
		Service:  service,
	})
	if err != nil {
		slog.Error("cli ingestion stats: histogram failed", "error", err)
		server.WriteError(w, http.StatusInternalServerError, "histogram query failed")
		return
	}

	server.WriteJSON(w, http.StatusOK, ingestionStatsResponse{Buckets: buckets})
}
