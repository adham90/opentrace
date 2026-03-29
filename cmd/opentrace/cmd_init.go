package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adham90/opentrace/internal/auth"
	"github.com/adham90/opentrace/internal/config"
	dbstore "github.com/adham90/opentrace/internal/adapter/sqlite"
	"github.com/adham90/opentrace/internal/version"
)

// runInit initializes the OpenTrace database and generates an API key.
// No user accounts are created — the first `npx opentrace connect` creates the admin.
func runInit() error {
	config.LoadEnvFile(".env")

	fmt.Printf("OpenTrace %s — Observability for AI coding agents\n\n", version.Full())

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	dataDir := cfg.DataDir

	// Allow override from args: opentrace init /custom/path
	if len(os.Args) > 2 {
		dataDir = os.Args[2]
	}

	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("creating data directory %q: %w", dataDir, err)
	}

	dbPath := filepath.Join(dataDir, "opentrace.db")

	// Check if database already exists
	if _, err := os.Stat(dbPath); err == nil {
		fmt.Printf("Database already exists at %s\n", dbPath)
		fmt.Println("To reinitialize, delete the database file first.")
		return nil
	}

	fmt.Printf("Data directory: %s\n", dataDir)

	// Open and migrate database
	fmt.Print("Creating database... ")
	bunDB, err := dbstore.OpenSQLite(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer bunDB.Close()

	fmt.Println("done")

	fmt.Print("Running migrations... ")
	if err := dbstore.RunSQLiteMigrations(bunDB); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	fmt.Println("done")

	// Generate API key for SDK ingestion
	apiKey, err := auth.GenerateAPIKey()
	if err != nil {
		return fmt.Errorf("generating API key: %w", err)
	}

	// Store API key in settings
	stores := dbstore.NewStores(bunDB)
	ctx := context.Background()
	if err := stores.SettingsStore.SetAPIKey(ctx, apiKey); err != nil {
		return fmt.Errorf("storing API key: %w", err)
	}

	fmt.Println()
	fmt.Println("✓ OpenTrace initialized.")
	fmt.Println()
	fmt.Printf("API Key (for SDK log ingestion): %s\n", apiKey)
	fmt.Println("Save this key — you'll need it when setting up your app's SDK.")
	fmt.Println()
	fmt.Println("Start the server:")
	fmt.Println("  opentrace serve")
	fmt.Println()
	fmt.Println("Then connect from your project directory:")
	fmt.Println("  curl -s https://YOUR_SERVER_IP:8080/connect | bash")
	fmt.Println("  (first connection creates your admin account)")
	fmt.Println("  (no client install needed — just curl)")

	return nil
}
