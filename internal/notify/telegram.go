// Package notify provides concrete notification delivery adapters (currently
// Telegram). Channels are configured at runtime via the settings store —
// no environment variables or restarts needed.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/httpclient"
)

// TelegramConfig holds the Telegram bot configuration.
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
	Enabled  bool   `json:"enabled"`
}

// telegramRequestTimeout bounds a single Bot API call.
const telegramRequestTimeout = 10 * time.Second

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
		client: httpclient.New(telegramRequestTimeout),
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
		// The error may embed the request URL (which contains the bot token).
		return fmt.Errorf("creating telegram request: %w", redactToken(err, cfg.BotToken))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		// A transport error is a *url.Error whose message embeds the full URL —
		// including https://api.telegram.org/bot<TOKEN>/sendMessage. Never let
		// the token reach a log or an API response.
		return fmt.Errorf("sending telegram message: %w", redactToken(err, cfg.BotToken))
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

// redactToken scrubs the bot token from an error message so it never leaks into
// logs or API responses. Transport failures surface as a *url.Error that embeds
// the request URL (https://api.telegram.org/bot<TOKEN>/sendMessage), so the raw
// error carries the secret. We flatten to a plain error with the token replaced
// by "<redacted>" (leaving the "bot" prefix, i.e. "bot<redacted>").
func redactToken(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, token) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, token, "<redacted>"))
}
