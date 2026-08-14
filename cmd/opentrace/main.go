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
	"sync"
	"syscall"
	"time"

	dbstore "github.com/adham90/opentrace/internal/adapter/sqlite"
	"github.com/adham90/opentrace/internal/api"
	"github.com/adham90/opentrace/internal/backup"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/cryptoutil"
	"github.com/adham90/opentrace/internal/healthcheck"
	"github.com/adham90/opentrace/internal/ingest"
	"github.com/adham90/opentrace/internal/jobs"
	logadapter "github.com/adham90/opentrace/internal/logstore/adapter"
	"github.com/adham90/opentrace/internal/logstore/engine"
	logsingest "github.com/adham90/opentrace/internal/logstore/ingest"
	mcpserver "github.com/adham90/opentrace/internal/mcp"
	"github.com/adham90/opentrace/internal/notify"
	"github.com/adham90/opentrace/internal/safe"
	"github.com/adham90/opentrace/internal/version"
	"github.com/adham90/opentrace/internal/watcher"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

const (
	// exitFailure is the status for a command that ran and failed.
	exitFailure = 1
	// exitUsage is the status for a wrong command line (unknown subcommand),
	// kept distinct so scripts can tell a typo from a genuine failure.
	exitUsage = 2
)

func main() {
	err, code := dispatch(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	if code != 0 {
		os.Exit(code)
	}
}

// dispatch routes a subcommand to its handler. It returns the error to report
// (if any) and the process exit status, so an unknown command can exit with a
// usage status instead of silently booting a server.
func dispatch(args []string) (error, int) {
	if len(args) == 0 {
		return withFailure(run())
	}

	switch args[0] {
	case "init":
		return withFailure(runInit())
	case "serve":
		return withFailure(run())
	case "mcp":
		return withFailure(runMCP())
	case "seed":
		return withFailure(runSeed())
	case "backup":
		return withFailure(runBackup())
	case "restore":
		return withFailure(runRestore())
	case "status":
		return withFailure(runStatus())
	case "logs":
		return withFailure(runLogs())
	case "version", "--version", "-v":
		fmt.Println("opentrace " + version.Full())
		return nil, 0
	case "help", "--help", "-h":
		printUsage(os.Stdout)
		return nil, 0
	default:
		// Never fall through to run(): starting the server has side effects
		// (creates the data dir, migrates the DB, may provision an API key,
		// binds the listen port), so a typo like `opentrace statsu` used to
		// boot a second server instead of reporting the mistake.
		printUsage(os.Stderr)
		return fmt.Errorf("unknown command %q (run `opentrace help`)", args[0]), exitUsage
	}
}

// withFailure maps a command result to an exit status.
func withFailure(err error) (error, int) {
	if err != nil {
		return err, exitFailure
	}
	return nil, 0
}

// printUsage writes the command-line help to w.
func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: opentrace [command]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Server commands:")
	fmt.Fprintln(w, "  (none)    Start the server")
	fmt.Fprintln(w, "  init      Initialize database (first-time setup)")
	fmt.Fprintln(w, "  serve     Start the server (same as no command)")
	fmt.Fprintln(w, "  mcp       Start the MCP stdio server")
	fmt.Fprintln(w, "  seed      Initialize sample data")
	fmt.Fprintln(w, "  backup    Create a database backup")
	fmt.Fprintln(w, "  restore   Restore database from a backup")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "CLI commands (connect to a running server):")
	fmt.Fprintln(w, "  status    Show server health and summary stats")
	fmt.Fprintln(w, "  logs      Stream live log tail")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  version   Print version information")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Backup options:")
	fmt.Fprintln(w, "  opentrace backup [-o <path>] [-f]")
	fmt.Fprintln(w, "    -o, --output   Output file (default: opentrace-backup-TIMESTAMP.db)")
	fmt.Fprintln(w, "    -f, --force    Overwrite destination if it exists")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Restore options:")
	fmt.Fprintln(w, "  opentrace restore --from <backup-file>")
	fmt.Fprintln(w, "    -f, --from     Backup file to restore from (required)")
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

// teardownApp shuts an initApp-built process down in dependency order:
// connectors stop writing, the active WAL is sealed (so this hour's logs become
// a searchable segment instead of an orphaned active.wal), then the engine and
// the SQLite handle are closed.
//
// sealWAL must be true only for a process that owns the log store as a writer —
// `serve` and `seed`. A short-lived reader like `opentrace mcp` shares the data
// dir with a possibly-running server, and sealing there renamed the live
// server's active.wal out from under it on every MCP session exit, silently
// dropping everything ingested afterwards.
func teardownApp(deps *server.Deps, logEngine *engine.Store, sealWAL bool) {
	if deps != nil && deps.Registry != nil {
		deps.Registry.CloseAll()
	}
	if logEngine != nil {
		if sealWAL {
			if err := logEngine.SealCurrentHour(); err != nil {
				slog.Error("final log seal failed", "error", err)
			}
		}
		logEngine.Close()
	}
	if deps != nil && deps.DB != nil {
		deps.DB.Close()
	}
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
	// A reader, not the store's owner: never seal here.
	defer teardownApp(deps, logEngine, false)

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
func ensureAPIKey(ctx context.Context, deps *server.Deps) error {
	if deps.Cfg != nil && deps.Cfg.APIKey != "" {
		return nil // supplied via OPENTRACE_API_KEY
	}
	if deps.SettingsStore != nil {
		key, err := deps.SettingsStore.GetAPIKey(ctx)
		if err != nil {
			// A read failure is NOT "no key". Falling through here would
			// generate and persist a fresh key, silently locking out every
			// already-provisioned SDK and CLI. Fail closed: refuse to start
			// rather than rotate a key we could not read.
			return fmt.Errorf("reading configured API key: %w", err)
		}
		if key != "" {
			return nil // already provisioned
		}
	}
	if os.Getenv("OPENTRACE_DISABLE_AUTH") == "true" {
		slog.Warn("AUTH DISABLED: ingest and CLI-read endpoints accept unauthenticated requests (OPENTRACE_DISABLE_AUTH=true). Do not expose this instance to untrusted networks.")
		return nil
	}
	if deps.SettingsStore == nil {
		slog.Error("no API key configured and no settings store to provision one; set OPENTRACE_API_KEY or OPENTRACE_DISABLE_AUTH=true")
		return nil
	}
	key, err := cryptoutil.GenerateAPIKey()
	if err != nil {
		return fmt.Errorf("generating API key: %w", err)
	}
	if err := deps.SettingsStore.SetAPIKey(ctx, key); err != nil {
		return fmt.Errorf("persisting auto-generated API key: %w", err)
	}
	slog.Warn("no API key was configured — generated one automatically; configure your SDKs with it", "api_key", key)
	return nil
}

// run starts the full OpenTrace server. The initialization sequence is:
//
//  1. initApp          — load .env + config, create data dir, open SQLite,
//     run migrations, health-check the DB, build stores,
//     create connector registry, reconnect saved connectors.
//  2. Watch subsystem  — WatchMetrics, WatchEvaluator, WatchEvidenceBuilder,
//     WatchStreamEvaluator (reactive on log ingestion).
//  3. MCP tool catalog — build once for the /tools introspection page.
//  4. Health checks    — scheduler created early so the HTTP server can read
//     reliability data.
//  5. Job queue        — persistent, restart-safe background processing
//     (worker + scheduler with recurring cleanup/aggregation).
//  6. HTTP server      — api.NewServerWithDeps wires routes + modules,
//     then http.Server starts in a goroutine.
//  7. Signal handler   — blocks on SIGINT/SIGTERM, then shuts down all
//     background components and the HTTP server gracefully.
func run() error {
	slog.Info("starting", "version", version.Full())

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	deps, logEngine, err := initApp(ctx)
	if err != nil {
		return err
	}
	// Teardown order (connectors → seal WAL → close engine → close DB) lives in
	// teardownApp; it runs after gracefulShutdown has drained HTTP and stopped
	// the background workers, so nothing is still writing when the WAL is sealed.
	defer teardownApp(deps, logEngine, true)

	// Never silently run with ingest/CLI endpoints wide open.
	if err := ensureAPIKey(ctx, deps); err != nil {
		return err
	}

	startHourlySeal(ctx, logEngine)

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
	slackSender := buildSlackSender(ctx, deps.SettingsStore)
	watchNotifiers := buildWatchNotifiers(telegramSender, slackSender)
	healthNotifiers := buildHealthNotifiers(telegramSender, slackSender)

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

	jobWorker, jobScheduler := startJobQueue(ctx, deps, jobQueue)

	fatal := make(chan error, 1)
	startHTTPServer(ctx, httpServer, deps, fatal)

	// Start Unix socket listener for local log ingestion (skip HTTP overhead)
	var unixSrv *unixIngestServer
	if deps.Cfg.SocketPath != "" {
		var err error
		unixSrv, err = startUnixSocketListener(deps.Cfg.SocketPath, srv.IngestHandler())
		if err != nil {
			slog.Error("failed to start unix socket listener", "path", deps.Cfg.SocketPath, "error", err)
		}
	}

	// A failed listen must still shut down cleanly: the deferred teardown seals
	// the active WAL, and skipping it loses this hour's logs.
	var listenErr error
	select {
	case <-done:
		slog.Info("shutting down")
	case listenErr = <-fatal:
		slog.Error("listen failed; shutting down", "error", listenErr)
	}

	gracefulShutdown(shutdownParams{
		CancelCtx:  cancelCtx,
		HTTPServer: httpServer,
		APIServer:  srv,
		UnixServer: unixSrv,
		// watchStream is drained here too: it dispatches alert notifications
		// off the ingest path, and an alert that fired but was never delivered
		// is the worst outcome for an alerting system. Workers stop after the
		// HTTP and unix drains, so the final batch's evaluation still lands.
		Workers: []backgroundStopper{watchSched, watchStream, hcSched, jobWorker, jobScheduler},
	})

	// Non-nil only when the listener failed, so the process still exits
	// non-zero after shutting down cleanly.
	return listenErr
}

// startJobQueue registers the background job handlers and recurring schedules,
// reclaims jobs orphaned by a previous crash, and starts the worker and
// scheduler. Returns both so shutdown can stop them.
func startJobQueue(ctx context.Context, deps *server.Deps, jobQueue *jobs.Queue) (*jobs.Worker, *jobs.Scheduler) {
	jobWorker := jobs.NewWorker(jobQueue)
	jobScheduler := jobs.NewScheduler(jobQueue)
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

	return jobWorker, jobScheduler
}

// startHTTPServer prints the connect banner and serves in the background.
func startHTTPServer(ctx context.Context, httpServer *http.Server, deps *server.Deps, fatal chan<- error) {
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
			// Report instead of os.Exit: exiting here skipped run()'s deferred
			// teardown, so the active WAL was never sealed and the SQLite handle
			// never closed. Hand the error back and let the normal shutdown path
			// run.
			select {
			case fatal <- err:
			default:
			}
		}
	}()
}

// shutdownTimeout bounds the whole graceful drain: in-flight HTTP requests,
// in-flight Unix-socket ingests, and the final WAL seal.
const shutdownTimeout = 10 * time.Second

// backgroundStopper is any long-running component stopped during shutdown.
type backgroundStopper interface{ Stop() }

// shutdownParams collects everything gracefulShutdown must tear down.
type shutdownParams struct {
	CancelCtx  context.CancelFunc
	HTTPServer *http.Server
	APIServer  *api.Server
	UnixServer *unixIngestServer
	Workers    []backgroundStopper
}

// gracefulShutdown tears the server down in dependency order:
//
//  1. stop accepting new work (Unix listener),
//  2. drain in-flight HTTP handlers, then flush the API server's queues
//     (srv.Shutdown flushes the async ingest queue into the log engine),
//  3. drain in-flight Unix-socket ingests,
//  4. stop background workers.
//
// run()'s deferred teardownApp then closes the connectors, seals the active WAL
// and closes the engine. Connector teardown used to run FIRST, killing pgx/mysql
// pools underneath the in-flight /mcp requests that the HTTP drain exists to
// protect; it now happens after every handler has finished.
func gracefulShutdown(p shutdownParams) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Stop accepting new Unix-socket connections; in-flight ones are drained below.
	if p.UnixServer != nil {
		p.UnixServer.CloseListener()
	}

	p.CancelCtx()

	// Drain the HTTP server FIRST so all in-flight handlers finish before we
	// tear down app-level channels. srv.Shutdown() closes the audit channel;
	// doing that while a mutating request is still running would panic on a
	// send-to-closed-channel (even with the select/default) and lose the audit.
	if err := p.HTTPServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP shutdown error", "error", err)
	}
	if p.APIServer != nil {
		if err := p.APIServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("SSE shutdown error", "error", err)
		}
	}

	if p.UnixServer != nil {
		p.UnixServer.WaitInFlight(shutdownCtx)
		slog.Info("unix socket listener stopped")
	}

	for _, w := range p.Workers {
		if w != nil {
			w.Stop()
		}
	}
	slog.Info("background jobs stopped")
}

// startHourlySeal runs the process's sealing authority: it seals the active WAL
// at every hour boundary, starting with the FIRST one after startup. The
// previous version armed a 1-hour ticker only after the initial wait fired
// without sealing, so the first seal landed 1-2 hours in and a server restarted
// more often than that never sealed at all.
func startHourlySeal(ctx context.Context, logEngine *engine.Store) {
	if logEngine == nil {
		return
	}
	safe.Go("hourly-seal", func() {
		timer := time.NewTimer(time.Until(nextHourBoundary(time.Now().UTC())))
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if err := logEngine.SealCurrentHour(); err != nil {
					slog.Error("hourly seal failed", "error", err)
				}
				// Recompute from the clock each time so the schedule cannot
				// drift off the hour boundary.
				timer.Reset(time.Until(nextHourBoundary(time.Now().UTC())))
			}
		}
	})
}

// nextHourBoundary returns the first wall-clock hour boundary strictly after t.
func nextHourBoundary(t time.Time) time.Time {
	return t.Truncate(time.Hour).Add(time.Hour)
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

// buildSlackSender returns a Slack sender whose webhook URL is read lazily from
// the settings store on every send, so enabling/disabling Slack at runtime takes
// effect without a restart. Send() silently no-ops when unconfigured.
func buildSlackSender(ctx context.Context, ss store.SettingsStore) *notify.SlackSender {
	return notify.NewSlackSender(func() *notify.SlackConfig {
		if ss == nil {
			return nil
		}
		cfg, err := ss.GetSlackConfig(ctx)
		if err != nil || cfg == nil {
			return nil
		}
		return &notify.SlackConfig{
			WebhookURL: cfg.WebhookURL,
			Enabled:    cfg.Enabled,
		}
	})
}

// buildWatchNotifiers assembles the watch-alert notifier list: the always-on
// slog fallback, the chat channels (no-op until configured), and a webhook if
// one is set.
func buildWatchNotifiers(telegram *notify.TelegramSender, slack *notify.SlackSender) []watcher.WatchAlertNotifier {
	notifiers := []watcher.WatchAlertNotifier{
		&watcher.WatchLogNotifier{},
		watcher.NewChatWatchNotifier(telegram),
		watcher.NewChatWatchNotifier(slack),
	}
	if url := os.Getenv(alertWebhookEnv); url != "" {
		notifiers = append(notifiers, watcher.NewWatchWebhookNotifier(url))
	}
	return notifiers
}

// buildHealthNotifiers assembles the health-check alert notifier list.
func buildHealthNotifiers(telegram *notify.TelegramSender, slack *notify.SlackSender) []healthcheck.HealthCheckAlertNotifier {
	notifiers := []healthcheck.HealthCheckAlertNotifier{
		&healthcheck.HealthCheckLogNotifier{},
		&chatHealthNotifier{sender: telegram},
		&chatHealthNotifier{sender: slack},
	}
	if url := os.Getenv(alertWebhookEnv); url != "" {
		notifiers = append(notifiers, healthcheck.NewHealthCheckWebhookNotifier(url))
	}
	return notifiers
}

// messageSender delivers a plain notification message to a chat channel.
// Both *notify.TelegramSender and *notify.SlackSender satisfy it.
type messageSender interface {
	Send(ctx context.Context, message string) error
}

// chatHealthNotifier adapts a chat sender to the health-check alert notifier
// interface. It lives here rather than in internal/healthcheck so all channel
// wiring stays in one place; senders no-op when their channel is off.
type chatHealthNotifier struct {
	sender messageSender
}

func (n *chatHealthNotifier) NotifyHealthCheckAlert(ctx context.Context, alert *healthcheck.HealthCheckAlert) error {
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

const (
	// maxUnixPayloadBytes is the maximum payload size accepted over the Unix socket (10 MB).
	maxUnixPayloadBytes = 10 << 20
	// unixWriteTimeout bounds writing the 4-byte status reply.
	unixWriteTimeout = 5 * time.Second
	// maxUnixConns caps concurrent in-flight socket ingests. Beyond it clients
	// get 503 immediately instead of the process accumulating goroutines.
	maxUnixConns = 128
)

// unixReadTimeout bounds how long a client may take to deliver its length
// prefix and payload. Without it a stalled client pinned a goroutine, an fd and
// an up-to-10MB buffer until process exit. A variable so tests can shorten it.
var unixReadTimeout = 30 * time.Second

// unixIngestServer owns the Unix-socket ingest listener and tracks in-flight
// connections so shutdown can drain them before the log engine is sealed and
// closed underneath a handler that already accepted a payload.
type unixIngestServer struct {
	listener net.Listener
	path     string
	wg       sync.WaitGroup
	sem      chan struct{}

	closeOnce sync.Once
}

// CloseListener stops accepting new connections and removes the socket file.
// In-flight connections keep running until WaitInFlight returns.
func (s *unixIngestServer) CloseListener() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.listener.Close()
		os.Remove(s.path)
	})
}

// WaitInFlight blocks until every accepted connection has finished or ctx is
// done.
func (s *unixIngestServer) WaitInFlight(ctx context.Context) {
	if s == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		slog.Warn("unix socket: in-flight connections did not drain before timeout")
	}
}

// Close closes the listener and waits (bounded) for in-flight connections.
// Used by tests and any caller that just wants a full stop.
func (s *unixIngestServer) Close() error {
	if s == nil {
		return nil
	}
	s.CloseListener()
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	s.WaitInFlight(ctx)
	return nil
}

// startUnixSocketListener creates a Unix domain socket at socketPath and
// spawns a goroutine that accepts connections. Each connection uses a simple
// length-prefixed binary protocol (4-byte big-endian length + payload).
// Returns the server so the caller can drain and close it during shutdown.
func startUnixSocketListener(socketPath string, handler *ingest.Handler) (*unixIngestServer, error) {
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

	s := &unixIngestServer{
		listener: listener,
		path:     socketPath,
		sem:      make(chan struct{}, maxUnixConns),
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				// Accept returns an error when the listener is closed during shutdown
				return
			}
			select {
			case s.sem <- struct{}{}:
			default:
				// At capacity: answer immediately instead of queueing goroutines.
				writeUnixStatus(conn, http.StatusServiceUnavailable)
				conn.Close()
				continue
			}
			s.wg.Add(1)
			go func() {
				defer func() {
					<-s.sem
					s.wg.Done()
				}()
				handleUnixConnection(conn, handler)
			}()
		}
	}()

	return s, nil
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

	// A client that connects and then stalls must not pin this goroutine (and
	// its payload buffer) forever.
	conn.SetReadDeadline(time.Now().Add(unixReadTimeout))

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
		decompressed, status := decompressUnixPayload(payload)
		if status != http.StatusOK {
			// Previously an over-limit or corrupt gzip fell through with the
			// still-compressed bytes, which the JSON handler reported as 400.
			writeUnixStatus(conn, status)
			return
		}
		payload = decompressed
	}

	// Build a synthetic HTTP request and route through the existing handler
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.HandleIngestLogs(rec, req)

	writeUnixStatus(conn, rec.Code)
}

// decompressUnixPayload gunzips a socket payload. It returns the decompressed
// bytes and http.StatusOK on success, or the status to report to the client:
// 413 when the decompressed size exceeds the limit, 400 when the stream is
// corrupt.
func decompressUnixPayload(payload []byte) ([]byte, int) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, http.StatusBadRequest
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(io.LimitReader(reader, int64(maxUnixPayloadBytes)+1))
	if err != nil {
		return nil, http.StatusBadRequest
	}
	if len(decompressed) > maxUnixPayloadBytes {
		return nil, http.StatusRequestEntityTooLarge
	}
	return decompressed, http.StatusOK
}

// writeUnixStatus writes a 4-byte big-endian status code to the connection.
func writeUnixStatus(conn net.Conn, code int) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(code))
	conn.SetWriteDeadline(time.Now().Add(unixWriteTimeout))
	conn.Write(buf[:])
}
