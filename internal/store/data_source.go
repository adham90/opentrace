package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// dataSourceStore implements DataSourceStore using database/sql (SQLite).
type dataSourceStore struct {
	db *sql.DB
}

// NewDataSourceStore creates a new DataSourceStore backed by SQLite.
func NewDataSourceStore(db *sql.DB) DataSourceStore {
	return &dataSourceStore{db: db}
}

func (s *dataSourceStore) Create(ctx context.Context, params CreateDataSourceParams) (*DataSource, error) {
	configJSON, err := json.Marshal(params.Config)
	if err != nil {
		return nil, fmt.Errorf("marshaling config: %w", err)
	}

	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO data_sources (id, type, name, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id.String(), params.Type, params.Name, string(configJSON), now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting data source: %w", err)
	}

	return s.GetByID(ctx, id)
}

func (s *dataSourceStore) GetByID(ctx context.Context, id uuid.UUID) (*DataSource, error) {
	ds := &DataSource{}
	var configJSON string
	var createdAt, updatedAt string
	var lastTestedAt sql.NullString

	err := s.db.QueryRowContext(ctx,
		`SELECT id, type, name, config, status, status_message, last_tested_at, created_at, updated_at
		 FROM data_sources WHERE id = ?`, id.String(),
	).Scan(
		&ds.ID, &ds.Type, &ds.Name, &configJSON,
		&ds.Status, &ds.StatusMessage, &lastTestedAt,
		&createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying data source: %w", err)
	}

	if err := json.Unmarshal([]byte(configJSON), &ds.Config); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}
	ds.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	ds.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if lastTestedAt.Valid {
		t, _ := time.Parse(time.RFC3339, lastTestedAt.String)
		ds.LastTestedAt = &t
	}

	return ds, nil
}

func (s *dataSourceStore) List(ctx context.Context) ([]DataSource, error) {
	rows, err := s.db.QueryContext(ctx,
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
		var configJSON string
		var createdAt, updatedAt string
		var lastTestedAt sql.NullString

		if err := rows.Scan(
			&ds.ID, &ds.Type, &ds.Name, &configJSON,
			&ds.Status, &ds.StatusMessage, &lastTestedAt,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning data source: %w", err)
		}

		if err := json.Unmarshal([]byte(configJSON), &ds.Config); err != nil {
			return nil, fmt.Errorf("unmarshaling config: %w", err)
		}
		ds.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		ds.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		if lastTestedAt.Valid {
			t, _ := time.Parse(time.RFC3339, lastTestedAt.String)
			ds.LastTestedAt = &t
		}

		result = append(result, ds)
	}

	return result, rows.Err()
}

func (s *dataSourceStore) Update(ctx context.Context, id uuid.UUID, params UpdateDataSourceParams) (*DataSource, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	var statusStr, statusMsg, testedAt *string
	if params.Status != nil {
		s := string(*params.Status)
		statusStr = &s
	}
	if params.StatusMessage != nil {
		statusMsg = params.StatusMessage
	}
	if params.LastTestedAt != nil {
		s := params.LastTestedAt.UTC().Format(time.RFC3339)
		testedAt = &s
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE data_sources
		 SET status = COALESCE(?, status),
		     status_message = COALESCE(?, status_message),
		     last_tested_at = COALESCE(?, last_tested_at),
		     updated_at = ?
		 WHERE id = ?`,
		statusStr, statusMsg, testedAt, now, id.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("updating data source: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}

	return s.GetByID(ctx, id)
}

func (s *dataSourceStore) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM data_sources WHERE id = ?`, id.String())
	if err != nil {
		return fmt.Errorf("deleting data source: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
