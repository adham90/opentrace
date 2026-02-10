package web

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// handleRegisterServer handles POST /api/servers/register.
func (s *Server) handleRegisterServer(w http.ResponseWriter, r *http.Request) {
	var params store.RegisterServerParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if params.Hostname == "" {
		writeError(w, http.StatusBadRequest, "hostname is required")
		return
	}

	srv, err := s.serverStore.Register(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register server")
		return
	}

	s.ensureMetricsConnector(r.Context())

	writeJSON(w, http.StatusOK, map[string]any{
		"server_id": srv.ID,
		"status":    srv.Status,
	})
}

// metricBatchRequest is the JSON body for POST /api/servers/{id}/metrics.
type metricBatchRequest struct {
	Timestamp time.Time            `json:"timestamp"`
	Samples   []store.MetricSample `json:"samples"`
}

// handlePushMetrics handles POST /api/servers/{id}/metrics.
func (s *Server) handlePushMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server ID")
		return
	}

	var batch metricBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if batch.Timestamp.IsZero() {
		batch.Timestamp = time.Now().UTC()
	}

	ctx := r.Context()

	n, err := s.metricStore.BatchInsert(ctx, id, batch.Timestamp, batch.Samples)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to insert metrics")
		return
	}

	if err := s.serverStore.UpdateHeartbeat(ctx, id); err != nil {
		// Non-fatal: metrics were saved
	}

	writeJSON(w, http.StatusCreated, map[string]int{"count": n})
}

// handleListServers handles GET /api/servers.
func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	servers, err := s.serverStore.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list servers")
		return
	}
	writeJSON(w, http.StatusOK, servers)
}

// handleGetServer handles GET /api/servers/{id}.
func (s *Server) handleGetServer(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server ID")
		return
	}

	srv, err := s.serverStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "server not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get server")
		return
	}

	writeJSON(w, http.StatusOK, srv)
}

// handleDeleteServer handles DELETE /api/servers/{id}.
func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server ID")
		return
	}

	if err := s.serverStore.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "server not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete server")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleQueryMetrics handles GET /api/servers/{id}/metrics.
func (s *Server) handleQueryMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server ID")
		return
	}

	q := store.MetricQuery{ServerID: id}
	q.MetricName = r.URL.Query().Get("name")

	if v := r.URL.Query().Get("start"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.Start = &t
		}
	}
	if v := r.URL.Query().Get("end"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.End = &t
		}
	}

	points, err := s.metricStore.Query(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query metrics")
		return
	}

	writeJSON(w, http.StatusOK, points)
}

// ensureMetricsConnector auto-creates and registers a MetricsConnector when
// the first server registers, following the same pattern as ensureLogsConnector.
func (s *Server) ensureMetricsConnector(ctx context.Context) {
	if s.registry.Get(connector.ConnectorServerMetrics) != nil {
		return
	}

	s.metricsConnMu.Lock()
	defer s.metricsConnMu.Unlock()

	if s.registry.Get(connector.ConnectorServerMetrics) != nil {
		return
	}

	// Create DB row if it doesn't exist
	sources, err := s.dsStore.List(ctx)
	if err != nil {
		log.Printf("WARN: ensureMetricsConnector: failed to list data sources: %v", err)
		return
	}
	var dsID *store.DataSource
	for i := range sources {
		if sources[i].Type == store.ConnectorServerMetrics {
			dsID = &sources[i]
			break
		}
	}
	if dsID == nil {
		created, err := s.dsStore.Create(ctx, store.CreateDataSourceParams{
			Type:   store.ConnectorServerMetrics,
			Name:   "Server Metrics",
			Config: map[string]any{},
		})
		if err != nil {
			log.Printf("WARN: ensureMetricsConnector: failed to create data source: %v", err)
			return
		}
		dsID = created
	}

	mc := connector.NewMetricsConnector(s.serverStore, s.metricStore)
	s.registry.Register(mc)

	connected := store.StatusConnected
	if _, err := s.dsStore.Update(ctx, dsID.ID, store.UpdateDataSourceParams{
		Status: &connected,
	}); err != nil {
		log.Printf("WARN: ensureMetricsConnector: failed to update status: %v", err)
	}

	log.Printf("INFO: auto-registered server metrics connector (data source %s)", dsID.ID)
}
