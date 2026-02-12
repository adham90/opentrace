package watcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// Notifier sends alert notifications.
type Notifier interface {
	Send(ctx context.Context, alert store.Alert) error
}

// DashboardNotifier is a no-op — the alert is already stored in the DB.
type DashboardNotifier struct{}

func (d *DashboardNotifier) Send(ctx context.Context, alert store.Alert) error {
	return nil
}

// WebhookNotifier sends a POST request to a configured URL.
type WebhookNotifier struct {
	URL    string
	Client *http.Client
}

// WebhookPayload is the JSON body sent to webhook endpoints.
type WebhookPayload struct {
	AlertID      string `json:"alert_id"`
	WatcherTitle string `json:"watcher_title"`
	Severity     string `json:"severity"`
	Summary      string `json:"summary"`
	Timestamp    string `json:"timestamp"`
}

func NewWebhookNotifier(url string) *WebhookNotifier {
	return &WebhookNotifier{
		URL: url,
		Client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (w *WebhookNotifier) Send(ctx context.Context, alert store.Alert) error {
	payload := WebhookPayload{
		AlertID:      alert.ID.String(),
		WatcherTitle: alert.Title,
		Severity:     string(alert.Severity),
		Summary:      alert.Summary,
		Timestamp:    alert.CreatedAt.Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.Client.Do(req)
	if err != nil {
		return fmt.Errorf("sending webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}

// notifyConfig represents a single entry in the watcher's notify array.
// It can be a string ("dashboard") or an object ({"type":"webhook","url":"..."}).
type notifyConfig struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// ParseNotifiers parses the watcher's notify JSON config into a list of Notifiers.
func ParseNotifiers(raw json.RawMessage) []Notifier {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		// Default to dashboard only
		return []Notifier{&DashboardNotifier{}}
	}

	var notifiers []Notifier
	for _, item := range items {
		// Try as string first
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			if s == "dashboard" {
				notifiers = append(notifiers, &DashboardNotifier{})
			}
			continue
		}

		// Try as object
		var cfg notifyConfig
		if err := json.Unmarshal(item, &cfg); err == nil {
			if cfg.Type == "webhook" && cfg.URL != "" {
				notifiers = append(notifiers, NewWebhookNotifier(cfg.URL))
			}
		}
	}

	if len(notifiers) == 0 {
		notifiers = append(notifiers, &DashboardNotifier{})
	}

	return notifiers
}

// SendAll dispatches an alert to all configured notifiers asynchronously.
// Each notifier runs in its own goroutine with a 30-second timeout.
// Errors are logged but do not block the caller.
func SendAll(ctx context.Context, notifiers []Notifier, alert store.Alert) {
	for _, n := range notifiers {
		go func(n Notifier) {
			sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := n.Send(sendCtx, alert); err != nil {
				slog.Warn("notification error", "error", err)
			}
		}(n)
	}
}
