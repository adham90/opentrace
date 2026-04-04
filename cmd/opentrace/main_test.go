package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/connector"
	dbstore "github.com/adham90/opentrace/internal/adapter/sqlite"
	"github.com/adham90/opentrace/internal/api"
	logadapter "github.com/adham90/opentrace/internal/logstore/adapter"
	"github.com/adham90/opentrace/internal/logstore/engine"
	logsingest "github.com/adham90/opentrace/internal/logstore/ingest"
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
	logEngine, _ := engine.NewStore(t.TempDir(), nil, logsingest.PIIConfig{})
	t.Cleanup(func() { logEngine.Close() })
	logStore := logadapter.New(logEngine)
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

func TestUnixSocketListener(t *testing.T) {
	bunDB, err := dbstore.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer bunDB.Close()

	if err := dbstore.RunSQLiteMigrations(bunDB); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	dsStore := dbstore.NewDataSourceStore(bunDB)
	logEngine, _ := engine.NewStore(t.TempDir(), nil, logsingest.PIIConfig{})
	t.Cleanup(func() { logEngine.Close() })
	logStore := logadapter.New(logEngine)
	registry := connector.NewRegistry()
	srv := api.NewServer(dsStore, logStore, registry, nil)

	// Use /tmp to keep the path short — Unix sockets have a 104-char limit on macOS.
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("ot-test-%d.sock", os.Getpid()))

	listener, err := startUnixSocketListener(socketPath, srv.IngestHandler())
	if err != nil {
		t.Fatalf("start unix socket listener: %v", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	// Send a valid log entry over the Unix socket
	payload := []byte(`[{"timestamp":"2026-01-01T00:00:00Z","level":"info","message":"hello from socket"}]`)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial unix socket: %v", err)
	}
	defer conn.Close()

	// Write 4-byte big-endian length prefix + payload
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := conn.Write(lenBuf[:]); err != nil {
		t.Fatalf("write length: %v", err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	// Read 4-byte status code response
	var statusBuf [4]byte
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(statusBuf[:]); err != nil {
		t.Fatalf("read status: %v", err)
	}
	status := int(binary.BigEndian.Uint32(statusBuf[:]))

	if status != http.StatusCreated {
		t.Errorf("unix socket status = %d, want %d", status, http.StatusCreated)
	}
}

func TestUnixSocketListener_InvalidJSON(t *testing.T) {
	bunDB, err := dbstore.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer bunDB.Close()

	if err := dbstore.RunSQLiteMigrations(bunDB); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	dsStore := dbstore.NewDataSourceStore(bunDB)
	logEngine, _ := engine.NewStore(t.TempDir(), nil, logsingest.PIIConfig{})
	t.Cleanup(func() { logEngine.Close() })
	logStore := logadapter.New(logEngine)
	registry := connector.NewRegistry()
	srv := api.NewServer(dsStore, logStore, registry, nil)

	// Use /tmp to keep the path short — Unix sockets have a 104-char limit on macOS.
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("ot-bad-%d.sock", os.Getpid()))

	listener, err := startUnixSocketListener(socketPath, srv.IngestHandler())
	if err != nil {
		t.Fatalf("start unix socket listener: %v", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	// Send invalid JSON
	payload := []byte(`{not json}`)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial unix socket: %v", err)
	}
	defer conn.Close()

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	conn.Write(lenBuf[:])
	conn.Write(payload)

	var statusBuf [4]byte
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(statusBuf[:]); err != nil {
		t.Fatalf("read status: %v", err)
	}
	status := int(binary.BigEndian.Uint32(statusBuf[:]))

	if status != http.StatusBadRequest {
		t.Errorf("unix socket status = %d, want %d (bad request)", status, http.StatusBadRequest)
	}
}
