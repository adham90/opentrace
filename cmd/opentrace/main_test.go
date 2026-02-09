package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/web"
)

func TestAppStartup(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if err := store.RunSQLiteMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	dsStore := store.NewDataSourceStore(db)
	logStore := store.NewLogStore(db)
	registry := connector.NewRegistry()
	srv := web.NewServer(dsStore, logStore, registry, nil)

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
