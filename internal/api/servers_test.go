package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/routes/servers"
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

	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandlePushMetrics(t *testing.T) {
	srv := newTestServerWithMetrics()

	// Register server
	body := `{"hostname":"web-01"}`
	req := httptest.NewRequest("POST", "/api/servers/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

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
