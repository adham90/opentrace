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
	auditStore       store.AuditStore
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

	// Apply LLM settings from database (overrides env-var defaults)
	applyLLMOverrides(ctx, deps.settingsStore, deps.cfg)

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
		AlertGroupStore:  deps.alertGroupStore,
		EventHub:         eventHub,
		AuditStore:       deps.auditStore,
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

	// Apply LLM settings from database (overrides env-var defaults)
	applyLLMOverrides(ctx, deps.settingsStore, deps.cfg)

	// Create LLM provider cache for per-watcher model selection
	defaultLLM, err := llm.NewLLMProvider(deps.cfg)
	if err != nil {
		slog.Warn("default LLM provider unavailable", "error", err)
	}
	providerCache := llm.NewProviderCache(deps.cfg, defaultLLM)
	modelRegistry := llm.NewModelRegistry(deps.cfg)

	// Create watcher run store and clean up stale runs from previous crashes
	runStore := store.NewWatcherRunStore(deps.db)
	if n, err := runStore.FailStaleRuns(ctx, 10*time.Minute); err != nil {
		slog.Warn("failed to clean stale runs", "error", err)
	} else if n > 0 {
		slog.Info("cleaned up stale watcher runs", "count", n)
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
		DataSourceStore: deps.dsStore,
		SettingsStore:   deps.settingsStore,
		RuleEvaluator:   ruleEvaluator,
		Config:          deps.cfg,
		AlertGroupStore: deps.alertGroupStore,
		AuditStore:      deps.auditStore,
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
		ProviderCache:    providerCache,
		RuleEvaluator:    ruleEvaluator,
		MCPActivityStore: deps.mcpActivityStore,
		AlertGroupStore:  deps.alertGroupStore,
		AuditStore:       deps.auditStore,
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

	// Create evaluators for typed watchers
	deadmanEval := watcher.NewDeadmanEvaluator(deps.registry, deps.logStore)
	diffEval := watcher.NewDiffEvaluator(deps.registry, runStore)
	compositeEval := watcher.NewCompositeEvaluator(deps.watcherStore, runStore, ruleEvaluator)
	trendEval := watcher.NewTrendEvaluator(deps.registry, deps.logStore, runStore)
	sequenceEval := watcher.NewSequenceEvaluator(deps.logStore, deps.alertStore)

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
		EventHub:           eventHub,
		RuleEvaluator:      ruleEvaluator,
		DeadmanEvaluator:   deadmanEval,
		DiffEvaluator:      diffEval,
		CompositeEvaluator: compositeEval,
		TrendEvaluator:     trendEval,
		SequenceEvaluator:  sequenceEval,
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
				slog.Warn("reading retention settings failed", "error", err)
				continue
			}

			globalDays := settings.RetentionDays

			// Prune logs, watcher runs, alerts (skip if 0 = keep forever)
			if globalDays > 0 {
				retention := time.Duration(globalDays) * 24 * time.Hour
				if n, err := deps.logStore.Prune(ctx, retention); err != nil {
					slog.Warn("log prune failed", "error", err)
				} else if n > 0 {
					slog.Info("pruned old logs", "count", n)
				}
				if n, err := runStore.Prune(ctx, retention); err != nil {
					slog.Warn("watcher run prune failed", "error", err)
				} else if n > 0 {
					slog.Info("pruned old watcher runs", "count", n)
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

			// Prune metrics: use env var override if set, else global setting
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

	// Cancel the root context to signal all background goroutines to stop
	cancelCtx()

	sched.Stop()
	deps.registry.CloseAll()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Shutdown SSE sessions before the HTTP server.
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

// applyLLMOverrides reads LLM settings from the database and applies them
// over the environment-variable defaults in cfg.
func applyLLMOverrides(ctx context.Context, ss store.SettingsStore, cfg *config.Config) {
	if ss == nil {
		return
	}
	settings, err := ss.GetLLMSettings(ctx)
	if err != nil {
		slog.Warn("failed to load LLM settings from database", "error", err)
		return
	}
	if settings == nil {
		return
	}
	cfg.ApplyLLMOverrides(config.LLMOverrides{
		DefaultProvider: settings.DefaultProvider,
		AnthropicAPIKey: settings.AnthropicAPIKey,
		AnthropicURL:    settings.AnthropicURL,
		OpenAIAPIKey:    settings.OpenAIAPIKey,
		OpenAIURL:       settings.OpenAIURL,
		GeminiAPIKey:    settings.GeminiAPIKey,
		GeminiURL:       settings.GeminiURL,
		OllamaURL:       settings.OllamaURL,
		OllamaModel:     settings.OllamaModel,
	})
	slog.Info("applied LLM settings from database", "provider", cfg.LLMProvider)
}
