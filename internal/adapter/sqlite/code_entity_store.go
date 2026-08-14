package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type codeEntityStore struct {
	db *bun.DB
}

// NewCodeEntityStore creates a new SQLite-backed CodeEntityStore.
func NewCodeEntityStore(db *bun.DB) store.CodeEntityStore {
	return &codeEntityStore{db: db}
}

func (s *codeEntityStore) Upsert(ctx context.Context, params store.UpsertCodeEntityParams) (*store.CodeEntity, error) {
	// RFC3339 strings, matching IncrementError/IncrementInvestigation and the
	// RFC3339 cutoff Prune compares against. A bun model insert would render
	// these timestamps in bun's own format and leave two incomparable formats
	// in the same columns of this table.
	now := rfc3339(time.Now().UTC())

	_, err := s.db.NewRaw(`
		INSERT INTO code_entities (entity_type, entity_name, service, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (entity_type, entity_name, service) DO UPDATE SET
			updated_at = excluded.updated_at`,
		string(params.EntityType), params.EntityName, params.Service, now, now,
	).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("upserting code entity: %w", err)
	}
	return s.GetByName(ctx, params.EntityType, params.EntityName, params.Service)
}

func (s *codeEntityStore) GetByName(ctx context.Context, entityType store.CodeEntityType, entityName, service string) (*store.CodeEntity, error) {
	e := new(store.CodeEntity)
	err := s.db.NewSelect().Model(e).
		Where("entity_type = ?", string(entityType)).
		Where("entity_name = ?", entityName).
		Where("service = ?", service).
		Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying code entity: %w", err)
	}
	return e, nil
}

func (s *codeEntityStore) TopByRisk(ctx context.Context, service string, limit int) ([]store.CodeEntity, error) {
	if limit <= 0 {
		limit = 10
	}

	var entities []store.CodeEntity
	q := s.db.NewSelect().Model(&entities)
	if service != "" {
		q = q.Where("service = ?", service)
	}
	q = q.OrderExpr("risk_score DESC").Limit(limit)
	err := q.Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying top by risk: %w", err)
	}
	return entities, nil
}

// BatchGetRisk uses bun's IN clause for dynamic name lists.
func (s *codeEntityStore) BatchGetRisk(ctx context.Context, entityType store.CodeEntityType, names []string, service string) ([]store.CodeEntity, error) {
	if len(names) == 0 {
		return nil, nil
	}

	var entities []store.CodeEntity
	err := s.db.NewSelect().Model(&entities).
		Where("entity_type = ?", string(entityType)).
		Where("entity_name IN (?)", bun.In(names)).
		Where("service = ?", service).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("batch get risk: %w", err)
	}
	return entities, nil
}

func (s *codeEntityStore) IncrementError(ctx context.Context, entityType store.CodeEntityType, entityName, service string) error {
	now := rfc3339(time.Now().UTC())
	_, err := s.db.NewRaw(`
		INSERT INTO code_entities (entity_type, entity_name, service, error_count, last_error_at, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?, ?)
		ON CONFLICT(entity_type, entity_name, service) DO UPDATE SET
		  error_count = error_count + 1,
		  last_error_at = ?,
		  updated_at = ?`,
		string(entityType), entityName, service, now, now, now, now, now,
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("incrementing error count: %w", err)
	}
	return nil
}

func (s *codeEntityStore) IncrementInvestigation(ctx context.Context, entityType store.CodeEntityType, entityName, service string) error {
	now := rfc3339(time.Now().UTC())
	_, err := s.db.NewRaw(`
		INSERT INTO code_entities (entity_type, entity_name, service, investigation_count, last_investigation_at, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?, ?)
		ON CONFLICT(entity_type, entity_name, service) DO UPDATE SET
		  investigation_count = investigation_count + 1,
		  last_investigation_at = ?,
		  updated_at = ?`,
		string(entityType), entityName, service, now, now, now, now, now,
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("incrementing investigation count: %w", err)
	}
	return nil
}

func (s *codeEntityStore) BatchRecomputeRisk(ctx context.Context) error {
	now := rfc3339(time.Now().UTC())
	_, err := s.db.NewRaw(`
		UPDATE code_entities SET
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
	).Exec(ctx)
	return err
}

func (s *codeEntityStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := rfc3339(time.Now().UTC().Add(-olderThan))
	res, err := s.db.NewDelete().Model((*store.CodeEntity)(nil)).
		Where("error_count = 0 AND investigation_count = 0 AND updated_at < ?", cutoff).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("pruning code entities: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
