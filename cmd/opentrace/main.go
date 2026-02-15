package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	mcpserver "github.com/adham90/opentrace/internal/mcp"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/version"
	"github.com/adham90/opentrace/internal/vmagent"
	"github.com/adham90/opentrace/internal/watcher"
	"github.com/adham90/opentrace/internal/web"
)

// appDeps holds shared application dependencies initialized by initApp.
type appDeps struct {
	db               *sql.DB
	dsStore          store.DataSourceStore
	logStore         store.LogStore
	watcherStore     store.WatcherStore
	alertStore       store.AlertStore
	serverStore      store.ServerStore
	metricStore      store.MetricStore
	userStore        store.UserStore
	sessionStore     store.SessionStore
	settingsStore    store.SettingsStore
	mcpActivityStore store.MCPActivityStore
	alertGroupStore  store.AlertGroupStore
	auditStore       store.AuditStore
	watchStore       store.WatchStore
	registry         *connector.Registry
	cfg              *config.Config
}

func main() {
	var err error
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "mcp":
			err = runMCP()
		case "agent":
			err = runAgent()
		case "seed":
			err = runSeed()
		case "version":
			fmt.Println("opentrace " + version.Full())
			return
		case "help", "--help", "-h":
			fmt.Println("Usage: opentrace [command]")
			fmt.Println()
			fmt.Println("Commands:")
			fmt.Println("  (none)    Start the web server")
			fmt.Println("  agent     Run the metrics collection agent")
			fmt.Println("  mcp       Start the MCP stdio server")
			fmt.Println("  seed      Initialize sample data")
			fmt.Println("  version   Print version information")
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
func initApp(ctx context.Context) (*appDeps, error) {
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
	db, err := store.OpenSQLite(cfg.DatabasePath())
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Run migrations
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	slog.Info("database ready")

	// Initialize stores
	dsStore := store.NewDataSourceStore(db)
	logStore := store.NewLogStore(db)
	watcherStore := store.NewWatcherStore(db)
	alertStore := store.NewAlertStore(db)
	serverStore := store.NewServerStore(db)
	metricStore := store.NewMetricStore(db)
	userStore := store.NewUserStore(db)
	sessionStore := store.NewSessionStore(db)
	settingsStore := store.NewSettingsStore(db)
	mcpActivityStore := store.NewMCPActivityStore(db)
	alertGroupStore := store.NewAlertGroupStore(db)
	auditStore := store.NewAuditStore(db)
	watchStore := store.NewWatchStore(db)

	// Initialize registry and reconnect previously-configured connectors
	registry := connector.NewRegistry()
	reconnectConnectors(ctx, dsStore, logStore, registry, cfg)

	return &appDeps{
		db:               db,
		dsStore:          dsStore,
		logStore:         logStore,
		watcherStore:     watcherStore,
		alertStore:       alertStore,
		serverStore:      serverStore,
		metricStore:      metricStore,
		userStore:        userStore,
		sessionStore:     sessionStore,
		settingsStore:    settingsStore,
		mcpActivityStore: mcpActivityStore,
		alertGroupStore:  alertGroupStore,
		auditStore:       auditStore,
		watchStore:       watchStore,
		registry:         registry,
		cfg:              cfg,
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
	defer deps.db.Close()
	defer deps.registry.CloseAll()

	watchMetrics := watcher.NewWatchMetrics(deps.logStore)

	return mcpserver.Serve(mcpserver.Deps{
		Registry:         deps.registry,
		WatcherStore:     deps.watcherStore,
		AlertStore:       deps.alertStore,
		LogStore:         deps.logStore,
		ServerStore:      deps.serverStore,
		MetricStore:      deps.metricStore,
		UserStore:        deps.userStore,
		MCPToken:         os.Getenv("OPENTRACE_MCP_TOKEN"),
		ServerName:       os.Getenv("OPENTRACE_MCP_NAME"),
		DataSourceStore:  deps.dsStore,
		SettingsStore:    deps.settingsStore,
		Config:           deps.cfg,
		MCPActivityStore: deps.mcpActivityStore,
		AlertGroupStore:  deps.alertGroupStore,
		AuditStore:       deps.auditStore,
		WatchStore:       deps.watchStore,
		WatchMetrics:     watchMetrics,
	})
}

// runAgent starts the VM metrics collection agent.
func runAgent() error {
	config.LoadEnvFile(".env")

	cfg, err := vmagent.LoadConfig()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-done
		cancel()
	}()

	a := vmagent.New(cfg)
	return a.Run(ctx)
}

func run() error {
	slog.Info("starting", "version", version.Full())

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	deps, err := initApp(ctx)
	if err != nil {
		return err
	}
	defer deps.db.Close()

	// Agent-first watch components
	watchMetrics := watcher.NewWatchMetrics(deps.logStore)

	// Build MCP tool catalog for the /tools page (auto-detected from MCP registrations).
	toolCatalog := mcpserver.BuildCatalog(mcpserver.Deps{
		Registry:        deps.registry,
		WatcherStore:    deps.watcherStore,
		AlertStore:      deps.alertStore,
		LogStore:        deps.logStore,
		ServerStore:     deps.serverStore,
		MetricStore:     deps.metricStore,
		DataSourceStore: deps.dsStore,
		SettingsStore:   deps.settingsStore,
		Config:          deps.cfg,
		AlertGroupStore: deps.alertGroupStore,
		AuditStore:      deps.auditStore,
		WatchStore:      deps.watchStore,
		WatchMetrics:    watchMetrics,
	})

	// Agent-first watch evaluator + stream (reactive on log ingestion)
	watchEvaluator := watcher.NewWatchEvaluator(watchMetrics, deps.watchStore)
	watchEvidenceBuilder := watcher.NewWatchEvidenceBuilder(deps.logStore, watchMetrics)
	watchStream := watcher.NewWatchStreamEvaluator(deps.watchStore, watchEvaluator, watchEvidenceBuilder)

	// Create server
	srv := web.NewServerWithDeps(web.ServerDeps{
		DB:               deps.db,
		DSStore:          deps.dsStore,
		LogStore:         deps.logStore,
		WatcherStore:     deps.watcherStore,
		AlertStore:       deps.alertStore,
		ServerStore:      deps.serverStore,
		MetricStore:      deps.metricStore,
		UserStore:        deps.userStore,
		SessionStore:     deps.sessionStore,
		SettingsStore:    deps.settingsStore,
		Registry:         deps.registry,
		ToolCatalog:      toolCatalog,
		Cfg:              deps.cfg,
		MCPActivityStore: deps.mcpActivityStore,
		AlertGroupStore:  deps.alertGroupStore,
		AuditStore:       deps.auditStore,
		WatchStreamEvaluator: watchStream,
		WatchStore:           deps.watchStore,
		WatchMetrics:         watchMetrics,
	})

	httpServer := &http.Server{
		Addr:         deps.cfg.ListenAddr,
		Handler:      srv.Router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second, // higher for SSE endpoints
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	// Start agent-first watch scheduler
	watchSessionMgr := watcher.NewWatchSessionManager(deps.watchStore, watchMetrics)
	watchSched := watcher.NewWatchScheduler(watcher.WatchSchedulerOpts{
		WatchStore:      deps.watchStore,
		LogStore:        deps.logStore,
		Evaluator:       watchEvaluator,
		EvidenceBuilder: watchEvidenceBuilder,
		SessionManager:  watchSessionMgr,
	})
	watchSched.Start(ctx)

	// Background: clean expired sessions every 15 minutes
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := deps.sessionStore.DeleteExpired(ctx); err != nil {
					slog.Warn("session cleanup failed", "error", err)
				} else if n > 0 {
					slog.Info("cleaned expired sessions", "count", n)
				}
			}
		}
	}()

	// Background: mark stale servers offline every 60s
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := deps.serverStore.MarkStaleOffline(ctx, 2*time.Minute); err != nil {
					slog.Warn("MarkStaleOffline failed", "error", err)
				} else if n > 0 {
					slog.Info("marked stale servers offline", "count", n)
				}
			}
		}
	}()

	// Background: unified data retention job every hour
	go func() {
		metricRetentionDays := 0
		if v := os.Getenv("OPENTRACE_METRIC_RETENTION_DAYS"); v != "" {
			var d int
			if _, err := fmt.Sscanf(v, "%d", &d); err == nil && d > 0 {
				metricRetentionDays = d
			}
		}

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			settings, err := deps.settingsStore.GetRetention(ctx)
			if err != nil {
				slog.Warn("reading retention settings failed", "error", err)
				continue
			}

			globalDays := settings.RetentionDays

			if globalDays > 0 {
				retention := time.Duration(globalDays) * 24 * time.Hour
				if n, err := deps.logStore.Prune(ctx, retention); err != nil {
					slog.Warn("log prune failed", "error", err)
				} else if n > 0 {
					slog.Info("pruned old logs", "count", n)
				}
				if n, err := deps.alertStore.Prune(ctx, retention); err != nil {
					slog.Warn("alert prune failed", "error", err)
				} else if n > 0 {
					slog.Info("pruned old alerts", "count", n)
				}
				if n, err := deps.mcpActivityStore.Prune(ctx, retention); err != nil {
					slog.Warn("mcp activity prune failed", "error", err)
				} else if n > 0 {
					slog.Info("pruned old mcp activity records", "count", n)
				}
				if deps.auditStore != nil {
					if n, err := deps.auditStore.Prune(ctx, retention); err != nil {
						slog.Warn("audit log prune failed", "error", err)
					} else if n > 0 {
						slog.Info("pruned old audit log entries", "count", n)
					}
				}
			}

			metricDays := metricRetentionDays
			if metricDays == 0 {
				metricDays = globalDays
			}
			if metricDays > 0 {
				retention := time.Duration(metricDays) * 24 * time.Hour
				if n, err := deps.metricStore.Prune(ctx, retention); err != nil {
					slog.Warn("metric prune failed", "error", err)
				} else if n > 0 {
					slog.Info("pruned old metrics", "count", n)
				}
			}
		}
	}()

	// Background: auto-update check (every hour)
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if isDockerEnv() {
					continue
				}
				if deps.settingsStore == nil {
					continue
				}
				enabled, err := deps.settingsStore.GetAutoUpdate(ctx)
				if err != nil || !enabled {
					continue
				}
				updater := web.NewSelfUpdater("adham90", "opentrace")
				result, err := updater.Update(ctx)
				if err != nil {
					slog.Warn("auto-update failed", "error", err)
					continue
				}
				slog.Info("auto-update succeeded, restarting", "old_version", result.OldVersion, "new_version", result.NewVersion)
				select {
				case <-srv.RestartCh():
				default:
					p, _ := os.FindProcess(os.Getpid())
					p.Signal(os.Interrupt)
				}
			}
		}
	}()

	go func() {
		if deps.cfg.TLSCert != "" && deps.cfg.TLSKey != "" {
			slog.Info("listening (HTTPS)", "addr", deps.cfg.ListenAddr)
			if err := httpServer.ListenAndServeTLS(deps.cfg.TLSCert, deps.cfg.TLSKey); err != nil && err != http.ErrServerClosed {
				slog.Error("listen error", "error", err)
				os.Exit(1)
			}
		} else {
			slog.Info("listening", "addr", deps.cfg.ListenAddr)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("listen error", "error", err)
				os.Exit(1)
			}
		}
	}()

	// Wait for either OS signal or self-update restart request
	shouldRestart := false
	select {
	case <-done:
		slog.Info("shutting down")
	case <-srv.RestartCh():
		slog.Info("restarting after self-update")
		shouldRestart = true
	}

	cancelCtx()
	watchSched.Stop()
	deps.registry.CloseAll()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("SSE shutdown error", "error", err)
	}

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP shutdown error", "error", err)
	}

	if shouldRestart {
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("restart: cannot find executable: %w", err)
		}
		slog.Info("restarting process", "path", execPath)
		return syscall.Exec(execPath, os.Args, os.Environ())
	}

	return nil
}

func isDockerEnv() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// reconnectConnectors re-registers connectors that were previously connected.
func reconnectConnectors(ctx context.Context, dsStore store.DataSourceStore, logStore store.LogStore, registry *connector.Registry, cfg *config.Config) {
	dataSources, err := dsStore.List(ctx, store.ListDataSourceParams{})
	if err != nil {
		slog.Warn("failed to list connectors for reconnect", "error", err)
		return
	}

	for _, ds := range dataSources {
		if ds.Status != store.StatusConnected {
			continue
		}

		c, err := connector.CreateConnector(ctx, ds, logStore, cfg)
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
