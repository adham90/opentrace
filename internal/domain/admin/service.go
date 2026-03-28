package admin

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// Service provides application settings management and audit logging.
type Service struct {
	settings SettingsRepository
	audit    AuditRepository
}

// NewService creates an admin service.
func NewService(settings SettingsRepository, audit AuditRepository) *Service {
	return &Service{settings: settings, audit: audit}
}

// Settings returns the current application settings.
func (s *Service) Settings(ctx context.Context) (*store.RetentionSettings, error) {
	if s.settings == nil {
		return nil, fmt.Errorf("admin settings: %w", domain.ErrNotConfigured)
	}
	ret, err := s.settings.GetRetention(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting retention settings: %w", err)
	}
	return ret, nil
}

// UpdateRetention updates the data retention policy.
func (s *Service) UpdateRetention(ctx context.Context, settings store.RetentionSettings) error {
	if s.settings == nil {
		return fmt.Errorf("admin update retention: %w", domain.ErrNotConfigured)
	}
	if settings.RetentionDays < 1 {
		return fmt.Errorf("retention_days must be >= 1: %w", domain.ErrInvalidInput)
	}
	return s.settings.SetRetention(ctx, settings)
}

// MCPName returns the configured MCP server name.
func (s *Service) MCPName(ctx context.Context) (string, error) {
	if s.settings == nil {
		return "", fmt.Errorf("admin mcp name: %w", domain.ErrNotConfigured)
	}
	return s.settings.GetMCPName(ctx)
}

// SetMCPName updates the MCP server name.
func (s *Service) SetMCPName(ctx context.Context, name string) error {
	if s.settings == nil {
		return fmt.Errorf("admin set mcp name: %w", domain.ErrNotConfigured)
	}
	if name == "" {
		return fmt.Errorf("name: %w", domain.ErrInvalidInput)
	}
	return s.settings.SetMCPName(ctx, name)
}

// Audit returns recent audit log entries.
func (s *Service) Audit(ctx context.Context, limit int) ([]store.AuditEntry, error) {
	if s.audit == nil {
		return nil, fmt.Errorf("admin audit: %w", domain.ErrNotConfigured)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	return s.audit.Recent(ctx, limit)
}

// LogAudit records an admin action in the audit trail.
func (s *Service) LogAudit(ctx context.Context, params store.LogAuditParams) error {
	if s.audit == nil {
		return fmt.Errorf("admin log audit: %w", domain.ErrNotConfigured)
	}
	return s.audit.Log(ctx, params)
}
