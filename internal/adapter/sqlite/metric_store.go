package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

// metricStore implements MetricStore using bun (SQLite).
type metricStore struct {
	db *bun.DB
}

// NewMetricStore creates a new MetricStore backed by SQLite.
func NewMetricStore(db *bun.DB) store.MetricStore {
	return &metricStore{db: db}
}

// BatchInsert inserts multiple metric samples in a single transaction using bun batch insert.
func (s *metricStore) BatchInsert(ctx context.Context, serverID uuid.UUID, ts time.Time, samples []store.MetricSample) (int, error) {
	if len(samples) == 0 {
		return 0, nil
	}

	now := time.Now().UTC()
	tsUTC := ts.UTC()

	// Use a transaction with prepared statements for best performance with SQLite.
	if err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO metrics (server_id, timestamp, metric_name, metric_value, unit, labels, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare insert: %w", err)
		}
		defer stmt.Close()

		tsStr := rfc3339(tsUTC)
		nowStr := rfc3339(now)

		for _, sample := range samples {
			labelsJSON := "{}"
			if sample.Labels != nil {
				labelsJSON = marshalMetadataJSON(toAnyMap(sample.Labels))
			}
			_, err = stmt.ExecContext(ctx,
				serverID.String(), tsStr, sample.Name, sample.Value,
				sample.Unit, labelsJSON, nowStr,
			)
			if err != nil {
				return fmt.Errorf("inserting metric %s: %w", sample.Name, err)
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}

	return len(samples), nil
}

// Query uses dynamic WHERE clauses with raw SQL for graceful label handling.
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

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)

	query := fmt.Sprintf(
		`SELECT id, server_id, timestamp, metric_name, metric_value, unit, labels
		 FROM metrics WHERE %s ORDER BY timestamp DESC LIMIT ?`,
		strings.Join(conditions, " AND "),
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying metrics: %w", err)
	}
	defer rows.Close()

	var points []store.MetricPoint
	for rows.Next() {
		var p store.MetricPoint
		var tsStr, labelsRaw string
		var serverIDStr string
		if err := rows.Scan(&p.ID, &serverIDStr, &tsStr, &p.MetricName, &p.MetricValue, &p.Unit, &labelsRaw); err != nil {
			return nil, fmt.Errorf("scanning metric: %w", err)
		}
		p.ServerID, _ = uuid.Parse(serverIDStr)
		p.Timestamp, _ = time.Parse(time.RFC3339, tsStr)
		p.Labels = make(map[string]string)
		if labelsRaw != "" {
			// Gracefully handle invalid JSON — leave as empty map
			json.Unmarshal([]byte(labelsRaw), &p.Labels)
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("querying metrics: %w", err)
	}
	return points, nil
}

func (s *metricStore) LatestByServer(ctx context.Context, serverID uuid.UUID) ([]store.MetricPoint, error) {
	var points []store.MetricPoint
	err := s.db.NewRaw(`
		SELECT m.id, m.server_id, m.timestamp, m.metric_name, m.metric_value, m.unit, m.labels
		FROM metrics m
		INNER JOIN (
		    SELECT metric_name, MAX(timestamp) AS max_ts
		    FROM metrics
		    WHERE server_id = ?
		    GROUP BY metric_name
		) latest ON m.metric_name = latest.metric_name AND m.timestamp = latest.max_ts
		WHERE m.server_id = ?`, serverID.String(), serverID.String(),
	).Scan(ctx, &points)
	if err != nil {
		return nil, fmt.Errorf("querying latest metrics: %w", err)
	}
	return points, nil
}

func (s *metricStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		res, err := s.db.NewRaw(
			"DELETE FROM metrics WHERE rowid IN (SELECT m.rowid FROM metrics m WHERE m.created_at < ? LIMIT 1000)",
			cutoff,
		).Exec(ctx)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning metrics: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}

// marshalMetadataJSON converts a map to a JSON string for metadata fields.
func marshalMetadataJSON(m map[string]any) string {
	if m == nil {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// toAnyMap converts map[string]string to map[string]any for JSON marshaling.
func toAnyMap(m map[string]string) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
