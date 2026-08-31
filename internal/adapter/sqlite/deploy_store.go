package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

// maxDeployListLimit caps a deploy listing. Deploys are low-cardinality
// compared to logs, but an uncapped limit is still an unbounded scan.
const maxDeployListLimit = 500

// defaultDeployListLimit applies when the caller does not ask for one.
const defaultDeployListLimit = 50

type deployStore struct {
	db *bun.DB
}

// NewDeployStore creates a DeployStore backed by SQLite.
func NewDeployStore(db *bun.DB) store.DeployStore {
	return &deployStore{db: db}
}

// Record inserts a deploy marker, ignoring commits already recorded for the
// same (service, environment). It runs on the ingest path, so the write is a
// single statement with no read-modify-write and no transaction.
func (s *deployStore) Record(ctx context.Context, d store.Deploy) error {
	if d.CommitHash == "" {
		return nil
	}
	at := d.FirstSeenAt
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.db.NewRaw(`
		INSERT OR IGNORE INTO deploys (commit_hash, service, environment, first_seen_at)
		VALUES (?, ?, ?, ?)`,
		d.CommitHash, d.Service, d.Environment, at.UTC().Format(time.RFC3339),
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("recording deploy: %w", err)
	}
	return nil
}

// Latest returns the newest deploy in scope. An empty service or environment
// widens the scope rather than filtering on the empty string, so an unscoped
// internal caller sees every deploy.
func (s *deployStore) Latest(ctx context.Context, service, environment string) (*store.Deploy, error) {
	query := `SELECT id, commit_hash, service, environment, first_seen_at FROM deploys WHERE 1=1`
	var args []any
	if service != "" {
		query += ` AND service = ?`
		args = append(args, service)
	}
	if environment != "" {
		query += ` AND environment = ?`
		args = append(args, environment)
	}
	query += ` ORDER BY first_seen_at DESC, id DESC LIMIT 1`

	var (
		id          int64
		commitHash  string
		svc         string
		env         string
		firstSeenAt string
	)
	err := s.db.NewRaw(query, args...).Scan(ctx, &id, &commitHash, &svc, &env, &firstSeenAt)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading latest deploy: %w", err)
	}
	return &store.Deploy{
		ID:          id,
		CommitHash:  commitHash,
		Service:     svc,
		Environment: env,
		FirstSeenAt: parseTime(firstSeenAt),
	}, nil
}

func (s *deployStore) List(ctx context.Context, params store.ListDeployParams) ([]store.Deploy, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = defaultDeployListLimit
	}
	if limit > maxDeployListLimit {
		limit = maxDeployListLimit
	}

	query := `SELECT id, commit_hash, service, environment, first_seen_at FROM deploys WHERE 1=1`
	var args []any
	if params.Service != "" {
		query += ` AND service = ?`
		args = append(args, params.Service)
	}
	if params.Environment != "" {
		query += ` AND environment = ?`
		args = append(args, params.Environment)
	}
	if params.Since != nil && !params.Since.IsZero() {
		query += ` AND first_seen_at >= ?`
		args = append(args, params.Since.UTC().Format(time.RFC3339))
	}
	query += ` ORDER BY first_seen_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	type row struct {
		ID          int64  `bun:"id"`
		CommitHash  string `bun:"commit_hash"`
		Service     string `bun:"service"`
		Environment string `bun:"environment"`
		FirstSeenAt string `bun:"first_seen_at"`
	}
	var rows []row
	if err := s.db.NewRaw(query, args...).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("listing deploys: %w", err)
	}

	result := make([]store.Deploy, len(rows))
	for i, r := range rows {
		result[i] = store.Deploy{
			ID:          r.ID,
			CommitHash:  r.CommitHash,
			Service:     r.Service,
			Environment: r.Environment,
			FirstSeenAt: parseTime(r.FirstSeenAt),
		}
	}
	return result, nil
}

func (s *deployStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	res, err := s.db.NewRaw(`DELETE FROM deploys WHERE first_seen_at < ?`, cutoff).Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("pruning deploys: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}
