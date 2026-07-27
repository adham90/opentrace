package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/adham90/opentrace/internal/api"
	"github.com/adham90/opentrace/internal/backup"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/cryptoutil"
	"github.com/adham90/opentrace/internal/healthcheck"
	"github.com/adham90/opentrace/internal/ingest"
	"github.com/adham90/opentrace/internal/jobs"
	mcpserver "github.com/adham90/opentrace/internal/mcp"
	dbstore "github.com/adham90/opentrace/internal/adapter/sqlite"
	logadapter "github.com/adham90/opentrace/internal/logstore/adapter"
	"github.com/adham90/opentrace/internal/logstore/engine"
	logsingest "github.com/adham90/opentrace/internal/logstore/ingest"
	"github.com/adham90/opentrace/internal/notify"
	"github.com/adham90/opentrace/internal/safe"
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



// ensureAPIKey guarantees `serve` does not silently accept unauthenticated
// ingest/CLI requests. A key from OPENTRACE_API_KEY or the settings DB (e.g.
// provisioned by `opentrace init`) is left as-is. If none exists, one is
// generated, persisted, and printed — unless the operator explicitly opts out
// via OPENTRACE_DISABLE_AUTH=true, in which case we run open but warn loudly.
func ensureAPIKey(ctx context.Context, deps *server.Deps) {
	if deps.Cfg != nil && deps.Cfg.APIKey != "" {
		return // supplied via OPENTRACE_API_KEY
	}
	if deps.SettingsStore != nil {
		if key, err := deps.SettingsStore.GetAPIKey(ctx); err == nil && key != "" {
			return // already provisioned
		}
	}
	if os.Getenv("OPENTRACE_DISABLE_AUTH") == "true" {
		slog.Warn("AUTH DISABLED: ingest and CLI-read endpoints accept unauthenticated requests (OPENTRACE_DISABLE_AUTH=true). Do not expose this instance to untrusted networks.")
		return
	}
	if deps.SettingsStore == nil {
		slog.Error("no API key configured and no settings store to provision one; set OPENTRACE_API_KEY or OPENTRACE_DISABLE_AUTH=true")
		return
	}
	key, err := cryptoutil.GenerateAPIKey()
	if err != nil {
		slog.Error("failed to auto-generate API key", "error", err)
		return
	}
	if err := deps.SettingsStore.SetAPIKey(ctx, key); err != nil {
		slog.Error("failed to persist auto-generated API key", "error", err)
		return
	}
	slog.Warn("no API key was configured — generated one automatically; configure your SDKs with it", "api_key", key)
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

	// Never silently run with ingest/CLI endpoints wide open.
	ensureAPIKey(ctx, deps)

	// Start hourly log seal goroutine (panic-isolated; ctx-aware so shutdown is
	// not blocked by the initial wait and it never seals a closing engine).
	safe.Go("hourly-seal", func() {
		now := time.Now().UTC()
		nextHour := now.Truncate(time.Hour).Add(time.Hour)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(nextHour)):
		}

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
	})

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

	// Build alert notifiers from settings. Without this the watch/health
	// evaluators only log to slog and never deliver anything. Telegram config is
	// read lazily per-send from the settings store (so runtime changes take
	// effect without restart); webhook URLs come from the environment. Every
	// path degrades gracefully: the Telegram sender no-ops when unconfigured and
	// webhook notifiers are only added when a URL is set.
	telegramSender := buildTelegramSender(ctx, deps.SettingsStore)
	watchNotifiers := buildWatchNotifiers(telegramSender)
	healthNotifiers := buildHealthNotifiers(telegramSender)

	// Agent-first watch evaluator + stream (reactive on log ingestion)
	watchEvaluator := watcher.NewWatchEvaluator(watchMetrics, deps.WatchStore)
	watchEvidenceBuilder := watcher.NewWatchEvidenceBuilder(deps.LogStore, watchMetrics)
	watchStream := watcher.NewWatchStreamEvaluator(ctx, deps.WatchStore, watchEvaluator, watchEvidenceBuilder, watchNotifiers)

	// Create health check scheduler early so we can inject its reliability data into the web server.
	hcSched := healthcheck.NewScheduler(deps.HealthCheckStore, 0, healthNotifiers...)

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
		Notifiers:       watchNotifiers,
	})
	watchSched.Start(ctx)

	// Start health check scheduler (created above for injection into web server)
	hcSched.Start(ctx)

	// --- Job Queue: persistent, restart-safe background processing ---
	jobWorker := jobs.NewWorker(jobQueue)
	jobScheduler := jobs.NewScheduler(jobQueue)

	// Register job handlers
	registerBackgroundJobs(jobWorker, deps)

	// Job-queue retention: prune old completed/dead jobs and VACUUM to reclaim
	// disk. Queue.Prune previously had no caller (jobs table grew unbounded) and
	// the VACUUM path was dead code. VACUUM locks the DB, so this runs on a slow
	// recurring schedule via the worker — never per-request.
	jobWorker.Register("retention:jobs", func(ctx context.Context, _ json.RawMessage) error {
		_, err := jobs.RunJobRetention(ctx, deps.DB, jobRetentionWindow)
		return err
	})

	// Register recurring schedules
	jobScheduler.Add(jobs.Schedule{Name: "session-cleanup", JobType: "cleanup:sessions", Interval: 15 * time.Minute})
	jobScheduler.Add(jobs.Schedule{Name: "stale-servers", JobType: "cleanup:stale_servers", Interval: 60 * time.Second})
	jobScheduler.Add(jobs.Schedule{Name: "stale-traces", JobType: "cleanup:stale_traces", Interval: 60 * time.Second})
	jobScheduler.Add(jobs.Schedule{Name: "data-retention", JobType: "retention:prune", Interval: 1 * time.Hour})
	jobScheduler.Add(jobs.Schedule{Name: "jobs-retention", JobType: "retention:jobs", Interval: 6 * time.Hour})
	jobScheduler.Add(jobs.Schedule{Name: "aggregation", JobType: "aggregate:all", Interval: 5 * time.Minute})

	// Reclaim jobs left 'running' by a previous crash before the worker starts.
	// ClaimNext has no lease, so a crash mid-job wedges that row 'running'
	// forever and the scheduler then skips the job type indefinitely. Running
	// this before Start() is safe: no worker is live yet, so any 'running' row
	// is necessarily orphaned.
	if n, err := jobQueue.ReapOrphaned(ctx, 0); err != nil {
		slog.Error("reaping orphaned jobs on startup failed", "error", err)
	} else if n > 0 {
		slog.Info("reclaimed orphaned running jobs on startup", "count", n)
	}

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

	// Drain the HTTP server FIRST so all in-flight handlers finish before we
	// tear down app-level channels. srv.Shutdown() closes the audit channel;
	// doing that while a mutating request is still running would panic on a
	// send-to-closed-channel (even with the select/default) and lose the audit.
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP shutdown error", "error", err)
	}

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("SSE shutdown error", "error", err)
	}

	return nil
}

// jobRetentionWindow is how long completed/dead jobs are kept before the
// recurring retention job prunes them.
const jobRetentionWindow = 7 * 24 * time.Hour

// alertWebhookEnv names the environment variable holding an optional webhook URL
// that receives both watch-rule and health-check alerts. Unset = no webhook.
const alertWebhookEnv = "OPENTRACE_ALERT_WEBHOOK_URL"

// buildTelegramSender returns a Telegram sender whose config is read lazily from
// the settings store on every send, so enabling/disabling Telegram at runtime
// takes effect without a restart. Send() silently no-ops when unconfigured.
func buildTelegramSender(ctx context.Context, ss store.SettingsStore) *notify.TelegramSender {
	return notify.NewTelegramSender(func() *notify.TelegramConfig {
		if ss == nil {
			return nil
		}
		cfg, err := ss.GetTelegramConfig(ctx)
		if err != nil || cfg == nil {
			return nil
		}
		return &notify.TelegramConfig{
			BotToken: cfg.BotToken,
			ChatID:   cfg.ChatID,
			Enabled:  cfg.Enabled,
		}
	})
}

// buildWatchNotifiers assembles the watch-alert notifier list: the always-on
// slog fallback, Telegram (no-op until configured), and a webhook if one is set.
func buildWatchNotifiers(sender *notify.TelegramSender) []watcher.WatchAlertNotifier {
	notifiers := []watcher.WatchAlertNotifier{
		&watcher.WatchLogNotifier{},
		watcher.NewTelegramWatchNotifier(sender),
	}
	if url := os.Getenv(alertWebhookEnv); url != "" {
		notifiers = append(notifiers, watcher.NewWatchWebhookNotifier(url))
	}
	return notifiers
}

// buildHealthNotifiers assembles the health-check alert notifier list.
func buildHealthNotifiers(sender *notify.TelegramSender) []healthcheck.HealthCheckAlertNotifier {
	notifiers := []healthcheck.HealthCheckAlertNotifier{
		&healthcheck.HealthCheckLogNotifier{},
		&telegramHealthNotifier{sender: sender},
	}
	if url := os.Getenv(alertWebhookEnv); url != "" {
		notifiers = append(notifiers, healthcheck.NewHealthCheckWebhookNotifier(url))
	}
	return notifiers
}

// telegramHealthNotifier adapts the Telegram sender to the health-check alert
// notifier interface. It lives here rather than in internal/healthcheck so all
// channel wiring stays in one place; the sender no-ops when Telegram is off.
type telegramHealthNotifier struct {
	sender *notify.TelegramSender
}

func (n *telegramHealthNotifier) NotifyHealthCheckAlert(ctx context.Context, alert *healthcheck.HealthCheckAlert) error {
	if n == nil || n.sender == nil {
		return nil
	}
	return n.sender.Send(ctx, formatHealthCheckAlertMessage(alert))
}

// formatHealthCheckAlertMessage renders a health-check transition as HTML for Telegram.
func formatHealthCheckAlertMessage(alert *healthcheck.HealthCheckAlert) string {
	emoji := "⚠️"
	if alert.CurrentStatus == store.HealthCheckUp {
		emoji = "✅"
	}
	msg := fmt.Sprintf(
		"%s <b>Health check %s</b>\n<b>Name:</b> %s\n<b>URL:</b> %s\n<b>Status:</b> %s → %s",
		emoji, alert.CurrentStatus, alert.HealthCheckName, alert.URL, alert.PreviousStatus, alert.CurrentStatus,
	)
	if alert.ErrorMessage != "" {
		msg += "\n<b>Error:</b> " + alert.ErrorMessage
	}
	return msg
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

	// Restrict to owner+group. The socket routes straight into log ingestion
	// with no API-key check, so world-writable (0666) let any local user inject
	// logs. ponytail: 0660 shares with a co-located app via a common group; set
	// OPENTRACE_SOCKET_MODE (octal) to override if a different arrangement is needed.
	mode := os.FileMode(0o660)
	if v := os.Getenv("OPENTRACE_SOCKET_MODE"); v != "" {
		if parsed, err := strconv.ParseUint(v, 8, 32); err == nil {
			mode = os.FileMode(parsed)
		} else {
			slog.Warn("invalid OPENTRACE_SOCKET_MODE, using 0660", "value", v, "error", err)
		}
	}
	if err := os.Chmod(socketPath, mode); err != nil {
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
