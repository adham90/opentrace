package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
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

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	params := store.ListServerParams{}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			params.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			params.Offset = n
		}
	}
	servers, err := h.serverStore.List(r.Context(), params)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to list servers")
		return
	}
	server.WriteJSON(w, http.StatusOK, servers)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid server ID")
		return
	}

	srv, err := h.serverStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "server not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to get server")
		return
	}

	server.WriteJSON(w, http.StatusOK, srv)
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid server ID")
		return
	}

	var params store.UpdateServerParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	srv, err := h.serverStore.Update(r.Context(), id, params)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "server not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to update server")
		return
	}

	server.WriteJSON(w, http.StatusOK, srv)
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid server ID")
		return
	}

	if err := h.serverStore.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			server.WriteError(w, http.StatusNotFound, "server not found")
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "failed to delete server")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) queryMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid server ID")
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

	points, err := h.metricStore.Query(r.Context(), q)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to query metrics")
		return
	}

	server.WriteJSON(w, http.StatusOK, points)
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

// agentInstallScript serves GET /api/agent/install.sh -- a generated shell
// script that installs the opentrace agent on a remote server.
func (h *handler) agentInstallScript(w http.ResponseWriter, r *http.Request) {
	// Infer the server URL from the request
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	serverURL := fmt.Sprintf("%s://%s", scheme, r.Host)

	apiKey := r.URL.Query().Get("key")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline")

	var b strings.Builder
	b.WriteString(`#!/bin/bash
set -euo pipefail

# OpenTrace Agent Installer
# Generated by your OpenTrace server

OPENTRACE_SERVER_URL="` + serverURL + `"
`)
	if apiKey != "" {
		b.WriteString(`OPENTRACE_API_KEY="` + apiKey + `"
`)
	} else {
		b.WriteString(`OPENTRACE_API_KEY=""
`)
	}

	b.WriteString(`INSTALL_DIR="/usr/local/bin"
SERVICE_NAME="opentrace-agent"

echo "==> OpenTrace Agent Installer"
echo "    Server: ${OPENTRACE_SERVER_URL}"
echo ""

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "ERROR: Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

echo "==> Detected ${OS}/${ARCH}"

# Try to download pre-built binary from GitHub releases
REPO="adham90/opentrace"
BINARY_NAME="opentrace"

install_from_release() {
  echo "==> Downloading from GitHub releases..."
  # Get latest version tag
  local latest_url="https://github.com/${REPO}/releases/latest"
  local version=""
  if command -v curl &>/dev/null; then
    version=$(curl -fsSL -o /dev/null -w '%{url_effective}' "${latest_url}" 2>/dev/null | grep -oE '[^/]+$' | sed 's/^v//')
  elif command -v wget &>/dev/null; then
    version=$(wget --spider -S -O /dev/null "${latest_url}" 2>&1 | grep -i 'Location:' | tail -1 | grep -oE '[^/]+$' | sed 's/^v//')
  fi
  if [ -z "$version" ]; then return 1; fi

  local archive="${BINARY_NAME}_${version}_${OS}_${ARCH}.tar.gz"
  local download_url="https://github.com/${REPO}/releases/latest/download/${archive}"
  echo "    Version: v${version}"

  if command -v curl &>/dev/null; then
    curl -fsSL -o "/tmp/${archive}" "${download_url}" 2>/dev/null || return 1
  elif command -v wget &>/dev/null; then
    wget -q -O "/tmp/${archive}" "${download_url}" 2>/dev/null || return 1
  else
    return 1
  fi

  tar -xzf "/tmp/${archive}" -C /tmp "${BINARY_NAME}" 2>/dev/null || return 1
  rm -f "/tmp/${archive}"
  return 0
}

install_from_source() {
  if ! command -v go &>/dev/null; then
    return 1
  fi
  echo "==> Building from source with go install..."
  GOBIN=/tmp go install "github.com/${REPO}/cmd/opentrace@latest"
  return 0
}

INSTALLED=false

if install_from_release; then
  INSTALLED=true
elif install_from_source; then
  INSTALLED=true
else
  echo ""
  echo "ERROR: Could not install opentrace."
  echo "  - No pre-built binary found for ${OS}/${ARCH}"
  echo "  - Go is not installed (needed for building from source)"
  echo ""
  echo "Install Go (https://go.dev/dl/) and re-run, or download the binary manually."
  exit 1
fi

# Stop existing service before replacing binary
UPGRADING=false
if command -v systemctl &>/dev/null && systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
  UPGRADING=true
  echo "==> Stopping existing ${SERVICE_NAME} service..."
  if [ "$(id -u)" -eq 0 ]; then
    systemctl stop "$SERVICE_NAME"
  else
    sudo systemctl stop "$SERVICE_NAME"
  fi
fi

# Move binary to install dir
echo "==> Installing to ${INSTALL_DIR}/${BINARY_NAME}"
chmod +x /tmp/${BINARY_NAME}
if [ "$(id -u)" -eq 0 ]; then
  mv /tmp/${BINARY_NAME} "${INSTALL_DIR}/${BINARY_NAME}"
else
  sudo mv /tmp/${BINARY_NAME} "${INSTALL_DIR}/${BINARY_NAME}"
fi

# Verify installation
if "${INSTALL_DIR}/${BINARY_NAME}" version &>/dev/null 2>&1; then
  echo "==> Binary installed at ${INSTALL_DIR}/${BINARY_NAME}"
else
  echo "==> Binary installed at ${INSTALL_DIR}/${BINARY_NAME} (could not verify version)"
fi

# Create systemd service if available
if command -v systemctl &>/dev/null; then
  if [ "$UPGRADING" = true ]; then
    echo "==> Upgrading existing service..."
  else
    echo "==> Creating systemd service..."
  fi

  SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
  SERVICE_CONTENT="[Unit]
Description=OpenTrace Agent
After=network.target

[Service]
Type=simple
Environment=OPENTRACE_SERVER_URL=${OPENTRACE_SERVER_URL}
Environment=OPENTRACE_API_KEY=${OPENTRACE_API_KEY}
ExecStart=${INSTALL_DIR}/${BINARY_NAME} agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target"

  if [ "$(id -u)" -eq 0 ]; then
    echo "$SERVICE_CONTENT" > "$SERVICE_FILE"
    systemctl daemon-reload
    systemctl enable --now "$SERVICE_NAME"
  else
    echo "$SERVICE_CONTENT" | sudo tee "$SERVICE_FILE" > /dev/null
    sudo systemctl daemon-reload
    sudo systemctl enable --now "$SERVICE_NAME"
  fi

  echo "==> Service ${SERVICE_NAME} started"
  echo ""
  echo "Manage with:"
  echo "  systemctl status ${SERVICE_NAME}"
  echo "  journalctl -u ${SERVICE_NAME} -f"
else
  echo ""
  echo "systemd not found. Run the agent manually:"
  echo "  OPENTRACE_SERVER_URL=${OPENTRACE_SERVER_URL} OPENTRACE_API_KEY=${OPENTRACE_API_KEY} opentrace agent"
fi

echo ""
echo "==> Done! Your server should appear at ${OPENTRACE_SERVER_URL}/servers shortly."
`)

	w.Write([]byte(b.String()))
}
