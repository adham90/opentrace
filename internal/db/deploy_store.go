package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type deployStore struct {
	db *bun.DB
}

// NewDeployStore creates a new SQLite-backed DeployStore.
func NewDeployStore(db *bun.DB) store.DeployStore {
	return &deployStore{db: db}
}

func (s *deployStore) Create(ctx context.Context, params store.CreateDeployParams) (*store.Deploy, error) {
	deployedAt := time.Now().UTC()
	if params.DeployedAt != nil {
		deployedAt = *params.DeployedAt
	}
	now := time.Now().UTC()

	filesChanged := params.FilesChanged
	if filesChanged == nil {
		filesChanged = []string{}
	}

	src := params.DeploySource
	if src == "" {
		src = store.DeploySourceWebhook
	}

	d := store.Deploy{
		Service:                params.Service,
		Environment:            params.Environment,
		CommitHash:             params.CommitHash,
		Branch:                 params.Branch,
		Author:                 params.Author,
		FilesChanged:           filesChanged,
		DeploySource:           src,
		Status:                 store.DeployStatusPending,
		LinkedInvestigationIDs: []string{},
		DeployedAt:             deployedAt,
		CreatedAt:              now,
	}

	_, err := s.db.NewInsert().Model(&d).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating deploy: %w", err)
	}

	return &d, nil
}

func (s *deployStore) GetByID(ctx context.Context, id int64) (*store.Deploy, error) {
	d := new(store.Deploy)
	err := s.db.NewSelect().Model(d).Where("id = ?", id).Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying deploy: %w", err)
	}
	return d, nil
}

func (s *deployStore) GetByCommit(ctx context.Context, commitHash string) (*store.Deploy, error) {
	d := new(store.Deploy)
	err := s.db.NewSelect().Model(d).Where("commit_hash = ?", commitHash).Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying deploy by commit: %w", err)
	}
	return d, nil
}

func (s *deployStore) GetRecent(ctx context.Context, service string, limit int) ([]store.Deploy, error) {
	if limit <= 0 {
		limit = 10
	}

	var deploys []store.Deploy
	q := s.db.NewSelect().Model(&deploys)
	if service != "" {
		q = q.Where("service = ?", service)
	}
	q = q.OrderExpr("deployed_at DESC").Limit(limit)
	err := q.Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying recent deploys: %w", err)
	}
	return deploys, nil
}

func (s *deployStore) MeasureImpact(ctx context.Context, id int64, impact store.DeployImpact) error {
	now := time.Now().UTC()
	status := store.DeployStatusMeasured
	if impact.IsIncident {
		status = store.DeployStatusIncident
	}

	_, err := s.db.NewUpdate().Model((*store.Deploy)(nil)).
		Set("pre_error_rate = ?", impact.PreErrorRate).
		Set("post_error_rate = ?", impact.PostErrorRate).
		Set("pre_avg_duration_ms = ?", impact.PreAvgDurationMs).
		Set("post_avg_duration_ms = ?", impact.PostAvgDurationMs).
		Set("impact_measured_at = ?", now).
		Set("status = ?", string(status)).
		Where("id = ?", id).
		Exec(ctx)
	return err
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
	var deploys []store.Deploy
	err := s.db.NewSelect().Model(&deploys).
		Where("status = ?", string(store.DeployStatusPending)).
		Where("deployed_at < ?", cutoff).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying pending deploys: %w", err)
	}
	return deploys, nil
}

func (s *deployStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	res, err := s.db.NewDelete().Model((*store.Deploy)(nil)).
		Where("created_at < ?", cutoff).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("pruning deploys: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
