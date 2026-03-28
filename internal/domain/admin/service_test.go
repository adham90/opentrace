package admin

import (
	"context"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// fakeSettings implements SettingsRepository for testing.
type fakeSettings struct {
	retention *store.RetentionSettings
	mcpName   string
	err       error
}

func (f *fakeSettings) GetRetention(_ context.Context) (*store.RetentionSettings, error) {
	return f.retention, f.err
}

func (f *fakeSettings) SetRetention(_ context.Context, _ store.RetentionSettings) error {
	return f.err
}

func (f *fakeSettings) GetAPIKey(_ context.Context) (string, error)       { return "", f.err }
func (f *fakeSettings) SetAPIKey(_ context.Context, _ string) error       { return f.err }
func (f *fakeSettings) GetMCPName(_ context.Context) (string, error)      { return f.mcpName, f.err }
func (f *fakeSettings) SetMCPName(_ context.Context, _ string) error      { return f.err }
func (f *fakeSettings) GetMaxQueryRows(_ context.Context) (int, error)    { return 0, f.err }
func (f *fakeSettings) SetMaxQueryRows(_ context.Context, _ int) error    { return f.err }
func (f *fakeSettings) GetStatementTimeout(_ context.Context) (int, error) { return 0, f.err }
func (f *fakeSettings) SetStatementTimeout(_ context.Context, _ int) error { return f.err }

// Compile-time check.
var _ SettingsRepository = (*fakeSettings)(nil)

// fakeAudit implements AuditRepository for testing.
type fakeAudit struct {
	entries []store.AuditEntry
	err     error
}

func (f *fakeAudit) Log(_ context.Context, _ store.LogAuditParams) error {
	return f.err
}

func (f *fakeAudit) Recent(_ context.Context, _ int) ([]store.AuditEntry, error) {
	return f.entries, f.err
}

// Compile-time check.
var _ AuditRepository = (*fakeAudit)(nil)

// --- Settings ---

func TestSettings_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Settings(context.Background())
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestSettings_Success(t *testing.T) {
	f := &fakeSettings{retention: &store.RetentionSettings{RetentionDays: 30}}
	svc := NewService(f, nil)
	ret, err := svc.Settings(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ret.RetentionDays != 30 {
		t.Errorf("retention_days = %d, want 30", ret.RetentionDays)
	}
}

// --- UpdateRetention ---

func TestUpdateRetention_NilRepo(t *testing.T) {
	svc := &Service{}
	err := svc.UpdateRetention(context.Background(), store.RetentionSettings{RetentionDays: 30})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestUpdateRetention_InvalidDays(t *testing.T) {
	svc := NewService(&fakeSettings{}, nil)
	err := svc.UpdateRetention(context.Background(), store.RetentionSettings{RetentionDays: 0})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestUpdateRetention_Success(t *testing.T) {
	svc := NewService(&fakeSettings{}, nil)
	err := svc.UpdateRetention(context.Background(), store.RetentionSettings{RetentionDays: 7})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Audit ---

func TestAudit_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Audit(context.Background(), 10)
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestAudit_Success(t *testing.T) {
	f := &fakeAudit{entries: []store.AuditEntry{{Action: "user.create"}}}
	svc := NewService(nil, f)
	entries, err := svc.Audit(context.Background(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("len = %d, want 1", len(entries))
	}
}

// --- MCPName ---

func TestMCPName_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.MCPName(context.Background())
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestSetMCPName_Empty(t *testing.T) {
	svc := NewService(&fakeSettings{}, nil)
	err := svc.SetMCPName(context.Background(), "")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}
