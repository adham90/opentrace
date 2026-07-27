package mocks

import (
	"context"
	"sync"

	"github.com/adham90/opentrace/pkg/store"
)

// Compile-time interface check.
var _ store.SettingsStore = (*SettingsStore)(nil)

// SettingsStore is a thread-safe in-memory mock implementing store.SettingsStore.
type SettingsStore struct {
	mu               sync.Mutex
	Retention        *store.RetentionSettings
	APIKey           string
	CORSOrigins      string
	MaxQueryRows     int
	StatementTimeout int
	MCPName          string
	SamplingRules    []store.SamplingRule
	TelegramConfig   *store.TelegramConfig
	SlackConfig      *store.SlackConfig
}

// NewSettingsStore returns an initialised SettingsStore mock.
func NewSettingsStore() *SettingsStore {
	return &SettingsStore{}
}

func (m *SettingsStore) GetRetention(_ context.Context) (*store.RetentionSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Retention == nil {
		return &store.RetentionSettings{RetentionDays: 30}, nil
	}
	return m.Retention, nil
}

func (m *SettingsStore) SetRetention(_ context.Context, settings store.RetentionSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Retention = &settings
	return nil
}

func (m *SettingsStore) GetAPIKey(_ context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.APIKey, nil
}

func (m *SettingsStore) SetAPIKey(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.APIKey = key
	return nil
}

func (m *SettingsStore) GetCORSOrigins(_ context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.CORSOrigins, nil
}

func (m *SettingsStore) SetCORSOrigins(_ context.Context, origins string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CORSOrigins = origins
	return nil
}

func (m *SettingsStore) GetMaxQueryRows(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.MaxQueryRows, nil
}

func (m *SettingsStore) SetMaxQueryRows(_ context.Context, val int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.MaxQueryRows = val
	return nil
}

func (m *SettingsStore) GetStatementTimeout(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.StatementTimeout, nil
}

func (m *SettingsStore) SetStatementTimeout(_ context.Context, val int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StatementTimeout = val
	return nil
}

func (m *SettingsStore) GetMCPName(_ context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.MCPName, nil
}

func (m *SettingsStore) SetMCPName(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.MCPName = name
	return nil
}

func (m *SettingsStore) GetSamplingRules(_ context.Context) ([]store.SamplingRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.SamplingRules, nil
}

func (m *SettingsStore) SetSamplingRules(_ context.Context, rules []store.SamplingRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SamplingRules = rules
	return nil
}

func (m *SettingsStore) GetTelegramConfig(_ context.Context) (*store.TelegramConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.TelegramConfig == nil {
		return &store.TelegramConfig{}, nil
	}
	return m.TelegramConfig, nil
}

func (m *SettingsStore) SetTelegramConfig(_ context.Context, cfg store.TelegramConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TelegramConfig = &cfg
	return nil
}

func (m *SettingsStore) GetSlackConfig(_ context.Context) (*store.SlackConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.SlackConfig == nil {
		return &store.SlackConfig{}, nil
	}
	return m.SlackConfig, nil
}

func (m *SettingsStore) SetSlackConfig(_ context.Context, cfg store.SlackConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SlackConfig = &cfg
	return nil
}
