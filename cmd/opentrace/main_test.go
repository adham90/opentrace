package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	dbstore "github.com/adham90/opentrace/internal/adapter/sqlite"
	"github.com/adham90/opentrace/internal/api"
	"github.com/adham90/opentrace/internal/apiclient"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	logadapter "github.com/adham90/opentrace/internal/logstore/adapter"
	"github.com/adham90/opentrace/internal/logstore/engine"
	logsingest "github.com/adham90/opentrace/internal/logstore/ingest"
	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
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

// TestDispatchUnknownCommand: a typo must not boot a server.
func TestDispatchUnknownCommand(t *testing.T) {
	err, code := dispatch([]string{"statsu"})
	if err == nil {
		t.Fatal("unknown command returned no error")
	}
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error = %q, want it to mention the unknown command", err)
	}
}

func TestDispatchKnownNonServerCommands(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v", "help", "--help", "-h"} {
		err, code := dispatch([]string{arg})
		if err != nil || code != 0 {
			t.Errorf("dispatch(%q) = (%v, %d), want (nil, 0)", arg, err, code)
		}
	}
}

// TestShutdownSealsActiveWAL is the regression test for the hour-crossing
// restart data loss: teardown must seal the active WAL, and the entries must
// still be searchable after the process restarts.
func TestShutdownSealsActiveWAL(t *testing.T) {
	dir := t.TempDir()

	logEngine, err := engine.NewStore(dir, nil, logsingest.PIIConfig{})
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	logStore := logadapter.New(logEngine)

	ctx := context.Background()
	entries := []store.LogEntry{{
		Timestamp: time.Now(),
		Level:     "ERROR",
		Service:   "payments",
		Message:   "shutdown-seal-canary",
	}}
	if _, err := logStore.BatchInsert(ctx, entries); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Simulate SIGTERM teardown (no registry, no SQLite handle).
	teardownApp(nil, logEngine, true)

	// The hour must now be a complete sealed segment, not an orphaned active.wal.
	dirs, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}
	sealed := 0
	for _, d := range dirs {
		if d.IsDir() && engine.IsSealComplete(filepath.Join(dir, d.Name())) {
			sealed++
		}
	}
	if sealed == 0 {
		t.Fatalf("no sealed segment after shutdown; dirs=%v", dirs)
	}

	// Restart against the same data dir: the log must still be searchable.
	restarted, err := engine.NewStore(dir, nil, logsingest.PIIConfig{})
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer restarted.Close()

	found, err := logadapter.New(restarted).Search(ctx, store.LogSearchParams{
		Query: "shutdown-seal-canary",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("search after restart: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d entries after restart, want 1", len(found))
	}
	if found[0].Message != "shutdown-seal-canary" {
		t.Errorf("message = %q", found[0].Message)
	}
}

func TestNextHourBoundary(t *testing.T) {
	in := time.Date(2026, 8, 14, 10, 20, 30, 0, time.UTC)
	want := time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC)
	if got := nextHourBoundary(in); !got.Equal(want) {
		t.Errorf("nextHourBoundary(%v) = %v, want %v", in, got, want)
	}
	onBoundary := time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC)
	if got := nextHourBoundary(onBoundary); !got.Equal(onBoundary.Add(time.Hour)) {
		t.Errorf("nextHourBoundary on boundary = %v, want strictly after", got)
	}
}

// TestUnixSocketOversizedGzip: a gzip bomb must be answered 413, not 400.
func TestUnixSocketOversizedGzip(t *testing.T) {
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
	srv := api.NewServer(dsStore, logadapter.New(logEngine), connector.NewRegistry(), nil)

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("ot-gzip-%d.sock", os.Getpid()))
	unixSrv, err := startUnixSocketListener(socketPath, srv.IngestHandler())
	if err != nil {
		t.Fatalf("start unix socket listener: %v", err)
	}
	defer unixSrv.Close()

	// Compress more than maxUnixPayloadBytes of highly compressible data.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	block := bytes.Repeat([]byte("a"), 1<<20)
	for i := 0; i < (maxUnixPayloadBytes>>20)+1; i++ {
		if _, err := zw.Write(block); err != nil {
			t.Fatalf("gzip write: %v", err)
		}
	}
	zw.Close()
	payload := buf.Bytes()

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
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Read(statusBuf[:]); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status := int(binary.BigEndian.Uint32(statusBuf[:])); status != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", status, http.StatusRequestEntityTooLarge)
	}
}

func TestStatusHealthy(t *testing.T) {
	if statusHealthy(nil) {
		t.Error("nil status reported healthy")
	}
	if statusHealthy(&apiclient.StatusResponse{Database: &apiclient.DatabaseStatus{Healthy: false}}) {
		t.Error("unhealthy database reported healthy")
	}
	if !statusHealthy(&apiclient.StatusResponse{Database: &apiclient.DatabaseStatus{Healthy: true}}) {
		t.Error("healthy database reported unhealthy")
	}
}

func TestValidSince(t *testing.T) {
	for _, ok := range []string{"15m", "6h", "7d", "2026-08-14T10:00:00Z"} {
		if !validSince(ok) {
			t.Errorf("validSince(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"yesterday", "1x", ""} {
		if validSince(bad) {
			t.Errorf("validSince(%q) = true, want false", bad)
		}
	}
}

// TestUnixStalledConnectionTimesOut: a client that connects and never sends
// must not pin a goroutine forever.
func TestUnixStalledConnectionTimesOut(t *testing.T) {
	prev := unixReadTimeout
	// Short enough to keep the test quick, long enough that a loaded -race
	// build does not trip it: the assertion is that the connection drains at
	// all, not how fast.
	unixReadTimeout = time.Second
	t.Cleanup(func() { unixReadTimeout = prev })

	logEngine, _ := engine.NewStore(t.TempDir(), nil, logsingest.PIIConfig{})
	t.Cleanup(func() { logEngine.Close() })

	bunDB, err := dbstore.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer bunDB.Close()
	if err := dbstore.RunSQLiteMigrations(bunDB); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	srv := api.NewServer(dbstore.NewDataSourceStore(bunDB), logadapter.New(logEngine), connector.NewRegistry(), nil)

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("ot-stall-%d.sock", os.Getpid()))
	unixSrv, err := startUnixSocketListener(socketPath, srv.IngestHandler())
	if err != nil {
		t.Fatalf("start unix socket listener: %v", err)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	// Send nothing at all, then close the listener and drain.
	unixSrv.CloseListener()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	unixSrv.WaitInFlight(ctx)
	if ctx.Err() != nil {
		t.Fatalf("stalled connection never drained (waited %s)", time.Since(start))
	}
}

// TestGracefulShutdownDrainsHTTPBeforeWorkers proves the ordering fix: an
// in-flight request finishes before app-level teardown (workers, connectors,
// the log engine) runs.
func TestGracefulShutdownDrainsHTTPBeforeWorkers(t *testing.T) {
	var (
		mu              sync.Mutex
		handlerFinished time.Time
		workerStopped   time.Time
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		mu.Lock()
		handlerFinished = time.Now()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpServer := &http.Server{Handler: mux}
	go httpServer.Serve(ln)

	worker := &recordingWorker{onStop: func() {
		mu.Lock()
		workerStopped = time.Now()
		mu.Unlock()
	}}

	reqDone := make(chan struct{})
	go func() {
		defer close(reqDone)
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Let the request start before shutting down.
	time.Sleep(50 * time.Millisecond)

	_, cancel := context.WithCancel(context.Background())
	gracefulShutdown(shutdownParams{
		CancelCtx:  cancel,
		HTTPServer: httpServer,
		APIServer:  nil,
		Workers:    []backgroundStopper{worker},
	})
	<-reqDone

	mu.Lock()
	defer mu.Unlock()
	if handlerFinished.IsZero() {
		t.Fatal("handler never finished")
	}
	if workerStopped.IsZero() {
		t.Fatal("worker was never stopped")
	}
	if !workerStopped.After(handlerFinished) {
		t.Errorf("worker stopped at %v, before handler finished at %v", workerStopped, handlerFinished)
	}
}

type recordingWorker struct{ onStop func() }

func (w *recordingWorker) Stop() { w.onStop() }

// failingSettingsStore makes GetAPIKey fail the way a transient SQLITE_BUSY
// would, while recording whether a new key was written.
type failingSettingsStore struct {
	*mocks.SettingsStore
	setCalled bool
}

func (s *failingSettingsStore) GetAPIKey(context.Context) (string, error) {
	return "", errors.New("database is locked")
}

func (s *failingSettingsStore) SetAPIKey(ctx context.Context, key string) error {
	s.setCalled = true
	return s.SettingsStore.SetAPIKey(ctx, key)
}

// TestEnsureAPIKeyFailsClosed: a failed settings read must never rotate the key.
func TestEnsureAPIKeyFailsClosed(t *testing.T) {
	ss := &failingSettingsStore{SettingsStore: mocks.NewSettingsStore()}
	ss.APIKey = "provisioned-key"

	deps := &server.Deps{
		Cfg:    &config.Config{},
		Stores: store.Stores{SettingsStore: ss},
	}

	err := ensureAPIKey(context.Background(), deps)
	if err == nil {
		t.Fatal("ensureAPIKey returned nil on a settings read failure")
	}
	if ss.setCalled {
		t.Error("ensureAPIKey overwrote the API key after a read failure")
	}
	if ss.APIKey != "provisioned-key" {
		t.Errorf("API key = %q, want it untouched", ss.APIKey)
	}
}

// TestEnsureAPIKeyProvisionsWhenMissing keeps the happy path honest.
func TestEnsureAPIKeyProvisionsWhenMissing(t *testing.T) {
	ss := mocks.NewSettingsStore()
	deps := &server.Deps{
		Cfg:    &config.Config{},
		Stores: store.Stores{SettingsStore: ss},
	}
	if err := ensureAPIKey(context.Background(), deps); err != nil {
		t.Fatalf("ensureAPIKey: %v", err)
	}
	if ss.APIKey == "" {
		t.Error("no API key was provisioned")
	}
}
