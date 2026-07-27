package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// RetentionConfig maps table names to TTL strings like "30d", "7d", "never".
// NOTE: Log retention is handled by the segmented log store engine (engine.Store.Prune).
// This config only covers SQLite-resident tables.
type RetentionConfig struct {
	Logs          string `json:"logs"`           // used by engine.Store.Prune, not SQLite
	ErrorGroups   string `json:"error_groups"`
	MetricBuckets string `json:"metric_buckets"`
	DeployMarkers string `json:"deploy_markers"`
}

// DefaultRetentionConfig returns sensible defaults matching the migration seed.
func DefaultRetentionConfig() RetentionConfig {
	return RetentionConfig{
		Logs:          "30d",
		ErrorGroups:   "never",
		MetricBuckets: "180d",
		DeployMarkers: "never",
	}
}

// parseTTL converts "30d" to a time.Duration. Returns 0 for "never".
func parseTTL(ttl string) time.Duration {
	ttl = strings.TrimSpace(strings.ToLower(ttl))
	if ttl == "never" || ttl == "" {
		return 0
	}
	if strings.HasSuffix(ttl, "d") {
		days := 0
		fmt.Sscanf(ttl, "%dd", &days)
		return time.Duration(days) * 24 * time.Hour
	}
	// Try parsing as Go duration
	d, _ := time.ParseDuration(ttl)
	return d
}

// RunRetentionCleanup deletes expired rows from SQLite tables according to the retention config.
// Log retention is handled separately by engine.Store.Prune (called via background_jobs.go).
func RunRetentionCleanup(ctx context.Context, db *sql.DB) error {
	slog.Info("retention cleanup: starting")

	totalDeleted, err := cleanupRetentionTables(ctx, db)
	if err != nil {
		return err
	}

	if totalDeleted > 0 {
		slog.Info("retention cleanup: running VACUUM", "total_deleted", totalDeleted)
		if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
			slog.Warn("retention cleanup: VACUUM failed", "error", err)
		}
	}

	slog.Info("retention cleanup: complete", "total_deleted", totalDeleted)
	return nil
}

// RunJobRetention prunes completed/dead jobs older than jobTTL, cleans the
// SQLite-resident retention tables, and runs a single VACUUM to reclaim disk
// when anything was removed. Without this the jobs table grows unbounded
// (Queue.Prune had no caller) and even when rows were deleted the pages were
// never reclaimed (RunRetentionCleanup, the only VACUUM caller, was dead code).
//
// VACUUM takes an exclusive lock on the database, so this is meant to run on
// the recurring retention schedule — never per-request. Returns the number of
// jobs pruned.
func RunJobRetention(ctx context.Context, db *sql.DB, jobTTL time.Duration) (int64, error) {
	q := NewQueue(db)
	prunedJobs, err := q.Prune(ctx, jobTTL)
	if err != nil {
		return 0, fmt.Errorf("job retention: prune jobs: %w", err)
	}
	if prunedJobs > 0 {
		slog.Info("job retention: pruned old jobs", "count", prunedJobs)
	}

	tableDeleted, err := cleanupRetentionTables(ctx, db)
	if err != nil {
		slog.Warn("job retention: table cleanup failed", "error", err)
	}

	if prunedJobs > 0 || tableDeleted > 0 {
		slog.Info("job retention: running VACUUM", "pruned_jobs", prunedJobs, "table_deleted", tableDeleted)
		if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
			slog.Warn("job retention: VACUUM failed", "error", err)
		}
	}

	return prunedJobs, nil
}

// cleanupRetentionTables deletes expired rows from the SQLite-resident tables
// covered by the retention policy and returns the total number of rows removed.
// It performs the deletes only — the caller decides whether to VACUUM.
func cleanupRetentionTables(ctx context.Context, db *sql.DB) (int, error) {
	// Read config from app_config
	cfg := DefaultRetentionConfig()
	var configJSON string
	err := db.QueryRowContext(ctx, "SELECT value FROM app_config WHERE key = 'retention_policy'").Scan(&configJSON)
	if err == nil && configJSON != "" {
		_ = json.Unmarshal([]byte(configJSON), &cfg)
	}

	// Only SQLite-resident tables with time-based retention
	tables := []struct {
		name    string
		ttl     string
		timeCol string
	}{
		{"metric_buckets", cfg.MetricBuckets, "created_at"},
	}

	totalDeleted := 0
	for _, t := range tables {
		ttl := parseTTL(t.ttl)
		if ttl == 0 {
			continue // "never" -- skip
		}

		cutoff := time.Now().UTC().Add(-ttl).Format(time.RFC3339)
		deleted, err := deleteInBatches(ctx, db, t.name, t.timeCol, cutoff, 1000)
		if err != nil {
			slog.Warn("retention cleanup: error deleting from table", "table", t.name, "error", err)
			continue
		}
		if deleted > 0 {
			slog.Info("retention cleanup: deleted rows", "table", t.name, "count", deleted)
			totalDeleted += deleted
		}
	}

	return totalDeleted, nil
}

// deleteInBatches deletes rows in batches to avoid locking SQLite for too long.
func deleteInBatches(ctx context.Context, db *sql.DB, table, timeCol, cutoff string, batchSize int) (int, error) {
	total := 0
	for {
		query := fmt.Sprintf(
			"DELETE FROM %s WHERE rowid IN (SELECT rowid FROM %s WHERE %s < ? LIMIT ?)",
			table, table, timeCol,
		)
		result, err := db.ExecContext(ctx, query, cutoff, batchSize)
		if err != nil {
			return total, err
		}
		affected, _ := result.RowsAffected()
		total += int(affected)
		if affected < int64(batchSize) {
			break
		}
	}
	return total, nil
}
