package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

type deployStore struct {
	db *sql.DB
}

// NewDeployStore creates a new SQLite-backed DeployStore.
func NewDeployStore(db *sql.DB) DeployStore {
	return &deployStore{db: db}
}

func (s *deployStore) Create(ctx context.Context, params CreateDeployParams) (*Deploy, error) {
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
		src = DeploySourceWebhook
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO deploys (service, environment, commit_hash, branch, author,
		                      files_changed_json, deploy_source, deployed_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		params.Service, params.Environment, params.CommitHash, params.Branch, params.Author,
		string(filesJSON), string(src), deployedAtStr, now,
	)
	if err != nil {
		return nil, fmt.Errorf("creating deploy: %w", err)
	}

	id, _ := res.LastInsertId()
	return s.GetByID(ctx, id)
}

func (s *deployStore) GetByID(ctx context.Context, id int64) (*Deploy, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, service, environment, commit_hash, branch, author,
		        files_changed_json, deploy_source,
		        pre_error_rate, post_error_rate, pre_avg_duration_ms, post_avg_duration_ms,
		        impact_measured_at, linked_investigation_ids_json, status,
		        deployed_at, created_at
		 FROM deploys WHERE id = ?`, id,
	)
	return scanDeploy(row)
}

func (s *deployStore) GetByCommit(ctx context.Context, commitHash string) (*Deploy, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, service, environment, commit_hash, branch, author,
		        files_changed_json, deploy_source,
		        pre_error_rate, post_error_rate, pre_avg_duration_ms, post_avg_duration_ms,
		        impact_measured_at, linked_investigation_ids_json, status,
		        deployed_at, created_at
		 FROM deploys WHERE commit_hash = ? ORDER BY deployed_at DESC LIMIT 1`, commitHash,
	)
	return scanDeploy(row)
}

func (s *deployStore) GetRecent(ctx context.Context, service string, limit int) ([]Deploy, error) {
	if limit <= 0 {
		limit = 10
	}

	var rows *sql.Rows
	var err error
	if service != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, service, environment, commit_hash, branch, author,
			        files_changed_json, deploy_source,
			        pre_error_rate, post_error_rate, pre_avg_duration_ms, post_avg_duration_ms,
			        impact_measured_at, linked_investigation_ids_json, status,
			        deployed_at, created_at
			 FROM deploys WHERE service = ?
			 ORDER BY deployed_at DESC LIMIT ?`, service, limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, service, environment, commit_hash, branch, author,
			        files_changed_json, deploy_source,
			        pre_error_rate, post_error_rate, pre_avg_duration_ms, post_avg_duration_ms,
			        impact_measured_at, linked_investigation_ids_json, status,
			        deployed_at, created_at
			 FROM deploys
			 ORDER BY deployed_at DESC LIMIT ?`, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("querying recent deploys: %w", err)
	}
	defer rows.Close()

	return scanDeploys(rows)
}

func (s *deployStore) MeasureImpact(ctx context.Context, id int64, impact DeployImpact) error {
	now := time.Now().UTC().Format(time.RFC3339)
	status := DeployStatusMeasured
	if impact.IsIncident {
		status = DeployStatusIncident
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE deploys SET
		   pre_error_rate = ?, post_error_rate = ?,
		   pre_avg_duration_ms = ?, post_avg_duration_ms = ?,
		   impact_measured_at = ?, status = ?
		 WHERE id = ?`,
		impact.PreErrorRate, impact.PostErrorRate,
		impact.PreAvgDurationMs, impact.PostAvgDurationMs,
		now, string(status), id,
	)
	if err != nil {
		return fmt.Errorf("measuring deploy impact: %w", err)
	}
	return nil
}

func (s *deployStore) LinkInvestigation(ctx context.Context, id int64, sessionID string) error {
	// Use a transaction for the read-modify-write to prevent concurrent data loss.
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

func (s *deployStore) GetPendingMeasurement(ctx context.Context, olderThan time.Duration) ([]Deploy, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, service, environment, commit_hash, branch, author,
		        files_changed_json, deploy_source,
		        pre_error_rate, post_error_rate, pre_avg_duration_ms, post_avg_duration_ms,
		        impact_measured_at, linked_investigation_ids_json, status,
		        deployed_at, created_at
		 FROM deploys
		 WHERE status = 'pending' AND deployed_at < ?
		 ORDER BY deployed_at ASC`, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("querying pending deploys: %w", err)
	}
	defer rows.Close()

	return scanDeploys(rows)
}

func (s *deployStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM deploys WHERE deployed_at < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("pruning deploys: %w", err)
	}
	return res.RowsAffected()
}

func scanDeploy(row *sql.Row) (*Deploy, error) {
	var d Deploy
	var filesJSON, linkedJSON string
	var preErr, postErr, preDur, postDur sql.NullFloat64
	var impactAt sql.NullString
	var deployedStr, createdStr string

	err := row.Scan(
		&d.ID, &d.Service, &d.Environment, &d.CommitHash, &d.Branch, &d.Author,
		&filesJSON, &d.DeploySource,
		&preErr, &postErr, &preDur, &postDur,
		&impactAt, &linkedJSON, &d.Status,
		&deployedStr, &createdStr,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning deploy: %w", err)
	}

	if err := json.Unmarshal([]byte(filesJSON), &d.FilesChanged); err != nil {
		slog.Warn("deploy: malformed files_changed_json", "id", d.ID, "error", err)
	}
	if err := json.Unmarshal([]byte(linkedJSON), &d.LinkedInvestigationIDs); err != nil {
		slog.Warn("deploy: malformed linked_investigation_ids_json", "id", d.ID, "error", err)
	}
	if preErr.Valid {
		d.PreErrorRate = &preErr.Float64
	}
	if postErr.Valid {
		d.PostErrorRate = &postErr.Float64
	}
	if preDur.Valid {
		d.PreAvgDurationMs = &preDur.Float64
	}
	if postDur.Valid {
		d.PostAvgDurationMs = &postDur.Float64
	}
	if impactAt.Valid {
		if t, err := time.Parse(time.RFC3339, impactAt.String); err == nil {
			d.ImpactMeasuredAt = &t
		}
	}
	d.DeployedAt, _ = time.Parse(time.RFC3339, deployedStr)
	d.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)

	return &d, nil
}

func scanDeploys(rows *sql.Rows) ([]Deploy, error) {
	var result []Deploy
	for rows.Next() {
		var d Deploy
		var filesJSON, linkedJSON string
		var preErr, postErr, preDur, postDur sql.NullFloat64
		var impactAt sql.NullString
		var deployedStr, createdStr string

		err := rows.Scan(
			&d.ID, &d.Service, &d.Environment, &d.CommitHash, &d.Branch, &d.Author,
			&filesJSON, &d.DeploySource,
			&preErr, &postErr, &preDur, &postDur,
			&impactAt, &linkedJSON, &d.Status,
			&deployedStr, &createdStr,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning deploy: %w", err)
		}

		if err := json.Unmarshal([]byte(filesJSON), &d.FilesChanged); err != nil {
			slog.Warn("deploy: malformed files_changed_json", "id", d.ID, "error", err)
		}
		if err := json.Unmarshal([]byte(linkedJSON), &d.LinkedInvestigationIDs); err != nil {
			slog.Warn("deploy: malformed linked_investigation_ids_json", "id", d.ID, "error", err)
		}
		if preErr.Valid {
			d.PreErrorRate = &preErr.Float64
		}
		if postErr.Valid {
			d.PostErrorRate = &postErr.Float64
		}
		if preDur.Valid {
			d.PreAvgDurationMs = &preDur.Float64
		}
		if postDur.Valid {
			d.PostAvgDurationMs = &postDur.Float64
		}
		if impactAt.Valid {
			if t, err := time.Parse(time.RFC3339, impactAt.String); err == nil {
				d.ImpactMeasuredAt = &t
			}
		}
		d.DeployedAt, _ = time.Parse(time.RFC3339, deployedStr)
		d.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)

		result = append(result, d)
	}
	return result, rows.Err()
}
