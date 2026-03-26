package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/adham90/opentrace/internal/jobs"
	"github.com/adham90/opentrace/pkg/server"
	"golang.org/x/sync/errgroup"
)

// registerBackgroundJobs registers all background job handlers with the worker.
func registerBackgroundJobs(w *jobs.Worker, deps *server.Deps) {
	w.Register("cleanup:sessions", func(ctx context.Context, _ json.RawMessage) error {
		n, err := deps.SessionStore.DeleteExpired(ctx)
		if err != nil {
			return fmt.Errorf("session cleanup: %w", err)
		}
		if n > 0 {
			slog.Info("cleaned expired sessions", "count", n)
		}
		return nil
	})

	w.Register("cleanup:stale_servers", func(ctx context.Context, _ json.RawMessage) error {
		n, err := deps.ServerStore.MarkStaleOffline(ctx, 2*time.Minute)
		if err != nil {
			return fmt.Errorf("stale server cleanup: %w", err)
		}
		if n > 0 {
			slog.Info("marked stale servers offline", "count", n)
		}
		return nil
	})

	w.Register("cleanup:stale_traces", func(ctx context.Context, _ json.RawMessage) error {
		if deps.TraceStore == nil {
			return nil
		}
		n, err := deps.TraceStore.MarkStaleTraces(ctx, 30*time.Second)
		if err != nil {
			return fmt.Errorf("stale trace cleanup: %w", err)
		}
		if n > 0 {
			slog.Info("marked stale traces as timeout", "count", n)
		}
		return nil
	})

	w.Register("retention:prune", retentionPruneHandler(deps))

	w.Register("aggregate:all", aggregationHandler(deps))
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
			pruneStore(ctx, "deploys", deps.DeployStore, retention)
			pruneStore(ctx, "events", deps.EventStore, retention)
			pruneStore(ctx, "uncovered paths", deps.TestCorrelationStore, retention)

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
		since := time.Now().UTC().Add(-10 * time.Minute)

		g, gctx := errgroup.WithContext(ctx)
		if deps.TrendStore != nil {
			g.Go(func() error {
				if err := deps.TrendStore.AggregateBuckets(gctx, "1h", since); err != nil {
					slog.Warn("trend aggregation failed", "error", err)
				}
				return nil
			})
		}
		if deps.AnalyticsStore != nil {
			g.Go(func() error {
				if err := deps.AnalyticsStore.AggregateEndpointStats(gctx, "1h", since); err != nil {
					slog.Warn("endpoint stats aggregation failed", "error", err)
				}
				return nil
			})
			g.Go(func() error {
				if err := deps.AnalyticsStore.UpdateTrafficHeatmap(gctx, since); err != nil {
					slog.Warn("traffic heatmap update failed", "error", err)
				}
				return nil
			})
		}
		if deps.JourneyStore != nil {
			g.Go(func() error {
				if err := deps.JourneyStore.BuildSessions(gctx, since); err != nil {
					slog.Warn("session building failed", "error", err)
				}
				return nil
			})
		}
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
		if deps.DeployStore != nil && deps.AnalyticsStore != nil {
			g.Go(func() error {
				measureDeployImpacts(gctx, deps.DeployStore, deps.AnalyticsStore)
				return nil
			})
		}
		if deps.TestCorrelationStore != nil {
			g.Go(func() error {
				if err := deps.TestCorrelationStore.RefreshUncoveredPaths(gctx); err != nil {
					slog.Warn("test correlation refresh failed", "error", err)
				}
				return nil
			})
		}
		return g.Wait()
	}
}
