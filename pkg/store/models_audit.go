package store

import "time"

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
