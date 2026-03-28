package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/connector"
	dbstore "github.com/adham90/opentrace/internal/db"
	"github.com/adham90/opentrace/internal/api"
)

func TestAppStartup(t *testing.T) {
	bunDB, err := dbstore.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer bunDB.Close()

	if err := dbstore.RunSQLiteMigrations(bunDB); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	dsStore := dbstore.NewDataSourceStore(bunDB)
	logStore := dbstore.NewLogStore(bunDB)
	registry := connector.NewRegistry()
	srv := api.NewServer(dsStore, logStore, registry, nil)

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

	var health map[string]any
	json.NewDecoder(resp.Body).Decode(&health)
	if health["status"] != "ok" {
		t.Errorf("health status = %q, want %q", health["status"], "ok")
	}

	// Connector API routes are behind module auth — tested separately in web tests.
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
