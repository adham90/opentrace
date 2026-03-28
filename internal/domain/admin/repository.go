// Package admin provides the domain service for application settings and
// audit logging. The repository interfaces define what the service needs
// from the data layer.
package admin

import (
	"context"

	"github.com/adham90/opentrace/pkg/store"
)

// SettingsRepository defines read/write access for application settings.
// Implemented by internal/db.SettingsStore (SQLite adapter).
type SettingsRepository interface {
	GetRetention(ctx context.Context) (*store.RetentionSettings, error)
	SetRetention(ctx context.Context, settings store.RetentionSettings) error
	GetAPIKey(ctx context.Context) (string, error)
	SetAPIKey(ctx context.Context, key string) error
	GetMCPName(ctx context.Context) (string, error)
	SetMCPName(ctx context.Context, name string) error
	GetMaxQueryRows(ctx context.Context) (int, error)
	SetMaxQueryRows(ctx context.Context, val int) error
	GetStatementTimeout(ctx context.Context) (int, error)
	SetStatementTimeout(ctx context.Context, val int) error
}

// AuditRepository defines access to the admin audit trail.
// Implemented by internal/db.AuditStore (SQLite adapter).
type AuditRepository interface {
	Log(ctx context.Context, params store.LogAuditParams) error
	Recent(ctx context.Context, limit int) ([]store.AuditEntry, error)
}
