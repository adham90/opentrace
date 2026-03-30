package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adham90/opentrace/internal/backup"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/healthcheck"
	"github.com/adham90/opentrace/internal/jobs"
	mcpserver "github.com/adham90/opentrace/internal/mcp"
	dbstore "github.com/adham90/opentrace/internal/adapter/sqlite"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/adham90/opentrace/internal/version"
	"github.com/adham90/opentrace/internal/watcher"
	"github.com/adham90/opentrace/internal/api"
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
		case "version":
			fmt.Println("opentrace " + version.Full())
			return
		case "help", "--help", "-h":
			fmt.Println("Usage: opentrace [command]")
			fmt.Println()
			fmt.Println("Commands:")
			fmt.Println("  (none)    Start the server")
			fmt.Println("  init      Initialize database (first-time setup)")
			fmt.Println("  serve     Start the server (same as no command)")
			fmt.Println("  mcp       Start the MCP stdio server")
			fmt.Println("  seed      Initialize sample data")
			fmt.Println("  backup    Create a database backup")
			fmt.Println("  restore   Restore database from a backup")
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
func initApp(ctx context.Context) (*server.Deps, error) {
	config.LoadEnvFile(".env")

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}

	// Open SQLite database
	bunDB, err := dbstore.OpenSQLite(cfg.DatabasePath())
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Run migrations
	if err := dbstore.RunSQLiteMigrations(bunDB); err != nil {
		bunDB.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	// Verify database is responsive before proceeding
	if err := bunDB.DB.PingContext(ctx); err != nil {
		bunDB.Close()
		return nil, fmt.Errorf("database health check failed: %w", err)
	}
	slog.Info("database ready")

	// Initialize all stores from a single constructor
	stores := dbstore.NewStores(bunDB)

	// Initialize registry and reconnect previously-configured connectors
	registry := connector.NewRegistry()
	reconnectConnectors(ctx, stores.DSStore, stores.LogStore, registry, cfg, stores.SettingsStore)

	return &server.Deps{
		DB:       bunDB.DB, // underlying *sql.DB for backward compat
		Cfg:      cfg,
		Stores:   stores,
		Registry: registry,
	}, nil
}

// runMCP starts the MCP stdio server. All log output goes to stderr to keep
// stdout clean for the JSON-RPC stream.
func runMCP() error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx := context.Background()
	deps, err := initApp(ctx)
	if err != nil {
		return err
	}
	defer deps.DB.Close()
	defer deps.Registry.CloseAll()

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

	deps, err := initApp(ctx)
	if err != nil {
		return err
	}
	defer deps.DB.Close()

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
		Handler:      srv.Router,
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

	<-done
	slog.Info("shutting down")

	cancelCtx()
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
