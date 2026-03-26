package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/adham90/opentrace/pkg/store"
)

// settingsStore implements SettingsStore using database/sql (SQLite).
type settingsStore struct {
	db *sql.DB
}

// NewSettingsStore creates a new SettingsStore backed by SQLite.
func NewSettingsStore(db *sql.DB) store.SettingsStore {
	return &settingsStore{db: db}
}

const retentionKey = "retention"
const apiKeyKey = "api_key"
const autoUpdateKey = "auto_update"
const corsOriginsKey = "cors_origins"
const maxQueryRowsKey = "max_query_rows"
const statementTimeoutKey = "statement_timeout"
const mcpNameKey = "mcp_name"
const samplingRulesKey = "sampling_rules"

func (s *settingsStore) GetRetention(ctx context.Context) (*store.RetentionSettings, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM app_config WHERE key = ?`, retentionKey,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return &store.RetentionSettings{RetentionDays: 30}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying retention setting: %w", err)
	}

	var settings store.RetentionSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return &store.RetentionSettings{RetentionDays: 30}, nil
	}
	return &settings, nil
}

func (s *settingsStore) SetRetention(ctx context.Context, settings store.RetentionSettings) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshaling retention setting: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO app_config (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		retentionKey, string(raw),
	)
	if err != nil {
		return fmt.Errorf("upserting retention setting: %w", err)
	}
	return nil
}

func (s *settingsStore) GetAPIKey(ctx context.Context) (string, error) {
	var val string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM app_config WHERE key = ?`, apiKeyKey,
	).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("querying api key: %w", err)
	}
	return val, nil
}

func (s *settingsStore) SetAPIKey(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO app_config (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		apiKeyKey, key,
	)
	if err != nil {
		return fmt.Errorf("upserting api key: %w", err)
	}
	return nil
}

func (s *settingsStore) GetAutoUpdate(ctx context.Context) (bool, error) {
	var val string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM app_config WHERE key = ?`, autoUpdateKey,
	).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("querying auto_update: %w", err)
	}
	return val == "1", nil
}

func (s *settingsStore) SetAutoUpdate(ctx context.Context, enabled bool) error {
	val := "0"
	if enabled {
		val = "1"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO app_config (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		autoUpdateKey, val,
	)
	if err != nil {
		return fmt.Errorf("upserting auto_update: %w", err)
	}
	return nil
}

func (s *settingsStore) GetCORSOrigins(ctx context.Context) (string, error) {
	var val string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM app_config WHERE key = ?`, corsOriginsKey,
	).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("querying cors_origins: %w", err)
	}
	return val, nil
}

func (s *settingsStore) SetCORSOrigins(ctx context.Context, origins string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO app_config (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		corsOriginsKey, origins,
	)
	if err != nil {
		return fmt.Errorf("upserting cors_origins: %w", err)
	}
	return nil
}

func (s *settingsStore) GetMaxQueryRows(ctx context.Context) (int, error) {
	return s.getIntSetting(ctx, maxQueryRowsKey, 0)
}

func (s *settingsStore) SetMaxQueryRows(ctx context.Context, val int) error {
	return s.setIntSetting(ctx, maxQueryRowsKey, val)
}

func (s *settingsStore) GetStatementTimeout(ctx context.Context) (int, error) {
	return s.getIntSetting(ctx, statementTimeoutKey, 0)
}

func (s *settingsStore) SetStatementTimeout(ctx context.Context, val int) error {
	return s.setIntSetting(ctx, statementTimeoutKey, val)
}

func (s *settingsStore) GetMCPName(ctx context.Context) (string, error) {
	var val string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM app_config WHERE key = ?`, mcpNameKey,
	).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("querying mcp_name: %w", err)
	}
	return val, nil
}

func (s *settingsStore) SetMCPName(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO app_config (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		mcpNameKey, name,
	)
	if err != nil {
		return fmt.Errorf("upserting mcp_name: %w", err)
	}
	return nil
}

// getIntSetting reads an integer from app_config, returning defaultVal if missing.
func (s *settingsStore) getIntSetting(ctx context.Context, key string, defaultVal int) (int, error) {
	var val string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM app_config WHERE key = ?`, key,
	).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultVal, nil
	}
	if err != nil {
		return 0, fmt.Errorf("querying %s: %w", key, err)
	}
	var n int
	if _, err := fmt.Sscanf(val, "%d", &n); err != nil {
		return defaultVal, nil
	}
	return n, nil
}

// setIntSetting writes an integer to app_config.
func (s *settingsStore) setIntSetting(ctx context.Context, key string, val int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO app_config (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, fmt.Sprintf("%d", val),
	)
	if err != nil {
		return fmt.Errorf("upserting %s: %w", key, err)
	}
	return nil
}

func (s *settingsStore) GetSamplingRules(ctx context.Context) ([]store.SamplingRule, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM app_config WHERE key = ?`, samplingRulesKey,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || raw == "" {
		return nil, nil // no sampling configured
	}
	if err != nil {
		return nil, fmt.Errorf("querying sampling_rules: %w", err)
	}
	var rules []store.SamplingRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, fmt.Errorf("unmarshaling sampling_rules: %w", err)
	}
	return rules, nil
}

func (s *settingsStore) SetSamplingRules(ctx context.Context, rules []store.SamplingRule) error {
	raw, err := json.Marshal(rules)
	if err != nil {
		return fmt.Errorf("marshaling sampling_rules: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO app_config (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		samplingRulesKey, string(raw),
	)
	if err != nil {
		return fmt.Errorf("upserting sampling_rules: %w", err)
	}
	return nil
}

