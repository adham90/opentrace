package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type codeEntityStore struct {
	db *sql.DB
}

// NewCodeEntityStore creates a new SQLite-backed CodeEntityStore.
func NewCodeEntityStore(db *sql.DB) CodeEntityStore {
	return &codeEntityStore{db: db}
}

func (s *codeEntityStore) Upsert(ctx context.Context, params UpsertCodeEntityParams) (*CodeEntity, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO code_entities (entity_type, entity_name, service, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(entity_type, entity_name, service) DO UPDATE SET updated_at = ?`,
		string(params.EntityType), params.EntityName, params.Service, now, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("upserting code entity: %w", err)
	}

	return s.GetByName(ctx, params.EntityType, params.EntityName, params.Service)
}

func (s *codeEntityStore) GetByName(ctx context.Context, entityType CodeEntityType, entityName, service string) (*CodeEntity, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, entity_type, entity_name, service, risk_score, error_count,
		        investigation_count, avg_duration_ms, last_error_at, last_investigation_at,
		        metadata_json, created_at, updated_at
		 FROM code_entities
		 WHERE entity_type = ? AND entity_name = ? AND service = ?`,
		string(entityType), entityName, service,
	)
	return scanCodeEntity(row)
}

func (s *codeEntityStore) TopByRisk(ctx context.Context, service string, limit int) ([]CodeEntity, error) {
	if limit <= 0 {
		limit = 10
	}

	var rows *sql.Rows
	var err error
	if service != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, entity_type, entity_name, service, risk_score, error_count,
			        investigation_count, avg_duration_ms, last_error_at, last_investigation_at,
			        metadata_json, created_at, updated_at
			 FROM code_entities
			 WHERE service = ?
			 ORDER BY risk_score DESC
			 LIMIT ?`,
			service, limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, entity_type, entity_name, service, risk_score, error_count,
			        investigation_count, avg_duration_ms, last_error_at, last_investigation_at,
			        metadata_json, created_at, updated_at
			 FROM code_entities
			 ORDER BY risk_score DESC
			 LIMIT ?`,
			limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("querying top by risk: %w", err)
	}
	defer rows.Close()

	return scanCodeEntities(rows)
}

func (s *codeEntityStore) BatchGetRisk(ctx context.Context, entityType CodeEntityType, names []string, service string) ([]CodeEntity, error) {
	if len(names) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(names))
	args := make([]any, 0, len(names)+2)
	args = append(args, string(entityType))
	for i, n := range names {
		placeholders[i] = "?"
		args = append(args, n)
	}
	args = append(args, service)

	query := fmt.Sprintf(
		`SELECT id, entity_type, entity_name, service, risk_score, error_count,
		        investigation_count, avg_duration_ms, last_error_at, last_investigation_at,
		        metadata_json, created_at, updated_at
		 FROM code_entities
		 WHERE entity_type = ? AND entity_name IN (%s) AND service = ?`,
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("batch get risk: %w", err)
	}
	defer rows.Close()

	return scanCodeEntities(rows)
}

func (s *codeEntityStore) IncrementError(ctx context.Context, entityType CodeEntityType, entityName, service string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO code_entities (entity_type, entity_name, service, error_count, last_error_at, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?, ?)
		 ON CONFLICT(entity_type, entity_name, service) DO UPDATE SET
		   error_count = error_count + 1,
		   last_error_at = ?,
		   updated_at = ?`,
		string(entityType), entityName, service, now, now, now, now, now,
	)
	if err != nil {
		return fmt.Errorf("incrementing error count: %w", err)
	}
	return nil
}

func (s *codeEntityStore) IncrementInvestigation(ctx context.Context, entityType CodeEntityType, entityName, service string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO code_entities (entity_type, entity_name, service, investigation_count, last_investigation_at, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?, ?)
		 ON CONFLICT(entity_type, entity_name, service) DO UPDATE SET
		   investigation_count = investigation_count + 1,
		   last_investigation_at = ?,
		   updated_at = ?`,
		string(entityType), entityName, service, now, now, now, now, now,
	)
	if err != nil {
		return fmt.Errorf("incrementing investigation count: %w", err)
	}
	return nil
}

func (s *codeEntityStore) BatchRecomputeRisk(ctx context.Context) error {
	// Risk formula: 0.4 * normalized_error + 0.3 * normalized_investigation + 0.2 * recency + 0.1 * frequency
	// recency = 1.0 if last_error_at within 24h, decays over 7 days
	// We use a simplified SQL-only computation.
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`UPDATE code_entities SET
		   risk_score = (
		     0.4 * MIN(error_count / 10.0, 1.0) +
		     0.3 * MIN(investigation_count / 5.0, 1.0) +
		     0.2 * CASE
		       WHEN last_error_at IS NULL THEN 0.0
		       WHEN (julianday(?) - julianday(last_error_at)) < 1.0 THEN 1.0
		       WHEN (julianday(?) - julianday(last_error_at)) < 7.0 THEN
		         1.0 - ((julianday(?) - julianday(last_error_at)) / 7.0)
		       ELSE 0.0
		     END +
		     0.1 * CASE
		       WHEN last_investigation_at IS NULL THEN 0.0
		       WHEN (julianday(?) - julianday(last_investigation_at)) < 7.0 THEN 1.0
		       ELSE 0.0
		     END
		   ),
		   updated_at = ?
		 WHERE error_count > 0 OR investigation_count > 0`,
		now, now, now, now, now,
	)
	if err != nil {
		return fmt.Errorf("recomputing risk scores: %w", err)
	}
	return nil
}

func (s *codeEntityStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM code_entities
		 WHERE error_count = 0 AND investigation_count = 0 AND updated_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("pruning code entities: %w", err)
	}
	return res.RowsAffected()
}

// scanCodeEntity scans a single code entity from a row.
func scanCodeEntity(row *sql.Row) (*CodeEntity, error) {
	var e CodeEntity
	var avgDur sql.NullFloat64
	var lastErr, lastInv sql.NullString
	var metaJSON string
	var createdStr, updatedStr string

	err := row.Scan(
		&e.ID, &e.EntityType, &e.EntityName, &e.Service,
		&e.RiskScore, &e.ErrorCount, &e.InvestigationCount,
		&avgDur, &lastErr, &lastInv,
		&metaJSON, &createdStr, &updatedStr,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning code entity: %w", err)
	}

	if avgDur.Valid {
		e.AvgDurationMs = &avgDur.Float64
	}
	if lastErr.Valid {
		if t, err := time.Parse(time.RFC3339, lastErr.String); err == nil {
			e.LastErrorAt = &t
		}
	}
	if lastInv.Valid {
		if t, err := time.Parse(time.RFC3339, lastInv.String); err == nil {
			e.LastInvestigationAt = &t
		}
	}
	if metaJSON != "" && metaJSON != "{}" {
		_ = json.Unmarshal([]byte(metaJSON), &e.Metadata)
	}
	e.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	e.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)

	return &e, nil
}

// scanCodeEntities scans multiple code entities from rows.
func scanCodeEntities(rows *sql.Rows) ([]CodeEntity, error) {
	var result []CodeEntity
	for rows.Next() {
		var e CodeEntity
		var avgDur sql.NullFloat64
		var lastErr, lastInv sql.NullString
		var metaJSON string
		var createdStr, updatedStr string

		err := rows.Scan(
			&e.ID, &e.EntityType, &e.EntityName, &e.Service,
			&e.RiskScore, &e.ErrorCount, &e.InvestigationCount,
			&avgDur, &lastErr, &lastInv,
			&metaJSON, &createdStr, &updatedStr,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning code entity: %w", err)
		}

		if avgDur.Valid {
			e.AvgDurationMs = &avgDur.Float64
		}
		if lastErr.Valid {
			if t, err := time.Parse(time.RFC3339, lastErr.String); err == nil {
				e.LastErrorAt = &t
			}
		}
		if lastInv.Valid {
			if t, err := time.Parse(time.RFC3339, lastInv.String); err == nil {
				e.LastInvestigationAt = &t
			}
		}
		if metaJSON != "" && metaJSON != "{}" {
			_ = json.Unmarshal([]byte(metaJSON), &e.Metadata)
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		e.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)

		result = append(result, e)
	}
	return result, rows.Err()
}
