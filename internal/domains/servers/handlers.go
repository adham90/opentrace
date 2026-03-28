package servers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

type handler struct {
	serverStore store.ServerStore
	metricStore store.MetricStore
	dsStore     store.DataSourceStore
	registry    *connector.Registry

	metricsConnMu sync.Mutex
}

func (h *handler) register(w http.ResponseWriter, r *http.Request) {
	var params store.RegisterServerParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if params.Hostname == "" {
		server.WriteError(w, http.StatusBadRequest, "hostname is required")
		return
	}

	srv, err := h.serverStore.Register(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to register server")
		return
	}

	h.ensureMetricsConnector(r.Context())

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"server_id": srv.ID,
		"status":    srv.Status,
	})
}

// metricBatchRequest is the JSON body for POST /api/servers/{id}/metrics.
type metricBatchRequest struct {
	Timestamp time.Time            `json:"timestamp"`
	Samples   []store.MetricSample `json:"samples"`
}

func (h *handler) pushMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid server ID")
		return
	}

	var batch metricBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if batch.Timestamp.IsZero() {
		batch.Timestamp = time.Now().UTC()
	}

	ctx := r.Context()

	n, err := h.metricStore.BatchInsert(ctx, id, batch.Timestamp, batch.Samples)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to insert metrics")
		return
	}

	if err := h.serverStore.UpdateHeartbeat(ctx, id); err != nil {
		// Non-fatal: metrics were saved
	}

	server.WriteJSON(w, http.StatusCreated, map[string]int{"count": n})
}

// ensureMetricsConnector auto-creates and registers a MetricsConnector when
// the first server registers, following the same pattern as ensureLogsConnector.
func (h *handler) ensureMetricsConnector(ctx context.Context) {
	if h.registry.Get(connector.ConnectorServerMetrics) != nil {
		return
	}

	h.metricsConnMu.Lock()
	defer h.metricsConnMu.Unlock()

	if h.registry.Get(connector.ConnectorServerMetrics) != nil {
		return
	}

	// Create DB row if it doesn't exist
	sources, err := h.dsStore.List(ctx, store.ListDataSourceParams{Type: store.ConnectorServerMetrics})
	if err != nil {
		slog.Warn("ensureMetricsConnector: failed to list data sources", "error", err)
		return
	}
	var dsID *store.DataSource
	if len(sources) > 0 {
		dsID = &sources[0]
	}
	if dsID == nil {
		created, err := h.dsStore.Create(ctx, store.CreateDataSourceParams{
			Type:   store.ConnectorServerMetrics,
			Name:   "Server Metrics",
			Config: map[string]any{},
		})
		if err != nil {
			slog.Warn("ensureMetricsConnector: failed to create data source", "error", err)
			return
		}
		dsID = created
	}

	mc := connector.NewMetricsConnector(h.serverStore, h.metricStore)
	h.registry.Register(mc)

	connected := store.StatusConnected
	if _, err := h.dsStore.Update(ctx, dsID.ID, store.UpdateDataSourceParams{
		Status: &connected,
	}); err != nil {
		slog.Warn("ensureMetricsConnector: failed to update status", "error", err)
	}

	slog.Info("auto-registered server metrics connector", "data_source_id", dsID.ID)
}
