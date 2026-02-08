package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/opentrace/opentrace/internal/store"
)

type createConnectorRequest struct {
	Type   store.ConnectorType `json:"type"`
	Name   string              `json:"name"`
	Config map[string]any      `json:"config"`
}

func (s *Server) handleCreateConnector(w http.ResponseWriter, r *http.Request) {
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

	ds, err := s.store.Create(r.Context(), store.CreateDataSourceParams{
		Type:   req.Type,
		Name:   req.Name,
		Config: req.Config,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create connector")
		return
	}

	writeJSON(w, http.StatusCreated, ds)
}

func (s *Server) handleListConnectors(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list connectors")
		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleTestConnector(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid connector ID")
		return
	}

	ds, err := s.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connector not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get connector")
		return
	}

	// For now, just update status to connected (actual connector test comes later)
	status := store.StatusConnected
	now := time.Now()
	msg := "connection successful"
	updated, err := s.store.Update(r.Context(), ds.ID, store.UpdateDataSourceParams{
		Status:        &status,
		StatusMessage: &msg,
		LastTestedAt:  &now,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update connector status")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteConnector(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid connector ID")
		return
	}

	if err := s.store.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connector not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete connector")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
