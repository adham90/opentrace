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
	"github.com/adham90/opentrace/internal/vmagent"
	"github.com/adham90/opentrace/internal/watcher"
	"github.com/adham90/opentrace/internal/web"
)

// appDeps holds shared application dependencies initialized by initApp.
type appDeps struct {
	db           *sql.DB
	dsStore      store.DataSourceStore
	logStore     store.LogStore
	watcherStore store.WatcherStore
	alertStore   store.AlertStore
	serverStore  store.ServerStore
	metricStore  store.MetricStore
	registry     *connector.Registry
	cfg          *config.Config
}

func main() {
	var err error
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "mcp":
			err = runMCP()
		case "agent":
			err = runAgent()
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
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
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

	// Initialize registry and reconnect previously-configured connectors
	registry := connector.NewRegistry()
	reconnectConnectors(ctx, dsStore, logStore, registry, cfg)

	return &appDeps{
		db:           db,
		dsStore:      dsStore,
		logStore:     logStore,
		watcherStore: watcherStore,
		alertStore:   alertStore,
		serverStore:  serverStore,
		metricStore:  metricStore,
		registry:     registry,
		cfg:          cfg,
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

	return mcpserver.Serve(mcpserver.Deps{
		Registry:     deps.registry,
		WatcherStore: deps.watcherStore,
		AlertStore:   deps.alertStore,
		ServerStore:  deps.serverStore,
		MetricStore:  deps.metricStore,
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
	ctx := context.Background()
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

	// Create server
	srv := web.NewServerWithDeps(web.ServerDeps{
		DSStore:       deps.dsStore,
		LogStore:      deps.logStore,
		WatcherStore:  deps.watcherStore,
		RunStore:      runStore,
		AlertStore:    deps.alertStore,
		ServerStore:   deps.serverStore,
		MetricStore:   deps.metricStore,
		Registry:      deps.registry,
		Cfg:           deps.cfg,
		Executor:      executor,
		EventHub:      eventHub,
		ModelRegistry: modelRegistry,
	})

	httpServer := &http.Server{
		Addr:    deps.cfg.ListenAddr,
		Handler: srv.Router,
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

	// Background: mark stale servers offline every 60s
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if n, err := deps.serverStore.MarkStaleOffline(context.Background(), 2*time.Minute); err != nil {
				log.Printf("WARN: MarkStaleOffline: %v", err)
			} else if n > 0 {
				log.Printf("marked %d stale server(s) offline", n)
			}
		}
	}()

	// Background: prune old metrics every hour
	go func() {
		retentionDays := 7
		if v := os.Getenv("OPENTRACE_METRIC_RETENTION_DAYS"); v != "" {
			var d int
			if _, err := fmt.Sscanf(v, "%d", &d); err == nil && d > 0 {
				retentionDays = d
			}
		}
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			retention := time.Duration(retentionDays) * 24 * time.Hour
			if n, err := deps.metricStore.Prune(context.Background(), retention); err != nil {
				log.Printf("WARN: metric prune: %v", err)
			} else if n > 0 {
				log.Printf("pruned %d old metric(s)", n)
			}
		}
	}()

	go func() {
		log.Printf("listening on %s", deps.cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	<-done
	log.Println("shutting down...")

	sched.Stop()
	deps.registry.CloseAll()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return httpServer.Shutdown(shutdownCtx)
}

// reconnectConnectors re-registers connectors that were previously connected.
func reconnectConnectors(ctx context.Context, dsStore store.DataSourceStore, logStore store.LogStore, registry *connector.Registry, cfg *config.Config) {
	dataSources, err := dsStore.List(ctx)
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
