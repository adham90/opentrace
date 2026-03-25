package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// handleDeployWebhook accepts deploy events from CI/CD pipelines.
// POST /api/events/deploy (API key auth)
func (s *Server) handleDeployWebhook(w http.ResponseWriter, r *http.Request) {
	if s.deployStore == nil {
		writeError(w, http.StatusServiceUnavailable, "deploy tracking not available")
		return
	}

	var body struct {
		Service      string   `json:"service"`
		Environment  string   `json:"environment"`
		CommitHash   string   `json:"commit_hash"`
		Branch       string   `json:"branch"`
		Author       string   `json:"author"`
		FilesChanged []string `json:"files_changed"`
		DeployedAt   string   `json:"deployed_at"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if body.CommitHash == "" {
		writeError(w, http.StatusBadRequest, "commit_hash is required")
		return
	}

	params := store.CreateDeployParams{
		Service:      body.Service,
		Environment:  body.Environment,
		CommitHash:   body.CommitHash,
		Branch:       body.Branch,
		Author:       body.Author,
		FilesChanged: body.FilesChanged,
		DeploySource: store.DeploySourceWebhook,
	}

	if body.DeployedAt != "" {
		if t, err := time.Parse(time.RFC3339, body.DeployedAt); err == nil {
			params.DeployedAt = &t
		}
	}

	d, err := s.deployStore.Create(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record deploy")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          d.ID,
		"service":     d.Service,
		"commit_hash": d.CommitHash,
		"status":      d.Status,
		"deployed_at": d.DeployedAt.Format(time.RFC3339),
	})
}

// handleEventWebhook accepts generic events from CI/CD pipelines and integrations.
// POST /api/events/{type} (API key auth)
func (s *Server) handleEventWebhook(w http.ResponseWriter, r *http.Request) {
	if s.eventStore == nil {
		writeError(w, http.StatusServiceUnavailable, "event tracking not available")
		return
	}

	eventType := store.EventType(chi.URLParam(r, "type"))
	switch eventType {
	case store.EventTypePR, store.EventTypeTest, store.EventTypeAlert,
		store.EventTypeCommit, store.EventTypeCustom, store.EventTypeDeploy:
		// valid
	default:
		writeError(w, http.StatusBadRequest, "invalid event type: must be pr, test, alert, commit, deploy, or custom")
		return
	}

	var body struct {
		Source      string         `json:"source"`
		Service     string         `json:"service"`
		Title       string         `json:"title"`
		Description string         `json:"description"`
		Metadata    map[string]any `json:"metadata"`
		ExternalID  string         `json:"external_id"`
		ExternalURL string         `json:"external_url"`
		Author      string         `json:"author"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if body.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	e, err := s.eventStore.Create(r.Context(), store.CreateEventParams{
		EventType:   eventType,
		Source:      body.Source,
		Service:     body.Service,
		Title:       body.Title,
		Description: body.Description,
		Metadata:    body.Metadata,
		ExternalID:  body.ExternalID,
		ExternalURL: body.ExternalURL,
		Author:      body.Author,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create event")
		return
	}

	writeJSON(w, http.StatusCreated, e)
}

// handleRegisterServer handles POST /api/servers/register (API key auth).
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

// handlePushMetrics handles POST /api/servers/{id}/metrics (API key auth).
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

// ensureMetricsConnector auto-registers the server metrics connector on first server registration.
func (s *Server) ensureMetricsConnector(ctx context.Context) {
	if s.registry.Get(connector.ConnectorServerMetrics) != nil {
		return
	}

	s.metricsConnMu.Lock()
	defer s.metricsConnMu.Unlock()

	if s.registry.Get(connector.ConnectorServerMetrics) != nil {
		return
	}

	sources, err := s.dsStore.List(ctx, store.ListDataSourceParams{Type: store.ConnectorServerMetrics})
	if err != nil {
		slog.Warn("ensureMetricsConnector: failed to list data sources", "error", err)
		return
	}
	var dsID *store.DataSource
	if len(sources) > 0 {
		dsID = &sources[0]
	}
	if dsID == nil {
		created, err := s.dsStore.Create(ctx, store.CreateDataSourceParams{
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

	mc := connector.NewMetricsConnector(s.serverStore, s.metricStore)
	s.registry.Register(mc)

	connected := store.StatusConnected
	if _, err := s.dsStore.Update(ctx, dsID.ID, store.UpdateDataSourceParams{
		Status: &connected,
	}); err != nil {
		slog.Warn("ensureMetricsConnector: failed to update status", "error", err)
	}

	slog.Info("auto-registered server metrics connector", "data_source_id", dsID.ID)
}

// handleAgentInstallScript serves GET /api/agent/install.sh.
func (s *Server) handleAgentInstallScript(w http.ResponseWriter, r *http.Request) {
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

REPO="adham90/opentrace"
BINARY_NAME="opentrace"

install_from_release() {
  echo "==> Downloading from GitHub releases..."
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

echo "==> Installing to ${INSTALL_DIR}/${BINARY_NAME}"
chmod +x /tmp/${BINARY_NAME}
if [ "$(id -u)" -eq 0 ]; then
  mv /tmp/${BINARY_NAME} "${INSTALL_DIR}/${BINARY_NAME}"
else
  sudo mv /tmp/${BINARY_NAME} "${INSTALL_DIR}/${BINARY_NAME}"
fi

if "${INSTALL_DIR}/${BINARY_NAME}" version &>/dev/null 2>&1; then
  echo "==> Binary installed at ${INSTALL_DIR}/${BINARY_NAME}"
else
  echo "==> Binary installed at ${INSTALL_DIR}/${BINARY_NAME} (could not verify version)"
fi

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
