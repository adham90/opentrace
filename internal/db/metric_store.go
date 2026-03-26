package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/pkg/store"
)

// metricStore implements MetricStore using database/sql (SQLite).
type metricStore struct {
	db *sql.DB
	q  *Queries
}

// NewMetricStore creates a new MetricStore backed by SQLite.
func NewMetricStore(db *sql.DB) store.MetricStore {
	return &metricStore{db: db, q: New(db)}
}

// BatchInsert uses a multi-row insert in a transaction — kept hand-written.
func (s *metricStore) BatchInsert(ctx context.Context, serverID uuid.UUID, ts time.Time, samples []store.MetricSample) (int, error) {
	if len(samples) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO metrics (server_id, timestamp, metric_name, metric_value, unit, labels, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	tsStr := ts.UTC().Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)

	for _, sample := range samples {
		labelsJSON := "{}"
		if sample.Labels != nil {
			b, err := json.Marshal(sample.Labels)
			if err != nil {
				return 0, fmt.Errorf("marshaling labels: %w", err)
			}
			labelsJSON = string(b)
		}
		_, err = stmt.ExecContext(ctx,
			serverID.String(), tsStr, sample.Name, sample.Value,
			sample.Unit, labelsJSON, now,
		)
		if err != nil {
			return 0, fmt.Errorf("inserting metric %s: %w", sample.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit batch insert: %w", err)
	}

	return len(samples), nil
}

// Query uses dynamic WHERE clauses — kept hand-written.
func (s *metricStore) Query(ctx context.Context, params store.MetricQuery) ([]store.MetricPoint, error) {
	var conditions []string
	var args []any

	conditions = append(conditions, "server_id = ?")
	args = append(args, params.ServerID.String())

	if params.MetricName != "" {
		conditions = append(conditions, "metric_name = ?")
		args = append(args, params.MetricName)
	}
	if params.Start != nil {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, params.Start.UTC().Format(time.RFC3339))
	}
	if params.End != nil {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, params.End.UTC().Format(time.RFC3339))
	}

	query := `SELECT id, server_id, timestamp, metric_name, metric_value, unit, labels
	          FROM metrics`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY timestamp DESC"

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	query += " LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying metrics: %w", err)
	}
	defer rows.Close()

	return scanMetricRows(rows)
}

func (s *metricStore) LatestByServer(ctx context.Context, serverID uuid.UUID) ([]store.MetricPoint, error) {
	rows, err := s.q.GetLatestMetricsByServer(ctx, serverID.String())
	if err != nil {
		return nil, fmt.Errorf("querying latest metrics: %w", err)
	}
	return toStoreMetricPoints(rows), nil
}

func (s *metricStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		n, err := s.q.PruneMetrics(ctx, cutoff)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning metrics: %w", err)
		}
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}

func scanMetricRows(rows *sql.Rows) ([]store.MetricPoint, error) {
	result := make([]store.MetricPoint, 0)
	for rows.Next() {
		var mp store.MetricPoint
		var tsStr string
		var unit sql.NullString
		var labelsStr string

		if err := rows.Scan(
			&mp.ID, &mp.ServerID, &tsStr, &mp.MetricName,
			&mp.MetricValue, &unit, &labelsStr,
		); err != nil {
			return nil, fmt.Errorf("scanning metric: %w", err)
		}

		mp.Timestamp, _ = time.Parse(time.RFC3339, tsStr)
		if unit.Valid {
			mp.Unit = unit.String
		}
		if labelsStr != "" && labelsStr != "{}" {
			if err := json.Unmarshal([]byte(labelsStr), &mp.Labels); err != nil {
				slog.Warn("invalid labels JSON in metric", "metric", mp.MetricName, "error", err)
				mp.Labels = make(map[string]string)
			}
		}

		result = append(result, mp)
	}
	return result, rows.Err()
}
