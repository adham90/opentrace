package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type deployStore struct {
	db *sql.DB
	q  *Queries
}

// NewDeployStore creates a new SQLite-backed DeployStore.
func NewDeployStore(db *sql.DB) store.DeployStore {
	return &deployStore{db: db, q: New(db)}
}

func (s *deployStore) Create(ctx context.Context, params store.CreateDeployParams) (*store.Deploy, error) {
	deployedAt := time.Now().UTC()
	if params.DeployedAt != nil {
		deployedAt = *params.DeployedAt
	}
	now := time.Now().UTC().Format(time.RFC3339)
	deployedAtStr := deployedAt.Format(time.RFC3339)

	filesJSON := []byte("[]")
	if params.FilesChanged != nil {
		if b, err := json.Marshal(params.FilesChanged); err == nil {
			filesJSON = b
		}
	}

	src := params.DeploySource
	if src == "" {
		src = store.DeploySourceWebhook
	}

	row, err := s.q.CreateDeploy(ctx, CreateDeployParams{
		Service:          params.Service,
		Environment:      params.Environment,
		CommitHash:       params.CommitHash,
		Branch:           params.Branch,
		Author:           params.Author,
		FilesChangedJson: string(filesJSON),
		DeploySource:     string(src),
		DeployedAt:       deployedAtStr,
		CreatedAt:        now,
	})
	if err != nil {
		return nil, fmt.Errorf("creating deploy: %w", err)
	}

	d := toStoreDeploy(row)
	return &d, nil
}

func (s *deployStore) GetByID(ctx context.Context, id int64) (*store.Deploy, error) {
	row, err := s.q.GetDeployByID(ctx, id)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying deploy: %w", err)
	}
	d := toStoreDeploy(row)
	return &d, nil
}

func (s *deployStore) GetByCommit(ctx context.Context, commitHash string) (*store.Deploy, error) {
	row, err := s.q.GetDeployByCommit(ctx, commitHash)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying deploy by commit: %w", err)
	}
	d := toStoreDeploy(row)
	return &d, nil
}

func (s *deployStore) GetRecent(ctx context.Context, service string, limit int) ([]store.Deploy, error) {
	if limit <= 0 {
		limit = 10
	}

	if service != "" {
		rows, err := s.q.ListRecentDeploysByService(ctx, ListRecentDeploysByServiceParams{
			Service:    service,
			MaxResults: int64(limit),
		})
		if err != nil {
			return nil, fmt.Errorf("querying recent deploys: %w", err)
		}
		return toStoreDeploys(rows), nil
	}

	rows, err := s.q.ListRecentDeploys(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("querying recent deploys: %w", err)
	}
	return toStoreDeploys(rows), nil
}

func (s *deployStore) MeasureImpact(ctx context.Context, id int64, impact store.DeployImpact) error {
	now := time.Now().UTC().Format(time.RFC3339)
	status := store.DeployStatusMeasured
	if impact.IsIncident {
		status = store.DeployStatusIncident
	}

	return s.q.MeasureDeployImpact(ctx, MeasureDeployImpactParams{
		PreErrorRate:      nullFloat64Val(impact.PreErrorRate),
		PostErrorRate:     nullFloat64Val(impact.PostErrorRate),
		PreAvgDurationMs:  nullFloat64Val(impact.PreAvgDurationMs),
		PostAvgDurationMs: nullFloat64Val(impact.PostAvgDurationMs),
		ImpactMeasuredAt:  nullString(now),
		Status:            string(status),
		ID:                id,
	})
}

// LinkInvestigation uses a transaction for the read-modify-write to prevent concurrent data loss.
func (s *deployStore) LinkInvestigation(ctx context.Context, id int64, sessionID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("linking investigation to deploy: %w", err)
	}
	defer tx.Rollback()

	var linkedJSON string
	err = tx.QueryRowContext(ctx,
		`SELECT linked_investigation_ids_json FROM deploys WHERE id = ?`, id,
	).Scan(&linkedJSON)
	if err != nil {
		return fmt.Errorf("reading linked investigations: %w", err)
	}

	var linked []string
	if err := json.Unmarshal([]byte(linkedJSON), &linked); err != nil {
		linked = nil
	}

	// Dedup
	for _, existing := range linked {
		if existing == sessionID {
			return nil // already linked
		}
	}
	linked = append(linked, sessionID)

	newJSON, err := json.Marshal(linked)
	if err != nil {
		return fmt.Errorf("marshaling linked investigations: %w", err)
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE deploys SET linked_investigation_ids_json = ? WHERE id = ?`,
		string(newJSON), id,
	)
	if err != nil {
		return fmt.Errorf("linking investigation to deploy: %w", err)
	}
	return tx.Commit()
}

func (s *deployStore) GetPendingMeasurement(ctx context.Context, olderThan time.Duration) ([]store.Deploy, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	rows, err := s.q.ListPendingMeasurement(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("querying pending deploys: %w", err)
	}
	return toStoreDeploys(rows), nil
}

func (s *deployStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	return s.q.PruneDeploys(ctx, cutoff)
}
