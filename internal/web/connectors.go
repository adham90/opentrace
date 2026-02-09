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

// renderConnectorList sends an HTML fragment of the connector list (for HTMX swaps).
func (s *Server) renderConnectorList(w http.ResponseWriter, r *http.Request) {
	connectors, err := s.dsStore.List(r.Context())
	if err != nil {
		http.Error(w, "failed to list connectors", http.StatusInternalServerError)
		return
	}
	data := pageData{Connectors: connectors}
	w.Header().Set("Content-Type", "text/html")
	templates.ExecuteTemplate(w, "connector-list", data)
}

func (s *Server) handleCreateConnectorAPI(w http.ResponseWriter, r *http.Request) {
	// HTMX form sends form-encoded data
	if isHTMX(r) {
		r.ParseForm()
		configStr := r.FormValue("config")
		cfg := map[string]any{}
		if configStr != "" {
			json.Unmarshal([]byte(configStr), &cfg)
		}
		connType := store.ConnectorType(r.FormValue("type"))
		name := r.FormValue("name")
		if connType == "" || name == "" {
			http.Error(w, "type and name required", http.StatusBadRequest)
			return
		}
		_, err := s.dsStore.Create(r.Context(), store.CreateDataSourceParams{
			Type: connType, Name: name, Config: cfg,
		})
		if err != nil {
			http.Error(w, "failed to create", http.StatusInternalServerError)
			return
		}
		s.renderConnectorList(w, r)
		return
	}

	// JSON API
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
	list, err := s.dsStore.List(r.Context())
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
		if isHTMX(r) {
			s.renderConnectorList(w, r)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "connector test failed: "+err.Error())
		return
	}

	if err := c.TestConnection(r.Context()); err != nil {
		c.Close()
		status := store.StatusError
		msg := err.Error()
		s.dsStore.Update(r.Context(), ds.ID, store.UpdateDataSourceParams{
			Status: &status, StatusMessage: &msg, LastTestedAt: &now,
		})
		if isHTMX(r) {
			s.renderConnectorList(w, r)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "connector test failed: "+err.Error())
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

	if isHTMX(r) {
		s.renderConnectorList(w, r)
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

	if isHTMX(r) {
		s.renderConnectorList(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
