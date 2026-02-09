package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/opentrace/opentrace/internal/config"
	"github.com/opentrace/opentrace/internal/connector"
	"github.com/opentrace/opentrace/internal/llm"
	mcpserver "github.com/opentrace/opentrace/internal/mcp"
	"github.com/opentrace/opentrace/internal/store"
	"github.com/opentrace/opentrace/internal/web"
)

// appDeps holds shared application dependencies initialized by initApp.
type appDeps struct {
	pool         *pgxpool.Pool
	dsStore      store.DataSourceStore
	logStore     store.LogStore
	embStore     store.EmbeddingStore
	memoryStore  store.MemoryStore
	watcherStore store.WatcherStore
	alertStore   store.AlertStore
	registry     *connector.Registry
	cfg          *config.Config
	embedder     llm.EmbeddingProvider
}

func main() {
	var err error
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		err = runMCP()
	} else {
		err = run()
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// initApp performs shared initialization: config, migrations, DB connection,
// stores, embedding provider, and connector registry.
func initApp(ctx context.Context) (*appDeps, error) {
	config.LoadEnvFile(".env")

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	// Run migrations
	migrationsPath := defaultMigrationsPath()
	if err := store.RunMigrations(cfg.AppDatabaseURL, migrationsPath); err != nil {
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	log.Println("migrations applied successfully")

	// Connect to database
	pool, err := pgxpool.New(ctx, cfg.AppDatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	log.Println("connected to database")

	// Initialize stores
	dsStore := store.NewPgDataSourceStore(pool)
	logStore := store.NewPgLogStore(pool)
	embStore := store.NewPgEmbeddingStore(pool)
	memoryStore := store.NewPgMemoryStore(pool)
	watcherStore := store.NewPgWatcherStore(pool)
	alertStore := store.NewPgAlertStore(pool)

	// Initialize embedding provider (may be nil if not configured)
	var embedder llm.EmbeddingProvider
	ep, err := llm.NewEmbeddingProvider(cfg)
	if err != nil {
		log.Printf("warning: embedding provider not available: %v", err)
	} else {
		embedder = ep
	}

	// Initialize registry and reconnect previously-configured connectors
	registry := connector.NewRegistry()
	reconnectConnectors(ctx, dsStore, logStore, embStore, registry, cfg, embedder)

	// Auto-register system connector (memory tools)
	registry.Register(connector.NewSystemConnector(memoryStore))

	return &appDeps{
		pool:         pool,
		dsStore:      dsStore,
		logStore:     logStore,
		embStore:     embStore,
		memoryStore:  memoryStore,
		watcherStore: watcherStore,
		alertStore:   alertStore,
		registry:     registry,
		cfg:          cfg,
		embedder:     embedder,
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
	defer deps.pool.Close()
	defer deps.registry.CloseAll()

	return mcpserver.Serve(mcpserver.Deps{
		Registry:     deps.registry,
		WatcherStore: deps.watcherStore,
		AlertStore:   deps.alertStore,
	})
}

func run() error {
	ctx := context.Background()
	deps, err := initApp(ctx)
	if err != nil {
		return err
	}
	defer deps.pool.Close()

	// Create server
	srv := web.NewServerWithDeps(web.ServerDeps{
		DSStore:      deps.dsStore,
		LogStore:     deps.logStore,
		EmbStore:     deps.embStore,
		WatcherStore: deps.watcherStore,
		AlertStore:   deps.alertStore,
		Registry:     deps.registry,
		Cfg:          deps.cfg,
		Embedder:     deps.embedder,
	})

	httpServer := &http.Server{
		Addr:    deps.cfg.ListenAddr,
		Handler: srv.Router,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("listening on %s", deps.cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	<-done
	log.Println("shutting down...")

	deps.registry.CloseAll()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return httpServer.Shutdown(shutdownCtx)
}

// reconnectConnectors re-registers connectors that were previously connected.
func reconnectConnectors(ctx context.Context, dsStore store.DataSourceStore, logStore store.LogStore, embStore store.EmbeddingStore, registry *connector.Registry, cfg *config.Config, embedder llm.EmbeddingProvider) {
	dataSources, err := dsStore.List(ctx)
	if err != nil {
		log.Printf("warning: failed to list connectors for reconnect: %v", err)
		return
	}

	for _, ds := range dataSources {
		if ds.Status != store.StatusConnected {
			continue
		}

		c, err := connector.CreateConnector(ctx, ds, logStore, embStore, embedder, cfg)
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

func defaultMigrationsPath() string {
	// Check for /migrations (Docker)
	if _, err := os.Stat("/migrations"); err == nil {
		return "/migrations"
	}
	// Development: relative to binary
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "migrations")
}
