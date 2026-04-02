// Package notify provides concrete notification delivery adapters (currently
// Telegram). Channels are configured at runtime via the settings store —
// no environment variables or restarts needed.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TelegramConfig holds the Telegram bot configuration.
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
	Enabled  bool   `json:"enabled"`
}

// TelegramSender sends notifications to a Telegram chat.
type TelegramSender struct {
	config func() *TelegramConfig // lazy config loader (reads from settings store)
	client *http.Client
}

// NewTelegramSender creates a Telegram sender. The configFn is called on each
// send to get the latest config from the settings store — this means config
// changes take effect immediately without restart.
func NewTelegramSender(configFn func() *TelegramConfig) *TelegramSender {
	return &TelegramSender{
		config: configFn,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Send sends a formatted message to the configured Telegram chat.
// Returns nil if Telegram is not configured or disabled.
func (t *TelegramSender) Send(ctx context.Context, message string) error {
	cfg := t.config()
	if cfg == nil || !cfg.Enabled || cfg.BotToken == "" || cfg.ChatID == "" {
		return nil // not configured — silently skip
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)

	payload, _ := json.Marshal(map[string]any{
		"chat_id":    cfg.ChatID,
		"text":       message,
		"parse_mode": "HTML",
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending telegram message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("telegram API error %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// SendTest sends a test message to verify the configuration works.
func (t *TelegramSender) SendTest(ctx context.Context) error {
	cfg := t.config()
	if cfg == nil || cfg.BotToken == "" || cfg.ChatID == "" {
		return fmt.Errorf("telegram not configured: set bot_token and chat_id first")
	}

	msg := "✅ <b>OpenTrace</b> — Telegram notifications are working!\n\nYou'll receive alerts for:\n• Error spikes after deploys\n• New error groups (3+ occurrences)\n• Health check failures\n• Watch rule triggers"
	return t.Send(ctx, msg)
}

