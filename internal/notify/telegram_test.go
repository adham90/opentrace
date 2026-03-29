package notify

import (
	"context"
	"testing"
)

func TestFormatNotification_Critical(t *testing.T) {
	msg := FormatNotification("error_spike", "critical", "Error spike in API", "Errors up 150%", map[string]any{
		"service":          "api",
		"suggested_action": "Check deploy logs",
	})
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
	// Should have red circle emoji for critical
	if msg[:4] != "🔴" {
		t.Errorf("expected red circle emoji for critical, got %q", msg[:4])
	}
	// Should contain title
	if !contains(msg, "Error spike in API") {
		t.Error("expected title in message")
	}
	// Should contain service
	if !contains(msg, "api") {
		t.Error("expected service in message")
	}
	// Should contain suggested action
	if !contains(msg, "Check deploy logs") {
		t.Error("expected suggested action in message")
	}
}

func TestFormatNotification_Error(t *testing.T) {
	msg := FormatNotification("new_error", "error", "New error", "RuntimeError", nil)
	if msg[:4] != "🟠" {
		t.Errorf("expected orange circle for error, got %q", msg[:4])
	}
}

func TestFormatNotification_Info(t *testing.T) {
	msg := FormatNotification("deploy", "info", "Deploy complete", "v1.2.3", nil)
	if msg[:4] != "🔵" {
		t.Errorf("expected blue circle for info, got %q", msg[:4])
	}
}

func TestTelegramSender_NilConfig(t *testing.T) {
	sender := NewTelegramSender(func() *TelegramConfig { return nil })
	// Should not error — just skip
	err := sender.Send(context.Background(), "test")
	if err != nil {
		t.Errorf("expected nil error for nil config, got %v", err)
	}
}

func TestTelegramSender_Disabled(t *testing.T) {
	sender := NewTelegramSender(func() *TelegramConfig {
		return &TelegramConfig{BotToken: "123", ChatID: "456", Enabled: false}
	})
	err := sender.Send(context.Background(), "test")
	if err != nil {
		t.Errorf("expected nil error for disabled, got %v", err)
	}
}

func TestTelegramSender_EmptyToken(t *testing.T) {
	sender := NewTelegramSender(func() *TelegramConfig {
		return &TelegramConfig{ChatID: "456", Enabled: true}
	})
	err := sender.Send(context.Background(), "test")
	if err != nil {
		t.Errorf("expected nil error for empty token, got %v", err)
	}
}

func TestTelegramSender_SendTest_NotConfigured(t *testing.T) {
	sender := NewTelegramSender(func() *TelegramConfig {
		return &TelegramConfig{}
	})
	err := sender.SendTest(context.Background())
	if err == nil {
		t.Error("expected error for unconfigured sender")
	}
}

func TestNotificationBridge_NilTelegram(t *testing.T) {
	bridge := NewNotificationBridge(nil)
	// Should not panic
	bridge.SendNotificationToAllClients("test", map[string]any{"title": "test"})
}

func TestNotificationBridge_FormatAndSend(t *testing.T) {
	var sentMsg string
	sender := NewTelegramSender(func() *TelegramConfig {
		return &TelegramConfig{Enabled: false} // disabled — won't actually send
	})
	bridge := NewNotificationBridge(sender)

	// Verify it doesn't panic
	bridge.SendNotificationToAllClients("notifications/opentrace/alert", map[string]any{
		"type":     "new_error_group",
		"severity": "error",
		"title":    "New error: RuntimeError",
		"summary":  "3 occurrences in 2 minutes",
	})
	_ = sentMsg
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
