package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/httpclient"
)

// SlackConfig holds the Slack incoming-webhook configuration.
//
// ponytail: incoming webhooks only — no OAuth app, no bot token, no scopes.
// Upgrade to chat.postMessage if we ever need threads, reactions or per-alert
// channel routing.
type SlackConfig struct {
	WebhookURL string `json:"webhook_url"`
	Enabled    bool   `json:"enabled"`
}

// slackRequestTimeout bounds a single webhook delivery.
const slackRequestTimeout = 10 * time.Second

// SlackSender posts notifications to a Slack incoming webhook.
type SlackSender struct {
	config func() *SlackConfig // lazy config loader (reads from settings store)
	client *http.Client
}

// NewSlackSender creates a Slack sender. The configFn is called on each send to
// get the latest config from the settings store — config changes take effect
// immediately without restart.
func NewSlackSender(configFn func() *SlackConfig) *SlackSender {
	return &SlackSender{
		config: configFn,
		client: httpclient.New(slackRequestTimeout),
	}
}

// Send posts a message to the configured Slack webhook. The message is the same
// HTML-flavoured text the Telegram channel gets; it is converted to Slack
// mrkdwn here so callers keep one formatter. Returns nil if Slack is not
// configured or disabled.
func (s *SlackSender) Send(ctx context.Context, message string) error {
	cfg := s.config()
	if cfg == nil || !cfg.Enabled || cfg.WebhookURL == "" {
		return nil // not configured — silently skip
	}

	payload, _ := json.Marshal(map[string]any{"text": htmlToMrkdwn(message)})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		// The error embeds the request URL, and a Slack webhook URL *is* the secret.
		return fmt.Errorf("creating slack request: %w", redactURL(err, cfg.WebhookURL))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending slack message: %w", redactURL(err, cfg.WebhookURL))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("slack API error %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// SendTest posts a test message to verify the configuration works.
func (s *SlackSender) SendTest(ctx context.Context) error {
	cfg := s.config()
	if cfg == nil || cfg.WebhookURL == "" {
		return errors.New("slack not configured: set webhook_url first")
	}
	msg := "✅ <b>OpenTrace</b> — Slack notifications are working!\n\nYou'll receive alerts for:\n• Error spikes after deploys\n• New error groups (3+ occurrences)\n• Health check failures\n• Watch rule triggers"
	return s.Send(ctx, msg)
}

// htmlTag matches any remaining HTML tag after bold/italic have been rewritten.
var htmlTag = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)

var mrkdwnReplacer = strings.NewReplacer(
	"<b>", "*", "</b>", "*",
	"<i>", "_", "</i>", "_",
	"<code>", "`", "</code>", "`",
)

// htmlToMrkdwn converts the small HTML subset used by the alert formatters into
// Slack mrkdwn, so both channels share one message template. Unknown tags are
// stripped and HTML entities decoded — Slack escapes &, < and > itself.
func htmlToMrkdwn(msg string) string {
	msg = mrkdwnReplacer.Replace(msg)
	msg = htmlTag.ReplaceAllString(msg, "")
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(msg)
}

// redactURL scrubs the webhook URL from an error so it never reaches a log or
// an API response — for Slack incoming webhooks the URL is the credential.
func redactURL(err error, url string) error {
	if err == nil || url == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, url) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, url, "<redacted>"))
}
