package db

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type auditStore struct {
	db *bun.DB
}

// NewAuditStore creates a new AuditStore backed by SQLite.
func NewAuditStore(db *bun.DB) store.AuditStore {
	return &auditStore{db: db}
}

func (s *auditStore) Log(ctx context.Context, params store.LogAuditParams) error {
	entry := &store.AuditEntry{
		UserID:     params.UserID,
		UserEmail:  params.UserEmail,
		Action:     params.Action,
		TargetType: params.TargetType,
		TargetID:   params.TargetID,
		Details:    params.Details,
		IPAddress:  params.IPAddress,
		CreatedAt:  time.Now().UTC(),
	}
	_, err := s.db.NewInsert().Model(entry).
		ExcludeColumn("id").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("logging audit entry: %w", err)
	}
	return nil
}

func (s *auditStore) Recent(ctx context.Context, limit int) ([]store.AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	var entries []store.AuditEntry
	err := s.db.NewSelect().Model(&entries).
		OrderExpr("created_at DESC").
		Limit(limit).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing audit log: %w", err)
	}
	return entries, nil
}

func (s *auditStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		res, err := s.db.NewRaw(
			"DELETE FROM audit_log WHERE rowid IN (SELECT a.rowid FROM audit_log a WHERE a.created_at < ? LIMIT 1000)",
			cutoff,
		).Exec(ctx)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning audit log: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}
