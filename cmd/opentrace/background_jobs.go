package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/jobs"
	"github.com/adham90/opentrace/internal/mcp/tools"
	"github.com/adham90/opentrace/internal/watcher"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"golang.org/x/sync/errgroup"
)

// registerBackgroundJobs registers all background job handlers with the worker.
func registerBackgroundJobs(w *jobs.Worker, deps *server.Deps) {
	w.Register("cleanup:sessions", handleSessionCleanup(deps.SessionStore))
	w.Register("cleanup:stale_servers", handleStaleServerCleanup(deps.ServerStore))
	w.Register("cleanup:stale_traces", handleStaleTraceCleanup(deps.TraceStore))
	w.Register("retention:prune", retentionPruneHandler(deps))
	w.Register("aggregate:all", aggregationHandler(deps))
}

// handleSessionCleanup returns a job handler that deletes expired browser sessions.
func handleSessionCleanup(sessionStore store.SessionStore) jobs.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) error {
		n, err := sessionStore.DeleteExpired(ctx)
		if err != nil {
			return fmt.Errorf("session cleanup: %w", err)
		}
		if n > 0 {
			slog.Info("cleaned expired sessions", "count", n)
		}
		return nil
	}
}

// handleStaleServerCleanup returns a job handler that marks servers offline
// when they haven't sent a heartbeat within the threshold.
func handleStaleServerCleanup(serverStore store.ServerStore) jobs.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) error {
		n, err := serverStore.MarkStaleOffline(ctx, 2*time.Minute)
		if err != nil {
			return fmt.Errorf("stale server cleanup: %w", err)
		}
		if n > 0 {
			slog.Info("marked stale servers offline", "count", n)
		}
		return nil
	}
}

// handleStaleTraceCleanup returns a job handler that marks incomplete traces
// as timed out when no new spans arrive within the threshold.
func handleStaleTraceCleanup(traceStore store.TraceStore) jobs.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) error {
		if traceStore == nil {
			return nil
		}
		n, err := traceStore.MarkStaleTraces(ctx, 30*time.Second)
		if err != nil {
			return fmt.Errorf("stale trace cleanup: %w", err)
		}
		if n > 0 {
			slog.Info("marked stale traces as timeout", "count", n)
		}
		return nil
	}
}

// retentionPruneHandler returns a job handler that prunes all stores according
// to the configured retention settings.
func retentionPruneHandler(deps *server.Deps) jobs.HandlerFunc {
	// Env var override for metric retention (checked once at startup).
	envMetricRetentionDays := 0
	if v := os.Getenv("OPENTRACE_METRIC_RETENTION_DAYS"); v != "" {
		var d int
		if _, err := fmt.Sscanf(v, "%d", &d); err == nil && d > 0 {
			envMetricRetentionDays = d
		}
	}

	return func(ctx context.Context, _ json.RawMessage) error {
		settings, err := deps.SettingsStore.GetRetention(ctx)
		if err != nil {
			return fmt.Errorf("reading retention settings: %w", err)
		}

		globalDays := settings.RetentionDays
		if globalDays > 0 {
			retention := time.Duration(globalDays) * 24 * time.Hour
			pruneStore(ctx, "logs", deps.LogStore, retention)
			pruneStore(ctx, "mcp activity", deps.MCPActivityStore, retention)
			pruneStore(ctx, "audit log", deps.AuditStore, retention)
			pruneStore(ctx, "error groups", deps.ErrorGroupStore, retention)
			pruneStore(ctx, "agent notes", deps.AgentNoteStore, retention)
			pruneStore(ctx, "code entities", deps.CodeEntityStore, retention)
			if deps.HealthCheckStore != nil {
				if n, err := deps.HealthCheckStore.PruneResults(ctx, retention); err != nil {
					slog.Warn("healthcheck results prune failed", "error", err)
				} else if n > 0 {
					slog.Info("pruned old healthcheck results", "count", n)
				}
			}
		}

		// Metric retention: env override > DB setting > global
		metricDays := envMetricRetentionDays
		if metricDays == 0 {
			metricDays = settings.MetricRetentionDays
		}
		if metricDays == 0 {
			metricDays = globalDays
		}
		if metricDays > 0 {
			retention := time.Duration(metricDays) * 24 * time.Hour
			if n, err := deps.MetricStore.Prune(ctx, retention); err != nil {
				slog.Warn("metric prune failed", "error", err)
			} else if n > 0 {
				slog.Info("pruned old metrics", "count", n)
			}
		}

		return nil
	}
}

// prunable is the common Prune interface shared by many stores.
type prunable interface {
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// pruneStore calls Prune on a store if it is non-nil.
func pruneStore(ctx context.Context, name string, s prunable, retention time.Duration) {
	if s == nil {
		return
	}
	n, err := s.Prune(ctx, retention)
	if err != nil {
		slog.Warn(name+" prune failed", "error", err)
	} else if n > 0 {
		slog.Info("pruned old "+name, "count", n)
	}
}

// aggregationHandler returns a job handler that runs all aggregation tasks
// concurrently via errgroup.
func aggregationHandler(deps *server.Deps) jobs.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) error {
		g, gctx := errgroup.WithContext(ctx)
		if deps.ErrorImpactStore != nil {
			g.Go(func() error {
				if err := deps.ErrorImpactStore.ComputeImpactScores(gctx); err != nil {
					slog.Warn("impact score computation failed", "error", err)
				}
				return nil
			})
		}
		if deps.CodeEntityStore != nil {
			g.Go(func() error {
				if err := deps.CodeEntityStore.BatchRecomputeRisk(gctx); err != nil {
					slog.Warn("code entity risk recomputation failed", "error", err)
				}
				return nil
			})
		}
		return g.Wait()
	}
}

// ---------------------------------------------------------------------------
// Proactive reporting: post-deploy check-ins and the scheduled catch-up brief.
// ---------------------------------------------------------------------------

const (
	// deployReportJob delivers the 1h/24h check-in the auto-watcher schedules
	// when it first sees a commit.
	deployReportJob = "deploy:report"

	// catchupPushJob delivers overview.catchup to chat on a timer, so the
	// morning brief arrives whether or not anyone opened an agent.
	catchupPushJob = "catchup:push"

	// catchupPushIntervalEnv overrides the brief's cadence. "off" or "0"
	// disables it.
	catchupPushIntervalEnv = "OPENTRACE_CATCHUP_PUSH_INTERVAL"

	// defaultCatchupPushInterval is one brief a day.
	defaultCatchupPushInterval = 24 * time.Hour

	// maxBriefItems caps the chat message. A brief nobody reads to the end is
	// not a brief; the full list stays one overview.catchup call away.
	maxBriefItems = 10
)

// catchupPushInterval resolves the brief's cadence from the environment.
// A zero return disables the schedule entirely.
func catchupPushInterval() time.Duration {
	v := os.Getenv(catchupPushIntervalEnv)
	switch v {
	case "":
		return defaultCatchupPushInterval
	case "off", "0":
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		slog.Warn("invalid catch-up push interval, using default", catchupPushIntervalEnv, v)
		return defaultCatchupPushInterval
	}
	return d
}

// deployReportHandler delivers one scheduled post-deploy check-in.
func deployReportHandler(auto *watcher.AutoWatcher, senders []messageSender) jobs.HandlerFunc {
	return func(ctx context.Context, payload json.RawMessage) error {
		var r watcher.DeployReport
		if err := json.Unmarshal(payload, &r); err != nil {
			return fmt.Errorf("decoding deploy report: %w", err)
		}
		text, err := auto.Report(ctx, r)
		if err != nil {
			return fmt.Errorf("building deploy report: %w", err)
		}
		if text == "" {
			// The watch is gone — deleted, or aged out with its data. Nothing
			// to say and nothing to retry.
			return nil
		}
		slog.Info("post-deploy report", "commit", r.Commit, "after", r.After, "service", r.Service)
		return broadcast(ctx, senders, "📦 "+text)
	}
}

// catchupPushHandler delivers the same payload overview.catchup returns, on a
// timer instead of on an agent's request.
//
// The window is the interval, anchored on the previous successful push. The
// jobs table is the cursor: no new store, and it is already the record of when
// this ran.
func catchupPushHandler(deps *server.Deps, senders []messageSender, interval time.Duration) jobs.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) error {
		last := lastCompletedJobAt(ctx, deps.DB, catchupPushJob)

		// The scheduler fires once at startup so a frequently-restarted process
		// still reaches its schedules. For a daily brief that would mean a
		// brief per deploy, so a recent one suppresses this run.
		if !last.IsZero() && time.Since(last) < interval/2 {
			return nil
		}

		since := time.Now().UTC().Add(-interval)
		if last.After(since) {
			since = last
		}

		items, truncated := tools.CollectCatchup(ctx, tools.OverviewDeps{
			LogStore:        deps.LogStore,
			ErrorGroupStore: deps.ErrorGroupStore,
			WatchStore:      deps.WatchStore,
			DeployStore:     deps.DeployStore,
			CriticalPaths:   criticalPathsFor(deps.Cfg),
		}, "", since)
		if len(items) == 0 {
			// Silence is the correct output for a quiet night. A daily "nothing
			// happened" is how a channel gets muted.
			return nil
		}
		return broadcast(ctx, senders, formatCatchupBrief(items, since, truncated))
	}
}

func criticalPathsFor(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	return cfg.CriticalPaths
}

// formatCatchupBrief renders catch-up items as a chat message.
func formatCatchupBrief(items []tools.TriageEntry, since time.Time, truncated bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "☀️ <b>Since %s</b> — %d event(s)\n", since.Format("Jan 2 15:04 MST"), len(items))

	shown := items
	if len(shown) > maxBriefItems {
		shown = shown[:maxBriefItems]
	}
	for _, it := range shown {
		marker := "•"
		if it.Critical {
			marker = "💰"
		} else if it.Severity == "critical" {
			marker = "🔴"
		}
		fmt.Fprintf(&b, "\n%s %s — %s", marker, it.Title, it.Detail)
	}
	if len(items) > len(shown) || truncated {
		fmt.Fprintf(&b, "\n\n… and more. Ask your agent for overview.catchup for the full list.")
	}
	return b.String()
}

// lastCompletedJobAt returns when a job type last completed, or the zero time
// when it never has (or the row has since been pruned).
func lastCompletedJobAt(ctx context.Context, db *sql.DB, jobType string) time.Time {
	var raw sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT MAX(completed_at) FROM jobs WHERE job_type = ? AND status = 'completed'`,
		jobType,
	).Scan(&raw)
	if err != nil || !raw.Valid {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw.String)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
