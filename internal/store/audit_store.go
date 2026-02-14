package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AuditEntry represents a single audit log entry.
type AuditEntry struct {
	ID         int64     `json:"id"`
	UserID     string    `json:"user_id"`
	UserEmail  string    `json:"user_email"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type,omitempty"`
	TargetID   string    `json:"target_id,omitempty"`
	Details    string    `json:"details,omitempty"`
	IPAddress  string    `json:"ip_address,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// LogAuditParams are the parameters for creating an audit log entry.
type LogAuditParams struct {
	UserID     string
	UserEmail  string
	Action     string // e.g. "user.create", "connector.delete", "settings.update"
	TargetType string // e.g. "user", "connector", "watcher"
	TargetID   string
	Details    string // JSON or free-text details
	IPAddress  string
}

type auditStore struct {
	db *sql.DB
}

// NewAuditStore creates a new AuditStore backed by SQLite.
func NewAuditStore(db *sql.DB) AuditStore {
	return &auditStore{db: db}
}

func (s *auditStore) Log(ctx context.Context, params LogAuditParams) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, user_email, action, target_type, target_id, details, ip_address)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		params.UserID, params.UserEmail, params.Action,
		params.TargetType, params.TargetID, params.Details, params.IPAddress,
	)
	if err != nil {
		return fmt.Errorf("logging audit entry: %w", err)
	}
	return nil
}

func (s *auditStore) Recent(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, user_email, action, COALESCE(target_type, ''),
		        COALESCE(target_id, ''), COALESCE(details, ''),
		        COALESCE(ip_address, ''), created_at
		 FROM audit_log
		 ORDER BY created_at DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing audit log: %w", err)
	}
	defer rows.Close()

	var result []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var createdAt string
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.UserEmail, &e.Action,
			&e.TargetType, &e.TargetID, &e.Details,
			&e.IPAddress, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning audit entry: %w", err)
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		result = append(result, e)
	}

	return result, rows.Err()
}

func (s *auditStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var totalDeleted int64
	for {
		result, err := s.db.ExecContext(ctx,
			`DELETE FROM audit_log WHERE rowid IN (SELECT rowid FROM audit_log WHERE created_at < ? LIMIT 1000)`, cutoff,
		)
		if err != nil {
			return totalDeleted, fmt.Errorf("pruning audit log: %w", err)
		}
		n, _ := result.RowsAffected()
		totalDeleted += n
		if n < 1000 {
			break
		}
	}
	return totalDeleted, nil
}
