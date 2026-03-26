package db

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
	q  *Queries
}

// NewDataSourceStore creates a new DataSourceStore backed by SQLite.
func NewDataSourceStore(db *sql.DB) store.DataSourceStore {
	return &dataSourceStore{db: db, q: New(db)}
}

func (s *dataSourceStore) Create(ctx context.Context, params store.CreateDataSourceParams) (*store.DataSource, error) {
	configJSON, err := json.Marshal(params.Config)
	if err != nil {
		return nil, fmt.Errorf("marshaling config: %w", err)
	}

	id := uuid.New()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	err = s.q.CreateDataSource(ctx, CreateDataSourceParams{
		ID:        id.String(),
		Type:      string(params.Type),
		Name:      params.Name,
		Config:    string(configJSON),
		CreatedAt: nowStr,
		UpdatedAt: nowStr,
	})
	if err != nil {
		return nil, fmt.Errorf("inserting data source: %w", err)
	}

	return &store.DataSource{
		ID:        id,
		Type:      params.Type,
		Name:      params.Name,
		Config:    params.Config,
		Status:    store.StatusDisconnected,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (s *dataSourceStore) GetByID(ctx context.Context, id uuid.UUID) (*store.DataSource, error) {
	row, err := s.q.GetDataSourceByID(ctx, id.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("querying data source: %w", err)
	}
	ds, err := toStoreDataSourceFromRow(row)
	if err != nil {
		return nil, err
	}
	return &ds, nil
}

func (s *dataSourceStore) List(ctx context.Context, params store.ListDataSourceParams) ([]store.DataSource, error) {
	if params.Type != "" {
		rows, err := s.q.ListDataSourcesByType(ctx, string(params.Type))
		if err != nil {
			return nil, fmt.Errorf("querying data sources: %w", err)
		}
		result := make([]store.DataSource, 0, len(rows))
		for _, r := range rows {
			ds, err := toStoreDataSourceFromTypeRow(r)
			if err != nil {
				return nil, err
			}
			result = append(result, ds)
		}
		return result, nil
	}

	rows, err := s.q.ListAllDataSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying data sources: %w", err)
	}
	result := make([]store.DataSource, 0, len(rows))
	for _, r := range rows {
		ds, err := toStoreDataSourceFromListRow(r)
		if err != nil {
			return nil, err
		}
		result = append(result, ds)
	}
	return result, nil
}

// Update uses dynamic SET clause — kept hand-written.
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
	n, err := s.q.DeleteDataSource(ctx, id.String())
	if err != nil {
		return fmt.Errorf("deleting data source: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}
