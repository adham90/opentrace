package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/adham90/opentrace/internal/notify"
	"github.com/adham90/opentrace/pkg/store"
)

// HandleNotifications manages notification channel configuration.
// Supports: telegram, slack (future: webhook, email).
//
// Usage:
//
//	admin(action: "notifications")                                       → list configured channels
//	admin(action: "notifications", params: {provider: "telegram", bot_token: "...", chat_id: "..."}) → configure
//	admin(action: "notifications", params: {provider: "slack", webhook_url: "https://hooks.slack.com/services/..."}) → configure
//	admin(action: "notifications", params: {provider: "slack", test: true})                          → send test
//	admin(action: "notifications", params: {provider: "telegram", enabled: false})                   → disable
func HandleNotifications(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.SettingsStore == nil {
		return NewToolResultError("SettingsStore not configured"), nil
	}

	provider := ArgString(args, "provider")

	// No provider specified → list all channels
	if provider == "" {
		return listNotificationChannels(ctx, d.SettingsStore)
	}

	switch provider {
	case "telegram":
		return handleTelegramConfig(ctx, d.SettingsStore, args)
	case "slack":
		return handleSlackConfig(ctx, d.SettingsStore, args)
	// Future providers:
	// case "webhook":
	//     return handleWebhookConfig(ctx, d.SettingsStore, args)
	default:
		return NewToolResultError(fmt.Sprintf("unknown notification provider: %q. Supported: telegram, slack", provider)), nil
	}
}

func listNotificationChannels(ctx context.Context, ss store.SettingsStore) (*CallToolResult, error) {
	channels := []map[string]any{}

	// Telegram
	tgCfg, err := ss.GetTelegramConfig(ctx)
	if err == nil && tgCfg != nil {
		ch := map[string]any{
			"provider": "telegram",
			"enabled":  tgCfg.Enabled,
		}
		if tgCfg.ChatID != "" {
			ch["chat_id"] = tgCfg.ChatID
			ch["configured"] = true
		} else {
			ch["configured"] = false
		}
		channels = append(channels, ch)
	}

	// Slack
	slCfg, err := ss.GetSlackConfig(ctx)
	if err == nil && slCfg != nil {
		channels = append(channels, map[string]any{
			"provider":   "slack",
			"enabled":    slCfg.Enabled,
			"configured": slCfg.WebhookURL != "",
		})
	}

	// Future: add webhook channels here

	resp := map[string]any{
		"channels": channels,
		"tip":      "Use provider=\"telegram\" with bot_token and chat_id, or provider=\"slack\" with webhook_url, to configure notifications.",
	}

	if len(channels) == 0 {
		resp["message"] = "No notification channels configured."
	}

	return JSONResult(resp)
}

func handleTelegramConfig(ctx context.Context, ss store.SettingsStore, args map[string]any) (*CallToolResult, error) {
	// Test mode
	if ArgBool(args, "test") {
		cfg, err := ss.GetTelegramConfig(ctx)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to read telegram config: %v", err)), nil
		}
		if cfg.BotToken == "" || cfg.ChatID == "" {
			return NewToolResultError("Telegram not configured yet. Set bot_token and chat_id first."), nil
		}

		sender := notify.NewTelegramSender(func() *notify.TelegramConfig {
			return &notify.TelegramConfig{
				BotToken: cfg.BotToken,
				ChatID:   cfg.ChatID,
				Enabled:  true,
			}
		})
		if err := sender.SendTest(ctx); err != nil {
			return NewToolResultError(fmt.Sprintf("Telegram test failed: %v", err)), nil
		}
		return NewToolResultText("✅ Test message sent to Telegram. Check your chat!"), nil
	}

	// Get current config
	cfg, err := ss.GetTelegramConfig(ctx)
	if err != nil {
		cfg = &store.TelegramConfig{}
	}

	// Update fields
	changed := false
	if v := ArgString(args, "bot_token"); v != "" {
		cfg.BotToken = v
		changed = true
	}
	if v := ArgString(args, "chat_id"); v != "" {
		cfg.ChatID = v
		changed = true
	}

	// Handle enabled/disabled explicitly
	if _, ok := args["enabled"]; ok {
		cfg.Enabled = ArgBool(args, "enabled")
		changed = true
	} else if changed && cfg.BotToken != "" && cfg.ChatID != "" {
		// Auto-enable when both token and chat_id are set
		cfg.Enabled = true
	}

	if !changed {
		// Just show current status
		status := "not configured"
		if cfg.BotToken != "" && cfg.ChatID != "" {
			if cfg.Enabled {
				status = "enabled"
			} else {
				status = "disabled"
			}
		}
		return JSONResult(map[string]any{
			"provider":   "telegram",
			"status":     status,
			"configured": cfg.BotToken != "" && cfg.ChatID != "",
			"enabled":    cfg.Enabled,
			"tip":        "Set bot_token and chat_id to configure. Use test=true to verify.",
		})
	}

	// Save
	if err := ss.SetTelegramConfig(ctx, *cfg); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to save telegram config: %v", err)), nil
	}

	status := "configured and enabled"
	if !cfg.Enabled {
		status = "configured but disabled"
	}

	resp := map[string]any{
		"provider": "telegram",
		"status":   status,
		"enabled":  cfg.Enabled,
		"message":  fmt.Sprintf("Telegram notifications %s.", status),
	}

	if cfg.Enabled {
		resp["tip"] = "Use test=true to send a test message. Notifications will be sent for error spikes, new error groups, and health check failures."
	}

	return JSONResult(resp)
}

// handleSlackConfig configures the Slack incoming-webhook channel. The webhook
// URL is a bearer credential, so it is never echoed back in a response.
func handleSlackConfig(ctx context.Context, ss store.SettingsStore, args map[string]any) (*CallToolResult, error) {
	cfg, err := ss.GetSlackConfig(ctx)
	if err != nil || cfg == nil {
		cfg = &store.SlackConfig{}
	}

	// Test mode
	if ArgBool(args, "test") {
		if cfg.WebhookURL == "" {
			return NewToolResultError("Slack not configured yet. Set webhook_url first."), nil
		}
		sender := notify.NewSlackSender(func() *notify.SlackConfig {
			return &notify.SlackConfig{WebhookURL: cfg.WebhookURL, Enabled: true}
		})
		if err := sender.SendTest(ctx); err != nil {
			return NewToolResultError(fmt.Sprintf("Slack test failed: %v", err)), nil
		}
		return NewToolResultText("✅ Test message sent to Slack. Check your channel!"), nil
	}

	changed := false
	if v := ArgString(args, "webhook_url"); v != "" {
		if !strings.HasPrefix(v, "https://hooks.slack.com/") {
			return NewToolResultError("webhook_url must be a https://hooks.slack.com/... incoming webhook URL"), nil
		}
		cfg.WebhookURL = v
		changed = true
	}
	if _, ok := args["enabled"]; ok {
		cfg.Enabled = ArgBool(args, "enabled")
		changed = true
	} else if changed && cfg.WebhookURL != "" {
		// Auto-enable once a webhook URL is set.
		cfg.Enabled = true
	}

	if !changed {
		status := "not configured"
		if cfg.WebhookURL != "" {
			status = "disabled"
			if cfg.Enabled {
				status = "enabled"
			}
		}
		return JSONResult(map[string]any{
			"provider":   "slack",
			"status":     status,
			"configured": cfg.WebhookURL != "",
			"enabled":    cfg.Enabled,
			"tip":        "Create an incoming webhook at https://api.slack.com/messaging/webhooks, then set webhook_url. Use test=true to verify.",
		})
	}

	if err := ss.SetSlackConfig(ctx, *cfg); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to save slack config: %v", err)), nil
	}

	status := "configured and enabled"
	if !cfg.Enabled {
		status = "configured but disabled"
	}
	resp := map[string]any{
		"provider": "slack",
		"status":   status,
		"enabled":  cfg.Enabled,
		"message":  fmt.Sprintf("Slack notifications %s.", status),
	}
	if cfg.Enabled {
		resp["tip"] = "Use test=true to send a test message. Notifications will be sent for error spikes, new error groups, health check failures and watch rule triggers."
	}
	return JSONResult(resp)
}
