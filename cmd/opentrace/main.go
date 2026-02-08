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

	// Initialize embedding provider (may be nil if not configured)
	var embedder llm.EmbeddingProvider
	ep, err := llm.NewEmbeddingProvider(cfg)
	if err != nil {
		log.Printf("warning: embedding provider not available: %v", err)
	} else {
		embedder = ep
	}

	// Initialize registry
	registry := connector.NewRegistry()

	// Create server
	srv := web.NewServer(dsStore, logStore, embStore, registry, cfg, embedder)

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

func defaultMigrationsPath() string {
	// Check for /migrations (Docker)
	if _, err := os.Stat("/migrations"); err == nil {
		return "/migrations"
	}
	// Development: relative to binary
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "migrations")
}
