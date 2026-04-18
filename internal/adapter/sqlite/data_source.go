package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

// dataSourceStore implements DataSourceStore using bun (SQLite).
type dataSourceStore struct {
	db *bun.DB
}

// NewDataSourceStore creates a new DataSourceStore backed by SQLite.
func NewDataSourceStore(db *bun.DB) store.DataSourceStore {
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

	_, err = s.db.NewRaw(
		`INSERT INTO data_sources (id, type, name, config, status, environment, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id.String(), string(params.Type), params.Name, string(configJSON),
		string(store.StatusDisconnected), params.Environment, nowStr, nowStr,
	).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("inserting data source: %w", err)
	}

	return &store.DataSource{
		ID:          id,
		Type:        params.Type,
		Name:        params.Name,
		Config:      params.Config,
		Status:      store.StatusDisconnected,
		Environment: params.Environment,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (s *dataSourceStore) GetByID(ctx context.Context, id uuid.UUID) (*store.DataSource, error) {
	ds := new(store.DataSource)
	err := s.db.NewSelect().Model(ds).Where("id = ?", id.String()).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("querying data source: %w", err)
	}
	return ds, nil
}

func (s *dataSourceStore) List(ctx context.Context, params store.ListDataSourceParams) ([]store.DataSource, error) {
	sources := make([]store.DataSource, 0)
	q := s.db.NewSelect().Model(&sources)
	if params.Type != "" {
		q = q.Where("type = ?", string(params.Type))
	}
	if params.Environment != "" {
		// A "*" scope matches every env; otherwise, exact match plus any
		// connector explicitly marked as shared ("*") is included.
		if params.Environment == "*" {
			// no-op — caller wants everything
		} else {
			q = q.Where("environment = ? OR environment = '*'", params.Environment)
		}
	}
	q = q.OrderExpr("name ASC")
	err := q.Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying data sources: %w", err)
	}
	return sources, nil
}

// Update uses COALESCE for dynamic SET clause.
func (s *dataSourceStore) Update(ctx context.Context, id uuid.UUID, params store.UpdateDataSourceParams) (*store.DataSource, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	var nameStr, statusStr, statusMsg, testedAt, configStr, envStr *string
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
	if params.Environment != nil {
		envStr = params.Environment
	}

	// When config changes, reset status to disconnected so the user knows to re-test.
	if params.Config != nil && params.Status == nil {
		disconnected := string(store.StatusDisconnected)
		statusStr = &disconnected
	}

	result, err := s.db.NewRaw(
		`UPDATE data_sources
		 SET name = COALESCE(?, name),
		     config = COALESCE(?, config),
		     status = COALESCE(?, status),
		     status_message = COALESCE(?, status_message),
		     last_tested_at = COALESCE(?, last_tested_at),
		     environment = COALESCE(?, environment),
		     updated_at = ?
		 WHERE id = ?`,
		nameStr, configStr, statusStr, statusMsg, testedAt, envStr, now, id.String(),
	).Exec(ctx)
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
	res, err := s.db.NewDelete().Model((*store.DataSource)(nil)).
		Where("id = ?", id.String()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("deleting data source: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}
