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

// RetentionConfig maps table names to TTL strings like "30d", "7d", "never"
type RetentionConfig struct {
	Logs             string `json:"logs"`
	RequestCaptures  string `json:"request_captures"`
	SQLCaptures      string `json:"sql_captures"`
	HTTPCaptures     string `json:"http_captures"`
	EmailCaptures    string `json:"email_captures"`
	AuditCaptures    string `json:"audit_captures"`
	RequestSummaries string `json:"request_summaries"`
	ErrorGroups      string `json:"error_groups"`
	MetricBuckets    string `json:"metric_buckets"`
	DeployMarkers    string `json:"deploy_markers"`
}

// DefaultRetentionConfig returns sensible defaults matching the migration seed.
func DefaultRetentionConfig() RetentionConfig {
	return RetentionConfig{
		Logs:             "30d",
		RequestCaptures:  "7d",
		SQLCaptures:      "14d",
		HTTPCaptures:     "7d",
		EmailCaptures:    "14d",
		AuditCaptures:    "365d",
		RequestSummaries: "90d",
		ErrorGroups:      "never",
		MetricBuckets:    "180d",
		DeployMarkers:    "never",
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

// RunRetentionCleanup deletes expired rows from all tables according to the retention config.
func RunRetentionCleanup(ctx context.Context, db *sql.DB) error {
	slog.Info("retention cleanup: starting")

	// Read config from app_config
	cfg := DefaultRetentionConfig()
	var configJSON string
	err := db.QueryRowContext(ctx, "SELECT value FROM app_config WHERE key = 'retention_policy'").Scan(&configJSON)
	if err == nil && configJSON != "" {
		_ = json.Unmarshal([]byte(configJSON), &cfg)
	}

	// Define table -> TTL -> time-column mapping
	tables := []struct {
		name    string
		ttl     string
		timeCol string
	}{
		{"request_captures", cfg.RequestCaptures, "created_at"},
		{"sql_captures", cfg.SQLCaptures, "created_at"},
		{"http_captures", cfg.HTTPCaptures, "created_at"},
		{"email_captures", cfg.EmailCaptures, "created_at"},
		{"audit_captures", cfg.AuditCaptures, "created_at"},
		{"file_captures", cfg.RequestCaptures, "created_at"}, // same TTL as request_captures
		{"request_summaries", cfg.RequestSummaries, "created_at"},
		{"metric_buckets", cfg.MetricBuckets, "created_at"},
		{"logs", cfg.Logs, "timestamp"},
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

	// Run VACUUM only if we deleted something
	if totalDeleted > 0 {
		slog.Info("retention cleanup: running VACUUM", "total_deleted", totalDeleted)
		_, err := db.ExecContext(ctx, "VACUUM")
		if err != nil {
			slog.Warn("retention cleanup: VACUUM failed", "error", err)
		}
	}

	slog.Info("retention cleanup: complete", "total_deleted", totalDeleted)
	return nil
}

// deleteInBatches deletes rows in batches to avoid locking SQLite for too long.
func deleteInBatches(ctx context.Context, db *sql.DB, table, timeCol, cutoff string, batchSize int) (int, error) {
	total := 0
	for {
		// Use a subquery to limit the batch size
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
			break // No more rows to delete
		}
	}
	return total, nil
}
