package connectors

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/views"
	webviews "github.com/adham90/opentrace/internal/web/views"
)

type handler struct {
	dsStore       store.DataSourceStore
	logStore      store.LogStore
	settingsStore store.SettingsStore
	registry      *connector.Registry
	cfg           *config.Config
}

type createConnectorRequest struct {
	Type   store.ConnectorType `json:"type"`
	Name   string              `json:"name"`
	Config map[string]any      `json:"config"`
}

type updateConnectorRequest struct {
	Name   *string        `json:"name,omitempty"`
	Config map[string]any `json:"config,omitempty"`
}

// ── Page handlers ────────────────────────────────────────────

func (h *handler) layoutData(r *http.Request, title, nav string) views.LayoutData {
	user := server.UserFromContext(r.Context())
	isAdmin := user != nil && user.Role == store.RoleAdmin
	return views.LayoutData{
		Title:   title,
		Nav:     nav,
		User:    user,
		IsAdmin: isAdmin,
		DevMode: h.cfg != nil && h.cfg.DevMode,
	}
}

func (h *handler) connectorsPage(w http.ResponseWriter, r *http.Request) {
	layout := h.layoutData(r, "Connectors", "connectors")
	var dataSources []store.DataSource
	if h.dsStore != nil {
		ds, err := h.dsStore.List(r.Context(), store.ListDataSourceParams{})
		if err == nil {
			dataSources = ds
		}
	}
	webviews.ConnectorsPage(layout, dataSources).Render(r.Context(), w)
}

// ── API handlers ─────────────────────────────────────────────

func (h *handler) listConnectors(w http.ResponseWriter, r *http.Request) {
	list, err := h.dsStore.List(r.Context(), store.ListDataSourceParams{})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list connectors")
		return
	}
	server.WriteJSON(w, http.StatusOK, list)
}

func (h *handler) getConnector(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid connector ID")
		return
	}

	ds, err := h.dsStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "connector not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to get connector")
		return
	}

	server.WriteJSON(w, http.StatusOK, ds)
}

func (h *handler) createConnector(w http.ResponseWriter, r *http.Request) {
	var req createConnectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Type == "" || req.Name == "" {
		server.WriteError(w, http.StatusBadRequest, "type and name are required")
		return
	}
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	ds, err := h.dsStore.Create(r.Context(), store.CreateDataSourceParams{
		Type: req.Type, Name: req.Name, Config: req.Config,
	})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to create connector")
		return
	}
	server.WriteJSON(w, http.StatusCreated, ds)
}

func (h *handler) updateConnector(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid connector ID")
		return
	}

	var req updateConnectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name != nil && *req.Name == "" {
		server.WriteError(w, http.StatusBadRequest, "name cannot be empty")
		return
	}

	params := store.UpdateDataSourceParams{
		Name:   req.Name,
		Config: req.Config,
	}

	// Unregister old connector from registry when config changes
	if req.Config != nil {
		ds, err := h.dsStore.GetByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				server.WriteError(w, http.StatusNotFound, "connector not found")
				return
			}
			server.WriteError(w, http.StatusInternalServerError, "failed to get connector")
			return
		}
		h.registry.Unregister(connector.ConnectorType(ds.Type))
	}

	updated, err := h.dsStore.Update(r.Context(), id, params)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "connector not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to update connector")
		return
	}

	server.WriteJSON(w, http.StatusOK, updated)
}

func (h *handler) testConnector(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid connector ID")
		return
	}

	ds, err := h.dsStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "connector not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to get connector")
		return
	}

	// Create connector and test connection
	c, err := connector.CreateConnector(r.Context(), *ds, h.logStore, h.cfg, h.settingsStore)
	now := time.Now()
	if err != nil {
		status := store.StatusError
		friendlyMsg := connector.FriendlyError(err)
		errCode := connector.ClassifyError(err)
		msg := sanitizeConnectorError(err)
		slog.Warn("connector init failed", "connector_id", ds.ID, "error", err)
		h.dsStore.Update(r.Context(), ds.ID, store.UpdateDataSourceParams{
			Status: &status, StatusMessage: &msg, LastTestedAt: &now,
		})
		server.WriteDetailedError(w, http.StatusUnprocessableEntity, friendlyMsg, string(errCode), map[string]any{
			"suggestion": connector.FriendlyMessage(errCode),
			"phase":      "initialization",
		})
		return
	}

	if err := c.TestConnection(r.Context()); err != nil {
		c.Close()
		status := store.StatusError
		friendlyMsg := connector.FriendlyError(err)
		errCode := connector.ClassifyError(err)
		msg := sanitizeConnectorError(err)
		slog.Warn("connector test failed", "connector_id", ds.ID, "error", err)
		h.dsStore.Update(r.Context(), ds.ID, store.UpdateDataSourceParams{
			Status: &status, StatusMessage: &msg, LastTestedAt: &now,
		})
		server.WriteDetailedError(w, http.StatusUnprocessableEntity, friendlyMsg, string(errCode), map[string]any{
			"suggestion": connector.FriendlyMessage(errCode),
			"phase":      "connection_test",
		})
		return
	}

	// Register in registry on success
	h.registry.Register(c)

	status := store.StatusConnected
	msg := "connection successful"
	updated, err := h.dsStore.Update(r.Context(), ds.ID, store.UpdateDataSourceParams{
		Status: &status, StatusMessage: &msg, LastTestedAt: &now,
	})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to update connector status")
		return
	}

	server.WriteJSON(w, http.StatusOK, updated)
}

func (h *handler) deleteConnector(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid connector ID")
		return
	}

	ds, err := h.dsStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "connector not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to get connector")
		return
	}

	h.registry.Unregister(connector.ConnectorType(ds.Type))

	if err := h.dsStore.Delete(r.Context(), id); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to delete connector")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// sanitizeConnectorError returns a user-safe error message, stripping
// potentially sensitive details like hostnames, ports, and credentials.
func sanitizeConnectorError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "password authentication failed"):
		return "authentication failed -- check credentials"
	case strings.Contains(msg, "connection refused"):
		return "connection refused -- check host and port"
	case strings.Contains(msg, "no such host"):
		return "host not found -- check hostname"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out"):
		return "connection timed out"
	case strings.Contains(msg, "SSL") || strings.Contains(msg, "TLS") || strings.Contains(msg, "x509"):
		return "SSL/TLS error -- check SSL configuration"
	case strings.Contains(msg, "does not exist"):
		return "database does not exist -- check database name"
	case strings.Contains(msg, "permission denied"):
		return "permission denied -- check user privileges"
	default:
		return "connection failed -- check connector settings"
	}
}
