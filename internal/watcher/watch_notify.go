package watcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/httpclient"
	"github.com/adham90/opentrace/internal/retry"
	"github.com/adham90/opentrace/pkg/store"
)

// WatchAlertNotifier sends notifications for watch alerts.
type WatchAlertNotifier interface {
	// NotifyWatchAlert sends a notification for a watch alert.
	// Implementations must be safe for concurrent use.
	NotifyWatchAlert(ctx context.Context, alert *store.WatchAlert, watch *store.Watch) error
}

// WatchWebhookNotifier sends watch alerts to a webhook URL.
type WatchWebhookNotifier struct {
	URL    string
	Client *http.Client
}

// WatchWebhookPayload is the JSON body sent for watch alerts.
type WatchWebhookPayload struct {
	AlertID        string  `json:"alert_id"`
	WatchID        string  `json:"watch_id"`
	Metric         string  `json:"metric"`
	Urgency        string  `json:"urgency"`
	Summary        string  `json:"summary"`
	TriggerValue   float64 `json:"trigger_value"`
	ThresholdValue float64 `json:"threshold_value"`
	Service        string  `json:"service,omitempty"`
	Timestamp      string  `json:"timestamp"`
}

// NewWatchWebhookNotifier creates a new webhook notifier for watch alerts.
func NewWatchWebhookNotifier(url string) *WatchWebhookNotifier {
	return &WatchWebhookNotifier{
		URL:    url,
		Client: httpclient.New(10 * time.Second),
	}
}

func (n *WatchWebhookNotifier) NotifyWatchAlert(ctx context.Context, alert *store.WatchAlert, watch *store.Watch) error {
	payload := WatchWebhookPayload{
		AlertID:        alert.ID,
		WatchID:        alert.WatchID,
		Metric:         alert.TriggerMetric,
		Urgency:        string(alert.Urgency),
		Summary:        alert.Summary,
		TriggerValue:   alert.TriggerValue,
		ThresholdValue: alert.ThresholdValue,
		Service:        string(watch.Service),
		Timestamp:      alert.CreatedAt.Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling watch webhook payload: %w", err)
	}

	return retry.Do(ctx, retry.Config{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    4 * time.Second,
	}, func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("creating watch webhook request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-OpenTrace-Event", "watch.alert")

		resp, err := n.Client.Do(req)
		if err != nil {
			return fmt.Errorf("sending watch webhook: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 500 {
			// 5xx errors are transient — retry
			return fmt.Errorf("watch webhook returned status %d", resp.StatusCode)
		}
		if resp.StatusCode >= 400 {
			// 4xx errors are not retryable (bad request, auth failure, etc.)
			return retry.Permanent(fmt.Errorf("watch webhook returned status %d", resp.StatusCode))
		}
		return nil
	})
}

// WatchLogNotifier logs watch alerts to slog (always-on fallback).
type WatchLogNotifier struct{}

func (n *WatchLogNotifier) NotifyWatchAlert(_ context.Context, alert *store.WatchAlert, watch *store.Watch) error {
	slog.Info("watch alert fired",
		"alert_id", alert.ID,
		"watch_id", alert.WatchID,
		"metric", alert.TriggerMetric,
		"value", alert.TriggerValue,
		"threshold", alert.ThresholdValue,
		"urgency", alert.Urgency,
		"service", watch.Service,
		"summary", alert.Summary,
	)
	return nil
}

// NotifyAllWatchAlert sends a watch alert to all notifiers concurrently.
// Errors are logged but do not block.
func NotifyAllWatchAlert(ctx context.Context, notifiers []WatchAlertNotifier, alert *store.WatchAlert, watch *store.Watch) {
	var wg sync.WaitGroup
	for _, n := range notifiers {
		wg.Add(1)
		go func(n WatchAlertNotifier) {
			defer wg.Done()
			nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := n.NotifyWatchAlert(nctx, alert, watch); err != nil {
				slog.Warn("watch notification error", "error", err)
			}
		}(n)
	}
	wg.Wait()
}
