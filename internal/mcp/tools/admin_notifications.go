package tools

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/notify"
	"github.com/adham90/opentrace/pkg/store"
)

// HandleNotifications manages notification channel configuration.
// Supports: telegram (future: slack, webhook, email).
//
// Usage:
//
//	admin(action: "notifications")                                       → list configured channels
//	admin(action: "notifications", params: {provider: "telegram", bot_token: "...", chat_id: "..."}) → configure
//	admin(action: "notifications", params: {provider: "telegram", test: true})                       → send test
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
	// Future providers:
	// case "slack":
	//     return handleSlackConfig(ctx, d.SettingsStore, args)
	// case "webhook":
	//     return handleWebhookConfig(ctx, d.SettingsStore, args)
	default:
		return NewToolResultError(fmt.Sprintf("unknown notification provider: %q. Supported: telegram", provider)), nil
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

	// Future: add Slack, webhook channels here

	resp := map[string]any{
		"channels": channels,
		"tip":      "Use provider=\"telegram\" with bot_token and chat_id to configure Telegram notifications.",
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
