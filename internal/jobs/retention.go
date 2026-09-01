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
	Logs        string `json:"logs"` // used by engine.Store.Prune, not SQLite
	ErrorGroups string `json:"error_groups"`
	WatchRuns   string `json:"watch_runs"`
	WatchAlerts string `json:"watch_alerts"`
	// Traces bounds trace_status, which holds one row per trace id. A busy
	// service emits roughly one trace per request, so "never" here is an
	// unbounded table: a stress run put 740k rows in it in five minutes, and
	// the periodic stale-trace sweep then had to update all of them at once.
	Traces string `json:"traces"`
}

// DefaultRetentionConfig returns sensible defaults matching the migration seed.
// watch_runs/watch_alerts/traces default to a bounded TTL because each is
// written per event rather than per entity — one run row per watch per check
// interval, one trace row per request — so "never" would grow the metadata
// database without limit.
func DefaultRetentionConfig() RetentionConfig {
	return RetentionConfig{
		Logs:        "30d",
		ErrorGroups: "never",
		WatchRuns:   defaultWatchRetention,
		WatchAlerts: defaultWatchRetention,
		Traces:      defaultWatchRetention,
	}
}

// defaultWatchRetention bounds watch_runs/watch_alerts growth when the operator
// has not configured an explicit TTL.
const defaultWatchRetention = "30d"

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

// RunJobRetention prunes completed/dead jobs older than jobTTL, cleans the
// SQLite-resident retention tables, and runs a single VACUUM to reclaim disk
// when anything was removed. Without this the jobs table grows unbounded
// (Queue.Prune had no caller) and even when rows were deleted the pages were
// never reclaimed.
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

	// Only SQLite-resident tables with time-based retention. watch_alerts is
	// pruned before watch_runs so an alert is never orphaned from a run that is
	// about to be deleted (the FK is ON DELETE SET NULL, not CASCADE).
	tables := []struct {
		name    string
		ttl     string
		timeCol string
	}{
		{"error_groups", cfg.ErrorGroups, "last_seen_at"},
		{"watch_alerts", cfg.WatchAlerts, "created_at"},
		{"watch_runs", cfg.WatchRuns, "started_at"},
		{"trace_status", cfg.Traces, "last_updated_at"},
	}

	totalDeleted := 0
	for _, t := range tables {
		ttl := parseTTL(t.ttl)
		if ttl == 0 {
			continue // "never" -- skip
		}
		if !tableExists(ctx, db, t.name) {
			continue
		}

		cutoff := time.Now().UTC().Add(-ttl).Format(time.RFC3339)
		deleted, err := deleteInBatches(ctx, db, t.name, t.timeCol, cutoff, retentionBatchSize)
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

// retentionBatchSize is how many rows a single DELETE statement removes, so
// SQLite is never locked for the length of a whole table scan.
const retentionBatchSize = 1000

// tableExists reports whether a table is present, so retention stays a no-op on
// databases that predate a table rather than logging an error every cycle.
func tableExists(ctx context.Context, db *sql.DB, table string) bool {
	var name string
	err := db.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
	return err == nil
}

// deleteInBatches deletes rows in batches to avoid locking SQLite for too long.
//
// Timestamp columns in this schema are TEXT and are not written in a single
// format: most writers use RFC3339 ("2006-01-02T15:04:05Z") but bun-managed
// models emit "2006-01-02 15:04:05.999999-07:00". A plain lexicographic
// comparison between the two is wrong (' ' < 'T', and the offset shifts the
// instant), so both sides are normalised through SQLite's datetime(), which
// parses either form and yields a comparable UTC value.
func deleteInBatches(ctx context.Context, db *sql.DB, table, timeCol, cutoff string, batchSize int) (int, error) {
	total := 0
	for {
		query := fmt.Sprintf(
			"DELETE FROM %s WHERE rowid IN "+
				"(SELECT rowid FROM %s WHERE datetime(%s) < datetime(?) LIMIT ?)",
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
