package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

type createConnectorRequest struct {
	Type   store.ConnectorType `json:"type"`
	Name   string              `json:"name"`
	Config map[string]any      `json:"config"`
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func (s *Server) handleCreateConnectorAPI(w http.ResponseWriter, r *http.Request) {
	var req createConnectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Type == "" || req.Name == "" {
		writeError(w, http.StatusBadRequest, "type and name are required")
		return
	}
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	ds, err := s.dsStore.Create(r.Context(), store.CreateDataSourceParams{
		Type: req.Type, Name: req.Name, Config: req.Config,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create connector")
		return
	}
	writeJSON(w, http.StatusCreated, ds)
}

func (s *Server) handleListConnectors(w http.ResponseWriter, r *http.Request) {
	list, err := s.dsStore.List(r.Context(), store.ListDataSourceParams{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list connectors")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleTestConnectorAPI(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid connector ID")
		return
	}

	ds, err := s.dsStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connector not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get connector")
		return
	}

	// Create connector and test connection
	c, err := connector.CreateConnector(r.Context(), *ds, s.logStore, s.cfg)
	now := time.Now()
	if err != nil {
		status := store.StatusError
		msg := err.Error()
		s.dsStore.Update(r.Context(), ds.ID, store.UpdateDataSourceParams{
			Status: &status, StatusMessage: &msg, LastTestedAt: &now,
		})
		writeError(w, http.StatusUnprocessableEntity, "connector test failed: unable to initialize connector")
		return
	}

	if err := c.TestConnection(r.Context()); err != nil {
		c.Close()
		status := store.StatusError
		msg := err.Error()
		s.dsStore.Update(r.Context(), ds.ID, store.UpdateDataSourceParams{
			Status: &status, StatusMessage: &msg, LastTestedAt: &now,
		})
		writeError(w, http.StatusUnprocessableEntity, "connector test failed: unable to connect")
		return
	}

	// Register in registry on success
	s.registry.Register(c)

	status := store.StatusConnected
	msg := "connection successful"
	updated, err := s.dsStore.Update(r.Context(), ds.ID, store.UpdateDataSourceParams{
		Status: &status, StatusMessage: &msg, LastTestedAt: &now,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update connector status")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleGetConnectorAPI(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid connector ID")
		return
	}

	ds, err := s.dsStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connector not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get connector")
		return
	}

	writeJSON(w, http.StatusOK, ds)
}

type updateConnectorRequest struct {
	Name   *string        `json:"name,omitempty"`
	Config map[string]any `json:"config,omitempty"`
}

func (s *Server) handleUpdateConnectorAPI(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid connector ID")
		return
	}

	var req updateConnectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name != nil && *req.Name == "" {
		writeError(w, http.StatusBadRequest, "name cannot be empty")
		return
	}

	params := store.UpdateDataSourceParams{
		Name:   req.Name,
		Config: req.Config,
	}

	// Unregister old connector from registry when config changes
	if req.Config != nil {
		ds, err := s.dsStore.GetByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "connector not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to get connector")
			return
		}
		s.registry.Unregister(connector.ConnectorType(ds.Type))
	}

	updated, err := s.dsStore.Update(r.Context(), id, params)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connector not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update connector")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteConnectorAPI(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid connector ID")
		return
	}

	ds, err := s.dsStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connector not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get connector")
		return
	}

	s.registry.Unregister(connector.ConnectorType(ds.Type))

	if err := s.dsStore.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete connector")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
