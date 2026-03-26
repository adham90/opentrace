package store

import (
	"time"

	"github.com/uptrace/bun"
)

// AuditEntry represents a single audit log entry.
type AuditEntry struct {
	bun.BaseModel `bun:"table:audit_log" json:"-"`
	ID            int64     `bun:"id,pk,autoincrement" json:"id"`
	UserID        string    `bun:"user_id" json:"user_id"`
	UserEmail     string    `bun:"user_email" json:"user_email"`
	Action        string    `bun:"action" json:"action"`
	TargetType    string    `bun:"target_type" json:"target_type,omitempty"`
	TargetID      string    `bun:"target_id" json:"target_id,omitempty"`
	Details       string    `bun:"details" json:"details,omitempty"`
	IPAddress     string    `bun:"ip_address" json:"ip_address,omitempty"`
	CreatedAt     time.Time `bun:"created_at" json:"created_at"`
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
