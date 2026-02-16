package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// settingsStore implements SettingsStore using database/sql (SQLite).
type settingsStore struct {
	db *sql.DB
}

// NewSettingsStore creates a new SettingsStore backed by SQLite.
func NewSettingsStore(db *sql.DB) SettingsStore {
	return &settingsStore{db: db}
}

const retentionKey = "retention"
const apiKeyKey = "api_key"
const autoUpdateKey = "auto_update"
const corsOriginsKey = "cors_origins"

func (s *settingsStore) GetRetention(ctx context.Context) (*RetentionSettings, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM app_config WHERE key = ?`, retentionKey,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return &RetentionSettings{RetentionDays: 30}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying retention setting: %w", err)
	}

	var settings RetentionSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return &RetentionSettings{RetentionDays: 30}, nil
	}
	return &settings, nil
}

func (s *settingsStore) SetRetention(ctx context.Context, settings RetentionSettings) error {
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
	if err == sql.ErrNoRows {
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
	if err == sql.ErrNoRows {
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
	if err == sql.ErrNoRows {
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

