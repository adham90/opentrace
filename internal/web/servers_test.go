package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/domains/servers"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

func newTestServerWithMetrics() *Server {
	reg := connector.NewRegistry()
	return NewServerWithDeps(ServerDeps{
		Stores: store.Stores{
			DSStore:     newMockStore(),
			LogStore:    newMockLogStore(),
			ServerStore: newMockServerStore(),
			MetricStore: newMockMetricStore(),
		},
		Registry: reg,
		SharedDeps: &server.Deps{
			Stores: store.Stores{
				DSStore:     newMockStore(),
				ServerStore: newMockServerStore(),
				MetricStore: newMockMetricStore(),
			},
			Registry: reg,
		},
		Modules: []server.Module{servers.Module},
	})
}

func TestHandleRegisterServer(t *testing.T) {
	srv := newTestServerWithMetrics()

	body := `{"hostname":"web-01","ip_address":"10.0.1.5","os":"linux","arch":"amd64"}`
	req := httptest.NewRequest("POST", "/api/servers/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["server_id"] == nil {
		t.Error("response missing server_id")
	}
}

func TestHandleRegisterServer_MissingHostname(t *testing.T) {
	srv := newTestServerWithMetrics()

	body := `{"os":"linux"}`
	req := httptest.NewRequest("POST", "/api/servers/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleListServers(t *testing.T) {
	srv := newTestServerWithMetrics()

	// Register a server first
	body := `{"hostname":"web-01"}`
	req := httptest.NewRequest("POST", "/api/servers/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	// List servers
	req = httptest.NewRequest("GET", "/api/servers", nil)
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var servers []map[string]any
	json.Unmarshal(w.Body.Bytes(), &servers)
	if len(servers) != 1 {
		t.Errorf("servers count = %d, want 1", len(servers))
	}
}

func TestHandlePushMetrics(t *testing.T) {
	srv := newTestServerWithMetrics()

	// Register server
	body := `{"hostname":"web-01"}`
	req := httptest.NewRequest("POST", "/api/servers/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	var regResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &regResp)
	serverID := regResp["server_id"].(string)

	// Push metrics
	metricsBody := `{
		"timestamp": "2024-01-15T10:00:00Z",
		"samples": [
			{"name": "cpu.usage_percent", "value": 42.5, "unit": "percent"},
			{"name": "memory.used_bytes", "value": 1073741824, "unit": "bytes"}
		]
	}`
	req = httptest.NewRequest("POST", "/api/servers/"+serverID+"/metrics", bytes.NewBufferString(metricsBody))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["count"].(float64) != 2 {
		t.Errorf("count = %v, want 2", resp["count"])
	}
}

func TestHandleGetServer(t *testing.T) {
	srv := newTestServerWithMetrics()

	// Register
	body := `{"hostname":"detail-test","os":"linux","arch":"amd64"}`
	req := httptest.NewRequest("POST", "/api/servers/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	var regResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &regResp)
	serverID := regResp["server_id"].(string)

	// Get
	req = httptest.NewRequest("GET", "/api/servers/"+serverID, nil)
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var s map[string]any
	json.Unmarshal(w.Body.Bytes(), &s)
	if s["hostname"] != "detail-test" {
		t.Errorf("hostname = %v, want %q", s["hostname"], "detail-test")
	}
}

func TestHandleDeleteServer(t *testing.T) {
	srv := newTestServerWithMetrics()

	// Register
	body := `{"hostname":"del-test"}`
	req := httptest.NewRequest("POST", "/api/servers/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	var regResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &regResp)
	serverID := regResp["server_id"].(string)

	// Delete
	req = httptest.NewRequest("DELETE", "/api/servers/"+serverID, nil)
	withCSRF(req)
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Verify gone
	req = httptest.NewRequest("GET", "/api/servers/"+serverID, nil)
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status after delete = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleAgentInstallScript(t *testing.T) {
	srv := newTestServerWithMetrics()

	req := httptest.NewRequest("GET", "/api/agent/install.sh?key=test-key-123", nil)
	req.Host = "opentrace.example.com:8080"
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content-type = %q, want text/plain", ct)
	}
	if !bytes.Contains([]byte(body), []byte("opentrace.example.com:8080")) {
		t.Error("script does not contain the server URL")
	}
	if !bytes.Contains([]byte(body), []byte("test-key-123")) {
		t.Error("script does not contain the API key")
	}
	if !bytes.Contains([]byte(body), []byte("#!/bin/bash")) {
		t.Error("script does not start with shebang")
	}
}

func TestAgentInstallScript_DownloadsCorrectArchive(t *testing.T) {
	srv := newTestServerWithMetrics()

	req := httptest.NewRequest("GET", "/api/agent/install.sh?key=abc", nil)
	req.Host = "trace.example.com"
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	body := w.Body.String()

	// Must use releases/latest/download with versioned tar.gz archive
	if !strings.Contains(body, "releases/latest/download/${archive}") {
		t.Error("script should download from releases/latest/download with archive variable")
	}

	// Archive name must include version, OS, arch, and .tar.gz extension
	if !strings.Contains(body, `${BINARY_NAME}_${version}_${OS}_${ARCH}.tar.gz`) {
		t.Error("archive name should be formatted as name_version_os_arch.tar.gz")
	}

	// Must extract the binary from tar.gz
	if !strings.Contains(body, "tar -xzf") {
		t.Error("script should extract binary from tar.gz archive")
	}
}

func TestAgentInstallScript_NoHangingCommands(t *testing.T) {
	srv := newTestServerWithMetrics()

	req := httptest.NewRequest("GET", "/api/agent/install.sh?key=abc", nil)
	req.Host = "trace.example.com"
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	body := w.Body.String()

	// Must NOT call the binary with --help (starts the server and hangs)
	if strings.Contains(body, `--help`) {
		t.Error("script must not use --help flag (it starts the web server and hangs)")
	}

	// Verification should use 'version' subcommand instead
	if !strings.Contains(body, `version`) {
		t.Error("script should verify installation using the 'version' subcommand")
	}
}

func TestAgentInstallScript_FetchesVersionTag(t *testing.T) {
	srv := newTestServerWithMetrics()

	req := httptest.NewRequest("GET", "/api/agent/install.sh?key=abc", nil)
	req.Host = "trace.example.com"
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	body := w.Body.String()

	// Must resolve the latest version from GitHub before downloading
	if !strings.Contains(body, "releases/latest") {
		t.Error("script should fetch latest version tag from GitHub releases")
	}

	// Must strip the 'v' prefix from the version for the archive name
	if !strings.Contains(body, "sed 's/^v//'") {
		t.Error("script should strip the 'v' prefix from the version tag")
	}
}

func TestAgentInstallScript_CleansUpArchive(t *testing.T) {
	srv := newTestServerWithMetrics()

	req := httptest.NewRequest("GET", "/api/agent/install.sh?key=abc", nil)
	req.Host = "trace.example.com"
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	body := w.Body.String()

	// Must clean up the downloaded archive after extraction
	if !strings.Contains(body, `rm -f "/tmp/${archive}"`) {
		t.Error("script should clean up the tar.gz archive after extraction")
	}
}

func TestAgentInstallScript_HTTPS(t *testing.T) {
	srv := newTestServerWithMetrics()

	req := httptest.NewRequest("GET", "/api/agent/install.sh?key=abc", nil)
	req.Host = "trace.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	body := w.Body.String()

	if !strings.Contains(body, `OPENTRACE_SERVER_URL="https://trace.example.com"`) {
		t.Error("script should use https when X-Forwarded-Proto is set")
	}
}

func TestAgentInstallScript_HandlesReinstall(t *testing.T) {
	srv := newTestServerWithMetrics()

	req := httptest.NewRequest("GET", "/api/agent/install.sh?key=abc", nil)
	req.Host = "trace.example.com"
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	body := w.Body.String()

	// Must stop existing service before replacing binary
	if !strings.Contains(body, "systemctl stop") {
		t.Error("script should stop existing service before replacing binary")
	}

	// Must check if service is already running
	if !strings.Contains(body, "systemctl is-active") {
		t.Error("script should check if service is already active")
	}

	// Stop must come before mv (binary replacement)
	stopIdx := strings.Index(body, "systemctl stop")
	mvIdx := strings.Index(body, `mv /tmp/${BINARY_NAME}`)
	if stopIdx > mvIdx {
		t.Error("script must stop the service before replacing the binary")
	}
}

func TestAgentInstallScript_EmptyAPIKey(t *testing.T) {
	srv := newTestServerWithMetrics()

	req := httptest.NewRequest("GET", "/api/agent/install.sh", nil)
	req.Host = "trace.example.com"
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	body := w.Body.String()

	if !strings.Contains(body, `OPENTRACE_API_KEY=""`) {
		t.Error("script should have empty API key when none provided")
	}
}

func TestUpdateServer(t *testing.T) {
	srv := newTestServerWithMetrics()

	// Register a server
	body := `{"hostname":"rename-test"}`
	req := httptest.NewRequest("POST", "/api/servers/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	var regResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &regResp)
	serverID := regResp["server_id"].(string)

	// Update display_name
	updateBody := `{"display_name":"My Server"}`
	req = httptest.NewRequest("PUT", "/api/servers/"+serverID, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var s map[string]any
	json.Unmarshal(w.Body.Bytes(), &s)
	if s["display_name"] != "My Server" {
		t.Errorf("display_name = %v, want %q", s["display_name"], "My Server")
	}
	if s["hostname"] != "rename-test" {
		t.Errorf("hostname = %v, want %q", s["hostname"], "rename-test")
	}
}

func TestUpdateServer_NotFound(t *testing.T) {
	srv := newTestServerWithMetrics()

	body := `{"display_name":"ghost"}`
	req := httptest.NewRequest("PUT", "/api/servers/00000000-0000-0000-0000-000000000000", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleQueryMetrics(t *testing.T) {
	srv := newTestServerWithMetrics()

	// Register + push
	body := `{"hostname":"query-test"}`
	req := httptest.NewRequest("POST", "/api/servers/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	var regResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &regResp)
	serverID := regResp["server_id"].(string)

	metricsBody := `{"timestamp":"2024-01-15T10:00:00Z","samples":[{"name":"cpu.usage_percent","value":55.0}]}`
	req = httptest.NewRequest("POST", "/api/servers/"+serverID+"/metrics", bytes.NewBufferString(metricsBody))
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	// Query
	req = httptest.NewRequest("GET", "/api/servers/"+serverID+"/metrics?name=cpu.usage_percent", nil)
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var points []map[string]any
	json.Unmarshal(w.Body.Bytes(), &points)
	if len(points) != 1 {
		t.Errorf("metrics count = %d, want 1", len(points))
	}
}
