package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgDataSourceStore implements DataSourceStore using pgx.
type PgDataSourceStore struct {
	pool *pgxpool.Pool
}

// NewPgDataSourceStore creates a new PgDataSourceStore.
func NewPgDataSourceStore(pool *pgxpool.Pool) *PgDataSourceStore {
	return &PgDataSourceStore{pool: pool}
}

func (s *PgDataSourceStore) Create(ctx context.Context, params CreateDataSourceParams) (*DataSource, error) {
	configJSON, err := json.Marshal(params.Config)
	if err != nil {
		return nil, fmt.Errorf("marshaling config: %w", err)
	}

	ds := &DataSource{}
	err = s.pool.QueryRow(ctx,
		`INSERT INTO data_sources (type, name, config)
		 VALUES ($1, $2, $3)
		 RETURNING id, type, name, config, status, status_message, last_tested_at, created_at, updated_at`,
		params.Type, params.Name, configJSON,
	).Scan(
		&ds.ID, &ds.Type, &ds.Name, &configJSON,
		&ds.Status, &ds.StatusMessage, &ds.LastTestedAt,
		&ds.CreatedAt, &ds.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting data source: %w", err)
	}

	if err := json.Unmarshal(configJSON, &ds.Config); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return ds, nil
}

func (s *PgDataSourceStore) GetByID(ctx context.Context, id uuid.UUID) (*DataSource, error) {
	ds := &DataSource{}
	var configJSON []byte

	err := s.pool.QueryRow(ctx,
		`SELECT id, type, name, config, status, status_message, last_tested_at, created_at, updated_at
		 FROM data_sources WHERE id = $1`, id,
	).Scan(
		&ds.ID, &ds.Type, &ds.Name, &configJSON,
		&ds.Status, &ds.StatusMessage, &ds.LastTestedAt,
		&ds.CreatedAt, &ds.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying data source: %w", err)
	}

	if err := json.Unmarshal(configJSON, &ds.Config); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return ds, nil
}

func (s *PgDataSourceStore) List(ctx context.Context) ([]DataSource, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, type, name, config, status, status_message, last_tested_at, created_at, updated_at
		 FROM data_sources ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying data sources: %w", err)
	}
	defer rows.Close()

	result := make([]DataSource, 0)
	for rows.Next() {
		var ds DataSource
		var configJSON []byte

		if err := rows.Scan(
			&ds.ID, &ds.Type, &ds.Name, &configJSON,
			&ds.Status, &ds.StatusMessage, &ds.LastTestedAt,
			&ds.CreatedAt, &ds.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning data source: %w", err)
		}

		if err := json.Unmarshal(configJSON, &ds.Config); err != nil {
			return nil, fmt.Errorf("unmarshaling config: %w", err)
		}

		result = append(result, ds)
	}

	return result, rows.Err()
}

func (s *PgDataSourceStore) Update(ctx context.Context, id uuid.UUID, params UpdateDataSourceParams) (*DataSource, error) {
	// Build dynamic update
	now := time.Now()
	ds := &DataSource{}
	var configJSON []byte

	err := s.pool.QueryRow(ctx,
		`UPDATE data_sources
		 SET status = COALESCE($2, status),
		     status_message = COALESCE($3, status_message),
		     last_tested_at = COALESCE($4, last_tested_at),
		     updated_at = $5
		 WHERE id = $1
		 RETURNING id, type, name, config, status, status_message, last_tested_at, created_at, updated_at`,
		id, params.Status, params.StatusMessage, params.LastTestedAt, now,
	).Scan(
		&ds.ID, &ds.Type, &ds.Name, &configJSON,
		&ds.Status, &ds.StatusMessage, &ds.LastTestedAt,
		&ds.CreatedAt, &ds.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("updating data source: %w", err)
	}

	if err := json.Unmarshal(configJSON, &ds.Config); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return ds, nil
}

func (s *PgDataSourceStore) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM data_sources WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting data source: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
