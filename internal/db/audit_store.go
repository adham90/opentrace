package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type auditStore struct {
	db *sql.DB
	q  *Queries
}

// NewAuditStore creates a new AuditStore backed by SQLite.
func NewAuditStore(db *sql.DB) store.AuditStore {
	return &auditStore{db: db, q: New(db)}
}

func (s *auditStore) Log(ctx context.Context, params store.LogAuditParams) error {
	err := s.q.InsertAuditLog(ctx, InsertAuditLogParams{
		UserID:     params.UserID,
		UserEmail:  params.UserEmail,
		Action:     params.Action,
		TargetType: sql.NullString{String: params.TargetType, Valid: params.TargetType != ""},
		TargetID:   sql.NullString{String: params.TargetID, Valid: params.TargetID != ""},
		Details:    sql.NullString{String: params.Details, Valid: params.Details != ""},
		IpAddress:  sql.NullString{String: params.IPAddress, Valid: params.IPAddress != ""},
	})
	if err != nil {
		return fmt.Errorf("logging audit entry: %w", err)
	}
	return nil
}

func (s *auditStore) Recent(ctx context.Context, limit int) ([]store.AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.q.ListRecentAuditLogs(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("listing audit log: %w", err)
	}
	return toStoreAuditEntries(rows), nil
}

func (s *auditStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		n, err := s.q.PruneAuditLogs(ctx, cutoff)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning audit log: %w", err)
		}
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}
