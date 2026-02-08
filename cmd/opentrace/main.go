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
	"github.com/opentrace/opentrace/internal/store"
	"github.com/opentrace/opentrace/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	config.LoadEnvFile(".env")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Run migrations
	migrationsPath := defaultMigrationsPath()
	if err := store.RunMigrations(cfg.AppDatabaseURL, migrationsPath); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	log.Println("migrations applied successfully")

	// Connect to database
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.AppDatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("pinging database: %w", err)
	}
	log.Println("connected to database")

	// Initialize stores
	dsStore := store.NewPgDataSourceStore(pool)
	logStore := store.NewPgLogStore(pool)
	embStore := store.NewPgEmbeddingStore(pool)
	chatStore := store.NewPgChatStore(pool)

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

	// Create server
	srv := web.NewServer(dsStore, logStore, embStore, chatStore, registry, cfg, embedder)

	httpServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv.Router,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("listening on %s", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	<-done
	log.Println("shutting down...")

	registry.CloseAll()

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
