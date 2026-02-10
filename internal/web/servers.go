package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
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

// handleAgentInstallScript serves GET /api/agent/install.sh — a generated shell
// script that installs the opentrace agent on a remote server.
func (s *Server) handleAgentInstallScript(w http.ResponseWriter, r *http.Request) {
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
DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${BINARY_NAME}_${OS}_${ARCH}"

install_from_release() {
  echo "==> Downloading from GitHub releases..."
  if command -v curl &>/dev/null; then
    curl -fsSL -o /tmp/${BINARY_NAME} "${DOWNLOAD_URL}" 2>/dev/null && return 0
  elif command -v wget &>/dev/null; then
    wget -q -O /tmp/${BINARY_NAME} "${DOWNLOAD_URL}" 2>/dev/null && return 0
  fi
  return 1
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

# Move binary to install dir
echo "==> Installing to ${INSTALL_DIR}/${BINARY_NAME}"
chmod +x /tmp/${BINARY_NAME}
if [ "$(id -u)" -eq 0 ]; then
  mv /tmp/${BINARY_NAME} "${INSTALL_DIR}/${BINARY_NAME}"
else
  sudo mv /tmp/${BINARY_NAME} "${INSTALL_DIR}/${BINARY_NAME}"
fi

# Verify installation
if ! "${INSTALL_DIR}/${BINARY_NAME}" --help &>/dev/null 2>&1; then
  # Binary may not have --help but that's ok
  true
fi

echo "==> Binary installed at ${INSTALL_DIR}/${BINARY_NAME}"

# Create systemd service if available
if command -v systemctl &>/dev/null; then
  echo "==> Creating systemd service..."

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
