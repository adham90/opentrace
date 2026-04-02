package notify

import (
	"context"
	"testing"
)

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
