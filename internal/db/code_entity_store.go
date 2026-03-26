package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type codeEntityStore struct {
	db *sql.DB
	q  *Queries
}

// NewCodeEntityStore creates a new SQLite-backed CodeEntityStore.
func NewCodeEntityStore(db *sql.DB) store.CodeEntityStore {
	return &codeEntityStore{db: db, q: New(db)}
}

func (s *codeEntityStore) Upsert(ctx context.Context, params store.UpsertCodeEntityParams) (*store.CodeEntity, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	// Hand-written: sqlc-generated query has unresolved sqlc.arg() directives.
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

func (s *codeEntityStore) GetByName(ctx context.Context, entityType store.CodeEntityType, entityName, service string) (*store.CodeEntity, error) {
	row, err := s.q.GetCodeEntityByName(ctx, GetCodeEntityByNameParams{
		EntityType: string(entityType),
		EntityName: entityName,
		Service:    service,
	})
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying code entity: %w", err)
	}
	e := toStoreCodeEntity(row)
	return &e, nil
}

func (s *codeEntityStore) TopByRisk(ctx context.Context, service string, limit int) ([]store.CodeEntity, error) {
	if limit <= 0 {
		limit = 10
	}

	if service != "" {
		rows, err := s.q.ListTopCodeEntitiesByRiskForService(ctx, ListTopCodeEntitiesByRiskForServiceParams{
			Service:    service,
			MaxResults: int64(limit),
		})
		if err != nil {
			return nil, fmt.Errorf("querying top by risk: %w", err)
		}
		return toStoreCodeEntities(rows), nil
	}

	rows, err := s.q.ListTopCodeEntitiesByRisk(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("querying top by risk: %w", err)
	}
	return toStoreCodeEntities(rows), nil
}

// BatchGetRisk uses a dynamic IN clause — kept hand-written.
func (s *codeEntityStore) BatchGetRisk(ctx context.Context, entityType store.CodeEntityType, names []string, service string) ([]store.CodeEntity, error) {
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

	return scanCodeEntitiesRows(rows)
}

// IncrementError is hand-written: sqlc-generated query has unresolved sqlc.arg() directives.
func (s *codeEntityStore) IncrementError(ctx context.Context, entityType store.CodeEntityType, entityName, service string) error {
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

// IncrementInvestigation is hand-written: sqlc-generated query has unresolved sqlc.arg() directives.
func (s *codeEntityStore) IncrementInvestigation(ctx context.Context, entityType store.CodeEntityType, entityName, service string) error {
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
	now := time.Now().UTC().Format(time.RFC3339)
	return s.q.BatchRecomputeCodeEntityRisk(ctx, now)
}

func (s *codeEntityStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	return s.q.PruneCodeEntities(ctx, cutoff)
}

// scanCodeEntitiesRows scans multiple code entities from rows (used by hand-written BatchGetRisk).
func scanCodeEntitiesRows(rows *sql.Rows) ([]store.CodeEntity, error) {
	var result []store.CodeEntity
	for rows.Next() {
		var e store.CodeEntity
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
