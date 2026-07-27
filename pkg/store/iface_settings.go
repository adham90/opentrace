package store

import "context"

// RetentionSettings holds data retention configuration.
type RetentionSettings struct {
	RetentionDays       int `json:"retention_days"`
	MetricRetentionDays int `json:"metric_retention_days"` // 0 = use global retention_days
}

// SettingsStore manages application settings stored in app_config.
type SettingsStore interface {
	GetRetention(ctx context.Context) (*RetentionSettings, error)
	SetRetention(ctx context.Context, settings RetentionSettings) error
	GetAPIKey(ctx context.Context) (string, error)
	SetAPIKey(ctx context.Context, key string) error
	GetCORSOrigins(ctx context.Context) (string, error)
	SetCORSOrigins(ctx context.Context, origins string) error
	GetMaxQueryRows(ctx context.Context) (int, error)
	SetMaxQueryRows(ctx context.Context, val int) error
	GetStatementTimeout(ctx context.Context) (int, error)
	SetStatementTimeout(ctx context.Context, val int) error
	GetMCPName(ctx context.Context) (string, error)
	SetMCPName(ctx context.Context, name string) error
	GetSamplingRules(ctx context.Context) ([]SamplingRule, error)
	SetSamplingRules(ctx context.Context, rules []SamplingRule) error
	GetTelegramConfig(ctx context.Context) (*TelegramConfig, error)
	SetTelegramConfig(ctx context.Context, cfg TelegramConfig) error
	GetSlackConfig(ctx context.Context) (*SlackConfig, error)
	SetSlackConfig(ctx context.Context, cfg SlackConfig) error
}

// TelegramConfig holds Telegram notification settings.
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
	Enabled  bool   `json:"enabled"`
}

// SlackConfig holds Slack incoming-webhook notification settings.
type SlackConfig struct {
	WebhookURL string `json:"webhook_url"`
	Enabled    bool   `json:"enabled"`
}
