package notify

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// errRoundTripper always fails the request without touching the network. The
// failure surfaces through http.Client.Do as a *url.Error whose message embeds
// the request URL — which is exactly how the bot token would leak.
type errRoundTripper struct{}

func (errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: connect: connection refused")
}

func TestTelegramSender_TokenRedactedInError(t *testing.T) {
	const secret = "123456789:AAExampleSuperSecretBotToken"

	sender := NewTelegramSender(func() *TelegramConfig {
		return &TelegramConfig{BotToken: secret, ChatID: "42", Enabled: true}
	})
	// Force a transport error without any real network call.
	sender.client = &http.Client{Transport: errRoundTripper{}}

	err := sender.Send(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected a transport error, got nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("bot token leaked in error: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "bot<redacted>") {
		t.Errorf("expected redacted token marker %q in error, got %q", "bot<redacted>", err.Error())
	}
}

func TestRedactToken(t *testing.T) {
	const token = "999:SECRET"
	in := errors.New(`Post "https://api.telegram.org/bot999:SECRET/sendMessage": boom`)
	out := redactToken(in, token)
	if strings.Contains(out.Error(), token) {
		t.Fatalf("token not redacted: %q", out.Error())
	}
	if !strings.Contains(out.Error(), "bot<redacted>") {
		t.Errorf("expected bot<redacted>, got %q", out.Error())
	}
	// Nil and empty-token inputs are passed through unchanged.
	if redactToken(nil, token) != nil {
		t.Error("expected nil error to pass through")
	}
	sentinel := errors.New("x")
	if redactToken(sentinel, "") != sentinel {
		t.Error("expected empty-token to return original error unchanged")
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
