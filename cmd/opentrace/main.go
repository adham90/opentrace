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

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	mcpserver "github.com/adham90/opentrace/internal/mcp"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/web"
)

// appDeps holds shared application dependencies initialized by initApp.
type appDeps struct {
	db           *sql.DB
	dsStore      store.DataSourceStore
	logStore     store.LogStore
	watcherStore store.WatcherStore
	alertStore   store.AlertStore
	registry     *connector.Registry
	cfg          *config.Config
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

	// Initialize registry and reconnect previously-configured connectors
	registry := connector.NewRegistry()
	reconnectConnectors(ctx, dsStore, logStore, registry, cfg)

	return &appDeps{
		db:           db,
		dsStore:      dsStore,
		logStore:     logStore,
		watcherStore: watcherStore,
		alertStore:   alertStore,
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
	})
}

func run() error {
	ctx := context.Background()
	deps, err := initApp(ctx)
	if err != nil {
		return err
	}
	defer deps.db.Close()

	// Create server
	srv := web.NewServerWithDeps(web.ServerDeps{
		DSStore:      deps.dsStore,
		LogStore:     deps.logStore,
		WatcherStore: deps.watcherStore,
		AlertStore:   deps.alertStore,
		Registry:     deps.registry,
		Cfg:          deps.cfg,
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
