package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/llm"
	mcpserver "github.com/adham90/opentrace/internal/mcp"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/version"
	"github.com/adham90/opentrace/internal/vmagent"
	"github.com/adham90/opentrace/internal/watcher"
	"github.com/adham90/opentrace/internal/web"
)

// appDeps holds shared application dependencies initialized by initApp.
type appDeps struct {
	db            *sql.DB
	dsStore       store.DataSourceStore
	logStore      store.LogStore
	watcherStore  store.WatcherStore
	alertStore    store.AlertStore
	serverStore   store.ServerStore
	metricStore   store.MetricStore
	userStore     store.UserStore
	sessionStore  store.SessionStore
	settingsStore    store.SettingsStore
	mcpActivityStore store.MCPActivityStore
	alertGroupStore  store.AlertGroupStore
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
	log.Println("database ready")

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
		registry:         registry,
		cfg:              cfg,
	}, nil
}

// runMCP starts the MCP stdio server. All log output goes to stderr to keep
// stdout clean for the JSON-RPC stream.
func runMCP() error {
	log.SetOutput(os.Stderr)

	ctx := context.Background()
	deps, err := initApp(ctx)
	if err != nil {
		return err
	}
	defer deps.db.Close()
	defer deps.registry.CloseAll()

	// Create LLM provider + executor for on-demand watcher execution.
	defaultLLM, _ := llm.NewLLMProvider(deps.cfg)
	providerCache := llm.NewProviderCache(deps.cfg, defaultLLM)
	runStore := store.NewWatcherRunStore(deps.db)
	eventHub := watcher.NewEventHub()
	executor := watcher.NewExecutor(
		deps.watcherStore,
		runStore,
		deps.alertStore,
		deps.registry,
		providerCache,
		agent.RunConfig{
			MaxSteps:            deps.cfg.MaxAgentSteps,
			MaxToolCalls:        deps.cfg.MaxToolCalls,
			MaxObservationBytes: deps.cfg.MaxObservationBytes,
		},
		eventHub,
	)

	return mcpserver.Serve(mcpserver.Deps{
		Registry:         deps.registry,
		WatcherStore:     deps.watcherStore,
		AlertStore:       deps.alertStore,
		WatcherRunStore:  runStore,
		LogStore:         deps.logStore,
		ServerStore:      deps.serverStore,
		MetricStore:      deps.metricStore,
		UserStore:        deps.userStore,
		MCPToken:         os.Getenv("OPENTRACE_MCP_TOKEN"),
		ServerName:       os.Getenv("OPENTRACE_MCP_NAME"),
		DataSourceStore:  deps.dsStore,
		SettingsStore:    deps.settingsStore,
		Executor:         executor,
		Config:           deps.cfg,
		MCPActivityStore: deps.mcpActivityStore,
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
	log.Printf("opentrace %s", version.Full())

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	deps, err := initApp(ctx)
	if err != nil {
		return err
	}
	defer deps.db.Close()

	// Create LLM provider cache for per-watcher model selection
	defaultLLM, err := llm.NewLLMProvider(deps.cfg)
	if err != nil {
		log.Printf("warning: default LLM provider unavailable: %v", err)
	}
	providerCache := llm.NewProviderCache(deps.cfg, defaultLLM)
	modelRegistry := llm.NewModelRegistry(deps.cfg)

	// Create watcher run store and clean up stale runs from previous crashes
	runStore := store.NewWatcherRunStore(deps.db)
	if n, err := runStore.FailStaleRuns(ctx, 10*time.Minute); err != nil {
		log.Printf("warning: failed to clean stale runs: %v", err)
	} else if n > 0 {
		log.Printf("cleaned up %d stale watcher run(s)", n)
	}

	eventHub := watcher.NewEventHub()
	executor := watcher.NewExecutor(
		deps.watcherStore,
		runStore,
		deps.alertStore,
		deps.registry,
		providerCache,
		agent.RunConfig{
			MaxSteps:            deps.cfg.MaxAgentSteps,
			MaxToolCalls:        deps.cfg.MaxToolCalls,
			MaxObservationBytes: deps.cfg.MaxObservationBytes,
		},
		eventHub,
	)

	// Create rule evaluator for watcher preview and MCP SSE.
	ruleEvaluator := watcher.NewRuleEvaluator(deps.registry, deps.logStore, deps.dsStore)

	// Build MCP tool catalog for the /tools page (auto-detected from MCP registrations).
	toolCatalog := mcpserver.BuildCatalog(mcpserver.Deps{
		Registry:        deps.registry,
		WatcherStore:    deps.watcherStore,
		AlertStore:      deps.alertStore,
		WatcherRunStore: runStore,
		LogStore:        deps.logStore,
		ServerStore:     deps.serverStore,
		MetricStore:     deps.metricStore,
	})

	// Create server
	srv := web.NewServerWithDeps(web.ServerDeps{
		DB:               deps.db,
		DSStore:          deps.dsStore,
		LogStore:         deps.logStore,
		WatcherStore:     deps.watcherStore,
		RunStore:         runStore,
		AlertStore:       deps.alertStore,
		ServerStore:      deps.serverStore,
		MetricStore:      deps.metricStore,
		UserStore:        deps.userStore,
		SessionStore:     deps.sessionStore,
		SettingsStore:    deps.settingsStore,
		Registry:         deps.registry,
		ToolCatalog:      toolCatalog,
		Cfg:              deps.cfg,
		Executor:         executor,
		EventHub:         eventHub,
		ModelRegistry:    modelRegistry,
		RuleEvaluator:    ruleEvaluator,
		MCPActivityStore: deps.mcpActivityStore,
		AlertGroupStore:  deps.alertGroupStore,
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

	// Start watcher scheduler for automatic scheduled runs
	sched := watcher.NewScheduler(watcher.SchedulerOpts{
		WatcherStore:  deps.watcherStore,
		RunStore:      runStore,
		AlertStore:    deps.alertStore,
		Registry:      deps.registry,
		ProviderCache: providerCache,
		AgentCfg: agent.RunConfig{
			MaxSteps:            deps.cfg.MaxAgentSteps,
			MaxToolCalls:        deps.cfg.MaxToolCalls,
			MaxObservationBytes: deps.cfg.MaxObservationBytes,
		},
		EventHub: eventHub,
	})
	sched.Start(ctx)

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
					log.Printf("WARN: session cleanup: %v", err)
				} else if n > 0 {
					log.Printf("cleaned %d expired session(s)", n)
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
					log.Printf("WARN: MarkStaleOffline: %v", err)
				} else if n > 0 {
					log.Printf("marked %d stale server(s) offline", n)
				}
			}
		}
	}()

	// Background: unified data retention job every hour
	go func() {
		// Preserve OPENTRACE_METRIC_RETENTION_DAYS as metric-specific override
		metricRetentionDays := 0 // 0 means use global setting
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

			// Read global retention setting from DB each tick
			settings, err := deps.settingsStore.GetRetention(ctx)
			if err != nil {
				log.Printf("WARN: reading retention settings: %v", err)
				continue
			}

			globalDays := settings.RetentionDays

			// Prune logs, watcher runs, alerts (skip if 0 = keep forever)
			if globalDays > 0 {
				retention := time.Duration(globalDays) * 24 * time.Hour
				if n, err := deps.logStore.Prune(ctx, retention); err != nil {
					log.Printf("WARN: log prune: %v", err)
				} else if n > 0 {
					log.Printf("pruned %d old log(s)", n)
				}
				if n, err := runStore.Prune(ctx, retention); err != nil {
					log.Printf("WARN: watcher run prune: %v", err)
				} else if n > 0 {
					log.Printf("pruned %d old watcher run(s)", n)
				}
				if n, err := deps.alertStore.Prune(ctx, retention); err != nil {
					log.Printf("WARN: alert prune: %v", err)
				} else if n > 0 {
					log.Printf("pruned %d old alert(s)", n)
				}
				if n, err := deps.mcpActivityStore.Prune(ctx, retention); err != nil {
					log.Printf("WARN: mcp activity prune: %v", err)
				} else if n > 0 {
					log.Printf("pruned %d old mcp activity record(s)", n)
				}
			}

			// Prune metrics: use env var override if set, else global setting
			metricDays := metricRetentionDays
			if metricDays == 0 {
				metricDays = globalDays
			}
			if metricDays > 0 {
				retention := time.Duration(metricDays) * 24 * time.Hour
				if n, err := deps.metricStore.Prune(ctx, retention); err != nil {
					log.Printf("WARN: metric prune: %v", err)
				} else if n > 0 {
					log.Printf("pruned %d old metric(s)", n)
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
					log.Printf("auto-update: %v", err)
					continue
				}
				log.Printf("auto-update: updated %s → %s, restarting...", result.OldVersion, result.NewVersion)
				// Trigger restart via the server's restart channel
				select {
				case <-srv.RestartCh():
					// Already closing
				default:
					// Signal restart (close is done by the server when it gets RestartCh)
					// We need to trigger shutdown; send signal to self
					p, _ := os.FindProcess(os.Getpid())
					p.Signal(os.Interrupt)
				}
			}
		}
	}()

	go func() {
		log.Printf("listening on %s", deps.cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	// Wait for either OS signal or self-update restart request
	shouldRestart := false
	select {
	case <-done:
		log.Println("shutting down...")
	case <-srv.RestartCh():
		log.Println("restarting after self-update...")
		shouldRestart = true
	}

	// Cancel the root context to signal all background goroutines to stop
	cancelCtx()

	sched.Stop()
	deps.registry.CloseAll()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Shutdown SSE sessions before the HTTP server.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("SSE shutdown error: %v", err)
	}

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}

	if shouldRestart {
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("restart: cannot find executable: %w", err)
		}
		log.Printf("exec %s %v", execPath, os.Args)
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
		log.Printf("warning: failed to list connectors for reconnect: %v", err)
		return
	}

	for _, ds := range dataSources {
		if ds.Status != store.StatusConnected {
			continue
		}

		c, err := connector.CreateConnector(ctx, ds, logStore, cfg)
		if err != nil {
			log.Printf("warning: failed to recreate connector %q (%s): %v", ds.Name, ds.Type, err)
			status := store.StatusError
			msg := fmt.Sprintf("failed to reconnect on startup: %v", err)
			dsStore.Update(ctx, ds.ID, store.UpdateDataSourceParams{
				Status: &status, StatusMessage: &msg,
			})
			continue
		}

		if err := c.TestConnection(ctx); err != nil {
			c.Close()
			log.Printf("warning: connector %q (%s) failed reconnect test: %v", ds.Name, ds.Type, err)
			status := store.StatusError
			msg := fmt.Sprintf("failed to reconnect on startup: %v", err)
			dsStore.Update(ctx, ds.ID, store.UpdateDataSourceParams{
				Status: &status, StatusMessage: &msg,
			})
			continue
		}

		registry.Register(c)
		log.Printf("reconnected connector %q (%s)", ds.Name, ds.Type)
	}
}
