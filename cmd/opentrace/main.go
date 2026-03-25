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

	"github.com/adham90/opentrace/internal/backup"
	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/healthcheck"
	"github.com/adham90/opentrace/internal/jobs"
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
	serverStore      store.ServerStore
	metricStore      store.MetricStore
	userStore        store.UserStore
	sessionStore     store.SessionStore
	settingsStore    store.SettingsStore
	mcpActivityStore store.MCPActivityStore
	auditStore store.AuditStore
	watchStore       store.WatchStore
	errorGroupStore    store.ErrorGroupStore
	healthCheckStore   store.HealthCheckStore
	agentNoteStore     store.AgentNoteStore
	trendStore         store.TrendStore
	analyticsStore     store.AnalyticsStore
	journeyStore       store.JourneyStore
	errorImpactStore            store.ErrorImpactStore
	traceStore                  store.TraceStore
	investigationSessionStore    store.InvestigationSessionStore
	toolTransitionStore          store.ToolTransitionStore
	workflowTemplateStore        store.WorkflowTemplateStore
	queryMemoryStore             store.QueryMemoryStore
	runbookEffectivenessStore    store.RunbookEffectivenessStore
	codeEntityStore              store.CodeEntityStore
	deployStore                  store.DeployStore
	eventStore                   store.EventStore
	testCorrelationStore         store.TestCorrelationStore
	registry           *connector.Registry
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
		case "backup":
			err = runBackup()
		case "restore":
			err = runRestore()
		case "version":
			fmt.Println("opentrace " + version.Full())
			return
		case "help", "--help", "-h":
			fmt.Println("Usage: opentrace [command]")
			fmt.Println()
			fmt.Println("Commands:")
			fmt.Println("  (none)    Start the web server")
			fmt.Println("  agent     Run the metrics collection agent")
			fmt.Println("  backup    Create a database backup")
			fmt.Println("  mcp       Start the MCP stdio server")
			fmt.Println("  restore   Restore database from a backup")
			fmt.Println("  seed      Initialize sample data")
			fmt.Println("  version   Print version information")
			fmt.Println()
			fmt.Println("Backup options:")
			fmt.Println("  opentrace backup [-o <path>] [-f]")
			fmt.Println("    -o, --output   Output file (default: opentrace-backup-TIMESTAMP.db)")
			fmt.Println("    -f, --force    Overwrite destination if it exists")
			fmt.Println()
			fmt.Println("Restore options:")
			fmt.Println("  opentrace restore --from <backup-file>")
			fmt.Println("    -f, --from     Backup file to restore from (required)")
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

	// Verify database is responsive before proceeding
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("database health check failed: %w", err)
	}
	slog.Info("database ready")

	// Initialize stores
	dsStore := store.NewDataSourceStore(db)
	logStore := store.NewLogStore(db)
	serverStore := store.NewServerStore(db)
	metricStore := store.NewMetricStore(db)
	userStore := store.NewUserStore(db)
	sessionStore := store.NewSessionStore(db)
	settingsStore := store.NewSettingsStore(db)
	mcpActivityStore := store.NewMCPActivityStore(db)
	auditStore := store.NewAuditStore(db)
	watchStore := store.NewWatchStore(db)
	errorGroupStore := store.NewErrorGroupStore(db)
	healthCheckStore := store.NewHealthCheckStore(db)
	agentNoteStore := store.NewAgentNoteStore(db)
	trendStore := store.NewTrendStore(db)
	analyticsStore := store.NewAnalyticsStore(db)
	journeyStore := store.NewJourneyStore(db)
	errorImpactStore := store.NewErrorImpactStore(db)
	traceStore := store.NewTraceStore(db)
	investigationSessionStore := store.NewInvestigationSessionStore(db)
	toolTransitionStore := store.NewToolTransitionStore(db)
	workflowTemplateStore := store.NewWorkflowTemplateStore(db)
	queryMemoryStore := store.NewQueryMemoryStore(db)
	runbookEffectivenessStore := store.NewRunbookEffectivenessStore(db)
	codeEntityStore := store.NewCodeEntityStore(db)
	deployStore := store.NewDeployStore(db)
	eventStore := store.NewEventStore(db)
	testCorrelationStore := store.NewTestCorrelationStore(db)

	// Initialize registry and reconnect previously-configured connectors
	registry := connector.NewRegistry()
	reconnectConnectors(ctx, dsStore, logStore, registry, cfg, settingsStore)

	return &appDeps{
		db:               db,
		dsStore:          dsStore,
		logStore:         logStore,
		serverStore:      serverStore,
		metricStore:      metricStore,
		userStore:        userStore,
		sessionStore:     sessionStore,
		settingsStore:    settingsStore,
		mcpActivityStore: mcpActivityStore,
		auditStore:       auditStore,
		watchStore:       watchStore,
		errorGroupStore:    errorGroupStore,
		healthCheckStore:   healthCheckStore,
		agentNoteStore:     agentNoteStore,
		trendStore:         trendStore,
		analyticsStore:     analyticsStore,
		journeyStore:       journeyStore,
		errorImpactStore:   errorImpactStore,
		traceStore:                  traceStore,
		investigationSessionStore:    investigationSessionStore,
		toolTransitionStore:          toolTransitionStore,
		workflowTemplateStore:        workflowTemplateStore,
		queryMemoryStore:             queryMemoryStore,
		runbookEffectivenessStore:    runbookEffectivenessStore,
		codeEntityStore:              codeEntityStore,
		deployStore:                  deployStore,
		eventStore:                   eventStore,
		testCorrelationStore:         testCorrelationStore,
		registry:                     registry,
		cfg:                         cfg,
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

	// Resolve MCP name: env override > DB > default
	mcpName := os.Getenv("OPENTRACE_MCP_NAME")
	if mcpName == "" {
		if v, err := deps.settingsStore.GetMCPName(ctx); err == nil && v != "" {
			mcpName = v
		}
	}

	return mcpserver.Serve(mcpserver.Deps{
		Ctx:              ctx,
		Registry:         deps.registry,
		LogStore:         deps.logStore,
		ServerStore:      deps.serverStore,
		MetricStore:      deps.metricStore,
		UserStore:        deps.userStore,
		MCPToken:         os.Getenv("OPENTRACE_MCP_TOKEN"),
		ServerName:       mcpName,
		DataSourceStore:  deps.dsStore,
		SettingsStore:    deps.settingsStore,
		Config:           deps.cfg,
		MCPActivityStore: deps.mcpActivityStore,
		AuditStore:       deps.auditStore,
		WatchStore:       deps.watchStore,
		WatchMetrics:     watchMetrics,
		ErrorGroupStore:    deps.errorGroupStore,
		HealthCheckStore:   deps.healthCheckStore,
		AgentNoteStore:     deps.agentNoteStore,
		TrendStore:         deps.trendStore,
		AnalyticsStore:     deps.analyticsStore,
		JourneyStore:       deps.journeyStore,
		ErrorImpactStore:             deps.errorImpactStore,
		InvestigationSessionStore:    deps.investigationSessionStore,
		ToolTransitionStore:          deps.toolTransitionStore,
		WorkflowTemplateStore:        deps.workflowTemplateStore,
		QueryMemoryStore:             deps.queryMemoryStore,
		RunbookEffectivenessStore:    deps.runbookEffectivenessStore,
		CodeEntityStore:              deps.codeEntityStore,
		DeployStore:                  deps.deployStore,
		EventStore:                   deps.eventStore,
		TestCorrelationStore:         deps.testCorrelationStore,
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
		Ctx:             ctx,
		Registry:        deps.registry,
		LogStore:        deps.logStore,
		ServerStore:     deps.serverStore,
		MetricStore:     deps.metricStore,
		DataSourceStore: deps.dsStore,
		SettingsStore:   deps.settingsStore,
		Config:          deps.cfg,
		AuditStore:      deps.auditStore,
		WatchStore:      deps.watchStore,
		WatchMetrics:    watchMetrics,
		ErrorGroupStore:  deps.errorGroupStore,
		HealthCheckStore: deps.healthCheckStore,
		AgentNoteStore:  deps.agentNoteStore,
		TrendStore:      deps.trendStore,
		AnalyticsStore:  deps.analyticsStore,
		JourneyStore:    deps.journeyStore,
		ErrorImpactStore: deps.errorImpactStore,
		CodeEntityStore:          deps.codeEntityStore,
		DeployStore:              deps.deployStore,
		EventStore:               deps.eventStore,
		TestCorrelationStore:     deps.testCorrelationStore,
	})

	// Agent-first watch evaluator + stream (reactive on log ingestion)
	watchEvaluator := watcher.NewWatchEvaluator(watchMetrics, deps.watchStore)
	watchEvidenceBuilder := watcher.NewWatchEvidenceBuilder(deps.logStore, watchMetrics)
	watchStream := watcher.NewWatchStreamEvaluator(ctx, deps.watchStore, watchEvaluator, watchEvidenceBuilder)

	// Create health check scheduler early so we can inject its reliability data into the web server.
	hcSched := healthcheck.NewScheduler(deps.healthCheckStore, 0)

	// Create server
	srv := web.NewServerWithDeps(web.ServerDeps{
		Ctx:              ctx,
		DB:               deps.db,
		DSStore:          deps.dsStore,
		LogStore:         deps.logStore,
		ServerStore:      deps.serverStore,
		MetricStore:      deps.metricStore,
		UserStore:        deps.userStore,
		SessionStore:     deps.sessionStore,
		SettingsStore:    deps.settingsStore,
		Registry:         deps.registry,
		ToolCatalog:      toolCatalog,
		Cfg:              deps.cfg,
		MCPActivityStore: deps.mcpActivityStore,
		AuditStore:       deps.auditStore,
		WatchStreamEvaluator: watchStream,
		WatchStore:           deps.watchStore,
		WatchMetrics:         watchMetrics,
		ErrorGroupStore:      deps.errorGroupStore,
		HealthCheckStore:     deps.healthCheckStore,
		AgentNoteStore:       deps.agentNoteStore,
		TrendStore:           deps.trendStore,
		AnalyticsStore:       deps.analyticsStore,
		JourneyStore:         deps.journeyStore,
		ErrorImpactStore:     deps.errorImpactStore,
		TraceStore:                deps.traceStore,
		InvestigationSessionStore: deps.investigationSessionStore,
		CodeEntityStore:           deps.codeEntityStore,
		DeployStore:               deps.deployStore,
		EventStore:                deps.eventStore,
		TestCorrelationStore:      deps.testCorrelationStore,
		ReliabilityProvider:       hcSched,
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

	// Start health check scheduler (created above for injection into web server)
	hcSched.Start(ctx)

	// --- Job Queue: persistent, restart-safe background processing ---
	jobQueue := jobs.NewQueue(deps.db)
	jobWorker := jobs.NewWorker(jobQueue)
	jobScheduler := jobs.NewScheduler(jobQueue)

	// Register job handlers
	registerBackgroundJobs(jobWorker, deps)

	// Register recurring schedules
	jobScheduler.Add(jobs.Schedule{Name: "session-cleanup", JobType: "cleanup:sessions", Interval: 15 * time.Minute})
	jobScheduler.Add(jobs.Schedule{Name: "stale-servers", JobType: "cleanup:stale_servers", Interval: 60 * time.Second})
	jobScheduler.Add(jobs.Schedule{Name: "stale-traces", JobType: "cleanup:stale_traces", Interval: 60 * time.Second})
	jobScheduler.Add(jobs.Schedule{Name: "data-retention", JobType: "retention:prune", Interval: 1 * time.Hour})
	jobScheduler.Add(jobs.Schedule{Name: "aggregation", JobType: "aggregate:all", Interval: 5 * time.Minute})

	jobWorker.Start(ctx)
	jobScheduler.Start(ctx)

	// Validate TLS certificate and key files exist if configured
	if deps.cfg.TLSCert != "" && deps.cfg.TLSKey != "" {
		if _, err := os.Stat(deps.cfg.TLSCert); err != nil {
			return fmt.Errorf("TLS certificate file: %w", err)
		}
		if _, err := os.Stat(deps.cfg.TLSKey); err != nil {
			return fmt.Errorf("TLS key file: %w", err)
		}
	}

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

	<-done
	slog.Info("shutting down")

	cancelCtx()
	watchSched.Stop()
	hcSched.Stop()
	jobWorker.Stop()
	jobScheduler.Stop()
	slog.Info("background jobs stopped")

	deps.registry.CloseAll()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("SSE shutdown error", "error", err)
	}

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP shutdown error", "error", err)
	}

	return nil
}

// measureDeployImpacts finds deploys older than 15 minutes that haven't been measured yet,
// computes before/after error rates and durations using the analytics store, and updates
// each deploy with its impact metrics.
func measureDeployImpacts(ctx context.Context, ds store.DeployStore, as store.AnalyticsStore) {
	pending, err := ds.GetPendingMeasurement(ctx, 15*time.Minute)
	if err != nil {
		slog.Warn("failed to get pending deploy measurements", "error", err)
		return
	}
	for _, d := range pending {
		window := 15 * time.Minute

		preSummary, err := as.TrafficSummary(ctx, store.AnalyticsParams{
			Service: d.Service,
			Since:   d.DeployedAt.Add(-window),
			Until:   d.DeployedAt,
		})
		if err != nil {
			slog.Warn("deploy impact: pre-deploy traffic summary failed", "deploy_id", d.ID, "error", err)
			continue
		}

		postSummary, err := as.TrafficSummary(ctx, store.AnalyticsParams{
			Service: d.Service,
			Since:   d.DeployedAt,
			Until:   d.DeployedAt.Add(window),
		})
		if err != nil {
			slog.Warn("deploy impact: post-deploy traffic summary failed", "deploy_id", d.ID, "error", err)
			continue
		}

		impact := store.DeployImpact{
			PreErrorRate:      preSummary.ErrorRate,
			PostErrorRate:     postSummary.ErrorRate,
			PreAvgDurationMs:  preSummary.AvgDurationMs,
			PostAvgDurationMs: postSummary.AvgDurationMs,
		}

		if preSummary.ErrorRate > 0 {
			impact.ErrorRateChangePct = ((postSummary.ErrorRate - preSummary.ErrorRate) / preSummary.ErrorRate) * 100
		}
		if preSummary.AvgDurationMs > 0 {
			impact.DurationChangePct = ((postSummary.AvgDurationMs - preSummary.AvgDurationMs) / preSummary.AvgDurationMs) * 100
		}

		// Mark as incident if error rate increased >50% or response time >2x
		impact.IsIncident = impact.ErrorRateChangePct > 50 || impact.DurationChangePct > 100

		if err := ds.MeasureImpact(ctx, d.ID, impact); err != nil {
			slog.Warn("deploy impact: failed to record measurement", "deploy_id", d.ID, "error", err)
		} else {
			status := "measured"
			if impact.IsIncident {
				status = "incident"
			}
			slog.Info("measured deploy impact", "deploy_id", d.ID, "status", status,
				"error_rate_change_pct", impact.ErrorRateChangePct,
				"duration_change_pct", impact.DurationChangePct)
		}
	}
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
