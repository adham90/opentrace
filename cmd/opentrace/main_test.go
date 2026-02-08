package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/opentrace/opentrace/internal/connector"
	"github.com/opentrace/opentrace/internal/store"
	"github.com/opentrace/opentrace/internal/testutil"
	"github.com/opentrace/opentrace/internal/web"
)

func TestAppStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	dsStore := store.NewPgDataSourceStore(pool)
	registry := connector.NewRegistry()
	srv := web.NewServer(dsStore, registry)

	// Find a free port
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: srv.Router,
	}

	go httpServer.ListenAndServe()
	defer httpServer.Close()

	// Wait for server to be ready
	baseURL := fmt.Sprintf("http://localhost:%d", port)
	waitForServer(t, baseURL, 5*time.Second)

	// Test /healthz
	resp, err := http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var health map[string]string
	json.NewDecoder(resp.Body).Decode(&health)
	if health["status"] != "ok" {
		t.Errorf("health status = %q, want %q", health["status"], "ok")
	}

	// Test /api/connectors returns []
	resp2, err := http.Get(baseURL + "/api/connectors")
	if err != nil {
		t.Fatalf("GET /api/connectors failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("connectors status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}

	var connectors []store.DataSource
	json.NewDecoder(resp2.Body).Decode(&connectors)
	if len(connectors) != 0 {
		t.Errorf("expected empty connectors list, got %d", len(connectors))
	}
}

func waitForServer(t *testing.T, baseURL string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("server did not start in time")
}

// Ensure OPENTRACE_APP_DATABASE_URL is not required for build
func init() {
	// Don't set env vars - tests use testcontainers directly
	os.Setenv("OPENTRACE_APP_DATABASE_URL", "not-used-in-test")
}
