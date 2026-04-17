package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/adham90/opentrace/internal/api"
	"github.com/adham90/opentrace/internal/backup"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/healthcheck"
	"github.com/adham90/opentrace/internal/ingest"
	"github.com/adham90/opentrace/internal/jobs"
	mcpserver "github.com/adham90/opentrace/internal/mcp"
	dbstore "github.com/adham90/opentrace/internal/adapter/sqlite"
	logadapter "github.com/adham90/opentrace/internal/logstore/adapter"
	"github.com/adham90/opentrace/internal/logstore/engine"
	logsingest "github.com/adham90/opentrace/internal/logstore/ingest"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/version"
	"github.com/adham90/opentrace/internal/watcher"
)

func main() {
	var err error
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			err = runInit()
		case "serve":
			err = run()
		case "mcp":
			err = runMCP()
		case "seed":
			err = runSeed()
		case "backup":
			err = runBackup()
		case "restore":
			err = runRestore()
		case "status":
			err = runStatus()
		case "logs":
			err = runLogs()
		case "version":
			fmt.Println("opentrace " + version.Full())
			return
		case "help", "--help", "-h":
			fmt.Println("Usage: opentrace [command]")
			fmt.Println()
			fmt.Println("Server commands:")
			fmt.Println("  (none)    Start the server")
			fmt.Println("  init      Initialize database (first-time setup)")
			fmt.Println("  serve     Start the server (same as no command)")
			fmt.Println("  mcp       Start the MCP stdio server")
			fmt.Println("  seed      Initialize sample data")
			fmt.Println("  backup    Create a database backup")
			fmt.Println("  restore   Restore database from a backup")
			fmt.Println()
			fmt.Println("CLI commands (connect to a running server):")
			fmt.Println("  status    Show server health and summary stats")
			fmt.Println("  logs      Stream live log tail")
			fmt.Println()
			fmt.Println("  version   Print version information")
			fmt.Println()
			fmt.Println("Backup options:")
			fmt.Println("  opentrace backup [-o <path>] [-f]")
			fmt.Println("    -o, --output   Output file (default: opentrace-backup-TIMESTAMP.db)")
			fmt.Println("    -f, --force    Overwrite destination if it exists")
			fmt.Println()
			fmt.Println("Restore options:")
			fmt.Println("  opentrace restore --from <backup-file>")
			fmt.Println("    -f, --from     Backup file to restore from (required)")
			return
		default:
			err = run()
		}
	} else {
		err = run()
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// initApp performs shared initialization: config, SQLite database,
// migrations, stores, and connector registry.
func initApp(ctx context.Context) (*server.Deps, *engine.Store, error) {
	config.LoadEnvFile(".env")

	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w", err)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("creating data directory: %w", err)
	}

	// Open SQLite database
	bunDB, err := dbstore.OpenSQLite(cfg.DatabasePath())
	if err != nil {
		return nil, nil, fmt.Errorf("opening database: %w", err)
	}

	// Run migrations
	if err := dbstore.RunSQLiteMigrations(bunDB); err != nil {
		bunDB.Close()
		return nil, nil, fmt.Errorf("running migrations: %w", err)
	}

	// Verify database is responsive before proceeding
	if err := bunDB.DB.PingContext(ctx); err != nil {
		bunDB.Close()
		return nil, nil, fmt.Errorf("database health check failed: %w", err)
	}
	slog.Info("database ready")

	// Initialize segmented log store engine
	logDataDir := filepath.Join(cfg.DataDir, "logs")
	logEngine, err := engine.NewStore(logDataDir, nil, logsingest.DefaultPIIConfig())
	if err != nil {
		bunDB.Close()
		return nil, nil, fmt.Errorf("init log store: %w", err)
	}
	logStore := logadapter.New(logEngine)

	// Initialize all stores from a single constructor
	stores := dbstore.NewStores(bunDB, logStore)

	// Initialize registry and reconnect previously-configured connectors
	registry := connector.NewRegistry()
	reconnectConnectors(ctx, stores.DSStore, stores.LogStore, registry, cfg, stores.SettingsStore)

	return &server.Deps{
		DB:        bunDB.DB, // underlying *sql.DB for backward compat
		Cfg:       cfg,
		Stores:    stores,
		Registry:  registry,
		StartedAt: time.Now(),
	}, logEngine, nil
}

// runMCP starts the MCP stdio server. All log output goes to stderr to keep
// stdout clean for the JSON-RPC stream.
func runMCP() error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx := context.Background()
	deps, logEngine, err := initApp(ctx)
	if err != nil {
		return err
	}
	defer deps.DB.Close()
	defer deps.Registry.CloseAll()
	if logEngine != nil {
		defer logEngine.Close()
	}

	watchMetrics := watcher.NewWatchMetrics(deps.LogStore)

	// Resolve MCP name: env override > DB > default
	mcpName := os.Getenv("OPENTRACE_MCP_NAME")
	if mcpName == "" {
		if v, err := deps.SettingsStore.GetMCPName(ctx); err == nil && v != "" {
			mcpName = v
		}
	}

	return mcpserver.Serve(mcpserver.Deps{
		Ctx:          ctx,
		Registry:     deps.Registry,
		MCPToken:     os.Getenv("OPENTRACE_MCP_TOKEN"),
		ServerName:   mcpName,
		Config:       deps.Cfg,
		DB:           deps.DB,
		Stores:       deps.Stores,
		WatchMetrics: watchMetrics,
	})
}



// run starts the full OpenTrace server. The initialization sequence is:
//
//  1. initApp          — load .env + config, create data dir, open SQLite,
//                        run migrations, health-check the DB, build stores,
//                        create connector registry, reconnect saved connectors.
//  2. Watch subsystem  — WatchMetrics, WatchEvaluator, WatchEvidenceBuilder,
//                        WatchStreamEvaluator (reactive on log ingestion).
//  3. MCP tool catalog — build once for the /tools introspection page.
//  4. Health checks    — scheduler created early so the HTTP server can read
//                        reliability data.
//  5. Job queue        — persistent, restart-safe background processing
//                        (worker + scheduler with recurring cleanup/aggregation).
//  6. HTTP server      — api.NewServerWithDeps wires routes + modules,
//                        then http.Server starts in a goroutine.
//  7. Signal handler   — blocks on SIGINT/SIGTERM, then shuts down all
//                        background components and the HTTP server gracefully.
func run() error {
	slog.Info("starting", "version", version.Full())

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	deps, logEngine, err := initApp(ctx)
	if err != nil {
		return err
	}
	defer deps.DB.Close()
	if logEngine != nil {
		defer logEngine.Close()
	}

	// Start hourly log seal goroutine
	go func() {
		// Calculate time until the next hour boundary
		now := time.Now().UTC()
		nextHour := now.Truncate(time.Hour).Add(time.Hour)
		time.Sleep(time.Until(nextHour))

		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := logEngine.SealCurrentHour(); err != nil {
					slog.Error("hourly seal failed", "error", err)
				}
			}
		}
	}()

	// Agent-first watch components
	watchMetrics := watcher.NewWatchMetrics(deps.LogStore)
	deps.WatchMetrics = watchMetrics

	// Build MCP tool catalog for the /tools page (auto-detected from MCP registrations).
	toolCatalog := mcpserver.BuildCatalog(mcpserver.Deps{
		Ctx:          ctx,
		Registry:     deps.Registry,
		Config:       deps.Cfg,
		Stores:       deps.Stores,
		WatchMetrics: watchMetrics,
	})
	deps.ToolCatalog = toolCatalog

	// Agent-first watch evaluator + stream (reactive on log ingestion)
	watchEvaluator := watcher.NewWatchEvaluator(watchMetrics, deps.WatchStore)
	watchEvidenceBuilder := watcher.NewWatchEvidenceBuilder(deps.LogStore, watchMetrics)
	watchStream := watcher.NewWatchStreamEvaluator(ctx, deps.WatchStore, watchEvaluator, watchEvidenceBuilder, nil)

	// Create health check scheduler early so we can inject its reliability data into the web server.
	hcSched := healthcheck.NewScheduler(deps.HealthCheckStore, 0)

	// Create job queue early so it can be injected into deps
	jobQueue := jobs.NewQueue(deps.DB)
	deps.Queue = jobQueue

	// Create server
	srv := api.NewServerWithDeps(api.ServerDeps{
		Ctx:                  ctx,
		DB:                   deps.DB,
		Stores:               deps.Stores,
		Registry:             deps.Registry,
		Cfg:                  deps.Cfg,
		WatchStreamEvaluator: watchStream,
		WatchMetrics:         watchMetrics,
		ReliabilityProvider:  hcSched,
		SharedDeps:           deps,
		Modules:              modules,
	})

	httpServer := &http.Server{
		Addr:         deps.Cfg.ListenAddr,
		Handler:      srv.Handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second, // higher for SSE endpoints
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	// Start agent-first watch scheduler
	watchSessionMgr := watcher.NewWatchSessionManager(deps.WatchStore, watchMetrics)
	watchSched := watcher.NewWatchScheduler(watcher.WatchSchedulerOpts{
		WatchStore:      deps.WatchStore,
		LogStore:        deps.LogStore,
		Evaluator:       watchEvaluator,
		EvidenceBuilder: watchEvidenceBuilder,
		SessionManager:  watchSessionMgr,
	})
	watchSched.Start(ctx)

	// Start health check scheduler (created above for injection into web server)
	hcSched.Start(ctx)

	// --- Job Queue: persistent, restart-safe background processing ---
	jobWorker := jobs.NewWorker(jobQueue)
	jobScheduler := jobs.NewScheduler(jobQueue)

	// Register job handlers
	registerBackgroundJobs(jobWorker, deps)

	// Register recurring schedules
	jobScheduler.Add(jobs.Schedule{Name: "session-cleanup", JobType: "cleanup:sessions", Interval: 15 * time.Minute})
	jobScheduler.Add(jobs.Schedule{Name: "stale-servers", JobType: "cleanup:stale_servers", Interval: 60 * time.Second})
	jobScheduler.Add(jobs.Schedule{Name: "stale-traces", JobType: "cleanup:stale_traces", Interval: 60 * time.Second})
	jobScheduler.Add(jobs.Schedule{Name: "data-retention", JobType: "retention:prune", Interval: 1 * time.Hour})
	jobScheduler.Add(jobs.Schedule{Name: "aggregation", JobType: "aggregate:all", Interval: 5 * time.Minute})

	jobWorker.Start(ctx)
	jobScheduler.Start(ctx)

	go func() {
		slog.Info("listening", "addr", deps.Cfg.ListenAddr)

		// Print connect command for easy copy-paste
		userCount := 0
		if deps.Stores.UserStore != nil {
			if c, err := deps.Stores.UserStore.Count(ctx); err == nil {
				userCount = c
			}
		}
		addr := deps.Cfg.ListenAddr
		if addr == "127.0.0.1:8080" || addr == "0.0.0.0:8080" || addr == ":8080" {
			addr = "YOUR_SERVER:8080"
		}
		fmt.Println()
		if userCount == 0 {
			fmt.Printf("  Connect: curl -s http://%s/connect | bash\n", addr)
			fmt.Println("  (first connection creates admin)")
		} else {
			fmt.Printf("  Connect: curl -s http://%s/connect | bash\n", addr)
			fmt.Printf("  Users:   %d\n", userCount)
		}
		fmt.Println()

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen error", "error", err)
			os.Exit(1)
		}
	}()

	// Start Unix socket listener for local log ingestion (skip HTTP overhead)
	var unixListener net.Listener
	if deps.Cfg.SocketPath != "" {
		var err error
		unixListener, err = startUnixSocketListener(deps.Cfg.SocketPath, srv.IngestHandler())
		if err != nil {
			slog.Error("failed to start unix socket listener", "path", deps.Cfg.SocketPath, "error", err)
		}
	}

	<-done
	slog.Info("shutting down")

	cancelCtx()

	// Close Unix socket listener first so no new connections arrive
	if unixListener != nil {
		unixListener.Close()
		os.Remove(deps.Cfg.SocketPath)
		slog.Info("unix socket listener stopped")
	}

	watchSched.Stop()
	hcSched.Stop()
	jobWorker.Stop()
	jobScheduler.Stop()
	slog.Info("background jobs stopped")

	deps.Registry.CloseAll()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("SSE shutdown error", "error", err)
	}

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP shutdown error", "error", err)
	}

	return nil
}

// runBackup creates a safe backup of the SQLite database.
func runBackup() error {
	config.LoadEnvFile(".env")
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	dbPath := cfg.DatabasePath()

	// Parse args
	destPath := ""
	force := false
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--output", "-o":
			if i+1 < len(os.Args) {
				destPath = os.Args[i+1]
				i++
			}
		case "--force", "-f":
			force = true
		}
	}

	if destPath == "" {
		destPath = fmt.Sprintf("opentrace-backup-%s.db", time.Now().Format("20060102-150405"))
	}

	ctx := context.Background()
	if force {
		return backup.BackupForce(ctx, dbPath, destPath)
	}
	return backup.Backup(ctx, dbPath, destPath)
}

// runRestore restores the SQLite database from a backup file.
func runRestore() error {
	config.LoadEnvFile(".env")
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	dbPath := cfg.DatabasePath()

	// Parse args
	srcPath := ""
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--from", "-f":
			if i+1 < len(os.Args) {
				srcPath = os.Args[i+1]
				i++
			}
		}
	}

	if srcPath == "" {
		return fmt.Errorf("usage: opentrace restore --from <backup-file>")
	}

	ctx := context.Background()
	return backup.Restore(ctx, srcPath, dbPath)
}

// reconnectConnectors re-registers connectors that were previously connected.
func reconnectConnectors(ctx context.Context, dsStore store.DataSourceStore, logStore store.LogStore, registry *connector.Registry, cfg *config.Config, ss store.SettingsStore) {
	dataSources, err := dsStore.List(ctx, store.ListDataSourceParams{})
	if err != nil {
		slog.Warn("failed to list connectors for reconnect", "error", err)
		return
	}

	for _, ds := range dataSources {
		if ds.Status != store.StatusConnected {
			continue
		}

		c, err := connector.CreateConnector(ctx, ds, logStore, cfg, ss)
		if err != nil {
			slog.Warn("failed to recreate connector", "connector", ds.Name, "type", string(ds.Type), "error", err)
			status := store.StatusError
			msg := fmt.Sprintf("failed to reconnect on startup: %v", err)
			dsStore.Update(ctx, ds.ID, store.UpdateDataSourceParams{
				Status: &status, StatusMessage: &msg,
			})
			continue
		}

		if err := c.TestConnection(ctx); err != nil {
			c.Close()
			slog.Warn("connector failed reconnect test", "connector", ds.Name, "type", string(ds.Type), "error", err)
			status := store.StatusError
			msg := fmt.Sprintf("failed to reconnect on startup: %v", err)
			dsStore.Update(ctx, ds.ID, store.UpdateDataSourceParams{
				Status: &status, StatusMessage: &msg,
			})
			continue
		}

		registry.Register(c)
		slog.Info("reconnected connector", "connector", ds.Name, "type", string(ds.Type))
	}
}

// maxUnixPayloadBytes is the maximum payload size accepted over the Unix socket (10 MB).
const maxUnixPayloadBytes = 10 << 20

// startUnixSocketListener creates a Unix domain socket at socketPath and
// spawns a goroutine that accepts connections. Each connection uses a simple
// length-prefixed binary protocol (4-byte big-endian length + payload).
// Returns the listener so the caller can close it during shutdown.
func startUnixSocketListener(socketPath string, handler *ingest.Handler) (net.Listener, error) {
	// Remove stale socket file from a previous run
	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", socketPath, err)
	}

	// Set permissions so any local user can connect
	if err := os.Chmod(socketPath, 0666); err != nil {
		slog.Warn("failed to chmod unix socket", "path", socketPath, "error", err)
	}

	slog.Info("unix socket listener started", "path", socketPath)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				// Accept returns an error when the listener is closed during shutdown
				return
			}
			go handleUnixConnection(conn, handler)
		}
	}()

	return listener, nil
}

// handleUnixConnection processes a single connection on the Unix socket.
//
// Protocol:
//
//	Request:  [4 bytes: big-endian payload length] [payload bytes]
//	Response: [4 bytes: big-endian HTTP status code]
//
// The payload is JSON (or gzip-compressed JSON). It is routed through the
// existing HandleIngestLogs handler via a synthetic httptest request.
func handleUnixConnection(conn net.Conn, handler *ingest.Handler) {
	defer conn.Close()

	// Read 4-byte length prefix
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return
	}
	payloadLen := binary.BigEndian.Uint32(lenBuf)

	// Guard against oversized payloads
	if payloadLen > maxUnixPayloadBytes {
		writeUnixStatus(conn, http.StatusRequestEntityTooLarge)
		return
	}

	// Read payload
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		writeUnixStatus(conn, http.StatusBadRequest)
		return
	}

	// Decompress gzip if the payload starts with the gzip magic bytes
	if len(payload) > 2 && payload[0] == 0x1f && payload[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err == nil {
			decompressed, err := io.ReadAll(io.LimitReader(reader, int64(maxUnixPayloadBytes)+1))
			reader.Close()
			if err == nil && len(decompressed) <= maxUnixPayloadBytes {
				payload = decompressed
			}
		}
	}

	// Build a synthetic HTTP request and route through the existing handler
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.HandleIngestLogs(rec, req)

	writeUnixStatus(conn, rec.Code)
}

// writeUnixStatus writes a 4-byte big-endian status code to the connection.
func writeUnixStatus(conn net.Conn, code int) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(code))
	conn.Write(buf[:])
}
