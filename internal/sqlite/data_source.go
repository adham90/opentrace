package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/pkg/store"
)

// dataSourceStore implements DataSourceStore using database/sql (SQLite).
type dataSourceStore struct {
	db *sql.DB
}

// NewDataSourceStore creates a new DataSourceStore backed by SQLite.
func NewDataSourceStore(db *sql.DB) store.DataSourceStore {
	return &dataSourceStore{db: db}
}

func (s *dataSourceStore) Create(ctx context.Context, params store.CreateDataSourceParams) (*store.DataSource, error) {
	configJSON, err := json.Marshal(params.Config)
	if err != nil {
		return nil, fmt.Errorf("marshaling config: %w", err)
	}

	id := uuid.New()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO data_sources (id, type, name, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id.String(), params.Type, params.Name, string(configJSON), nowStr, nowStr,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting data source: %w", err)
	}

	return &store.DataSource{
		ID:          id,
		Type:        params.Type,
		Name:        params.Name,
		Config:      params.Config,
		Status:      store.StatusDisconnected,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (s *dataSourceStore) GetByID(ctx context.Context, id uuid.UUID) (*store.DataSource, error) {
	ds := &store.DataSource{}
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
			return nil, store.ErrNotFound
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

func (s *dataSourceStore) List(ctx context.Context, params store.ListDataSourceParams) ([]store.DataSource, error) {
	query := `SELECT id, type, name, config, status, status_message, last_tested_at, created_at, updated_at
		 FROM data_sources WHERE 1=1`
	var args []any
	if params.Type != "" {
		query += ` AND type = ?`
		args = append(args, string(params.Type))
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying data sources: %w", err)
	}
	defer rows.Close()

	result := make([]store.DataSource, 0)
	for rows.Next() {
		var ds store.DataSource
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

func (s *dataSourceStore) Update(ctx context.Context, id uuid.UUID, params store.UpdateDataSourceParams) (*store.DataSource, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	var nameStr, statusStr, statusMsg, testedAt, configStr *string
	if params.Name != nil {
		nameStr = params.Name
	}
	if params.Config != nil {
		configJSON, err := json.Marshal(params.Config)
		if err != nil {
			return nil, fmt.Errorf("marshaling config: %w", err)
		}
		cs := string(configJSON)
		configStr = &cs
	}
	if params.Status != nil {
		st := string(*params.Status)
		statusStr = &st
	}
	if params.StatusMessage != nil {
		statusMsg = params.StatusMessage
	}
	if params.LastTestedAt != nil {
		ts := params.LastTestedAt.UTC().Format(time.RFC3339)
		testedAt = &ts
	}

	// When config changes, reset status to disconnected so the user knows to re-test.
	if params.Config != nil && params.Status == nil {
		disconnected := string(store.StatusDisconnected)
		statusStr = &disconnected
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE data_sources
		 SET name = COALESCE(?, name),
		     config = COALESCE(?, config),
		     status = COALESCE(?, status),
		     status_message = COALESCE(?, status_message),
		     last_tested_at = COALESCE(?, last_tested_at),
		     updated_at = ?
		 WHERE id = ?`,
		nameStr, configStr, statusStr, statusMsg, testedAt, now, id.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("updating data source: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, store.ErrNotFound
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
		return store.ErrNotFound
	}
	return nil
}
