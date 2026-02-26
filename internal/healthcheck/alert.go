package healthcheck

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
	"github.com/adham90/opentrace/internal/store"
)

// HealthCheckAlertNotifier sends notifications for health check status changes.
type HealthCheckAlertNotifier interface {
	NotifyHealthCheckAlert(ctx context.Context, alert *HealthCheckAlert) error
}

// HealthCheckAlert represents a health check status change event.
type HealthCheckAlert struct {
	HealthCheckID   string                  `json:"healthcheck_id"`
	HealthCheckName string                  `json:"healthcheck_name"`
	URL             string                  `json:"url"`
	PreviousStatus  store.HealthCheckStatus `json:"previous_status"`
	CurrentStatus   store.HealthCheckStatus `json:"current_status"`
	StatusCode      *int                    `json:"status_code,omitempty"`
	ResponseMs      *int                    `json:"response_ms,omitempty"`
	ErrorMessage    string                  `json:"error,omitempty"`
	Timestamp       time.Time               `json:"timestamp"`
}

// HealthCheckLogNotifier logs health check alerts to slog (always-on fallback).
type HealthCheckLogNotifier struct{}

func (n *HealthCheckLogNotifier) NotifyHealthCheckAlert(_ context.Context, alert *HealthCheckAlert) error {
	slog.Warn("health check status changed",
		"healthcheck_id", alert.HealthCheckID,
		"name", alert.HealthCheckName,
		"url", alert.URL,
		"previous_status", alert.PreviousStatus,
		"current_status", alert.CurrentStatus,
		"status_code", alert.StatusCode,
		"response_ms", alert.ResponseMs,
		"error", alert.ErrorMessage,
	)
	return nil
}

// HealthCheckWebhookNotifier sends health check alerts to a webhook URL.
type HealthCheckWebhookNotifier struct {
	URL    string
	Client *http.Client
}

// HealthCheckWebhookPayload is the JSON body sent for health check alerts.
type HealthCheckWebhookPayload struct {
	HealthCheckID   string `json:"healthcheck_id"`
	HealthCheckName string `json:"healthcheck_name"`
	URL             string `json:"url"`
	PreviousStatus  string `json:"previous_status"`
	CurrentStatus   string `json:"current_status"`
	StatusCode      *int   `json:"status_code,omitempty"`
	ResponseMs      *int   `json:"response_ms,omitempty"`
	ErrorMessage    string `json:"error,omitempty"`
	Timestamp       string `json:"timestamp"`
}

// NewHealthCheckWebhookNotifier creates a new webhook notifier for health check alerts.
func NewHealthCheckWebhookNotifier(url string) *HealthCheckWebhookNotifier {
	return &HealthCheckWebhookNotifier{
		URL:    url,
		Client: httpclient.New(10 * time.Second),
	}
}

func (n *HealthCheckWebhookNotifier) NotifyHealthCheckAlert(ctx context.Context, alert *HealthCheckAlert) error {
	payload := HealthCheckWebhookPayload{
		HealthCheckID:   alert.HealthCheckID,
		HealthCheckName: alert.HealthCheckName,
		URL:             alert.URL,
		PreviousStatus:  string(alert.PreviousStatus),
		CurrentStatus:   string(alert.CurrentStatus),
		StatusCode:      alert.StatusCode,
		ResponseMs:      alert.ResponseMs,
		ErrorMessage:    alert.ErrorMessage,
		Timestamp:       alert.Timestamp.Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling healthcheck webhook payload: %w", err)
	}

	return retry.Do(ctx, retry.Config{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    4 * time.Second,
	}, func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("creating healthcheck webhook request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-OpenTrace-Event", "healthcheck.status_change")

		resp, err := n.Client.Do(req)
		if err != nil {
			return fmt.Errorf("sending healthcheck webhook: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 500 {
			return fmt.Errorf("healthcheck webhook returned status %d", resp.StatusCode)
		}
		if resp.StatusCode >= 400 {
			return retry.Permanent(fmt.Errorf("healthcheck webhook returned status %d", resp.StatusCode))
		}
		return nil
	})
}

// NotifyAllHealthCheck sends an alert to all notifiers concurrently.
// Errors are logged but do not block.
func NotifyAllHealthCheck(ctx context.Context, notifiers []HealthCheckAlertNotifier, alert *HealthCheckAlert) {
	var wg sync.WaitGroup
	for _, n := range notifiers {
		wg.Add(1)
		go func(n HealthCheckAlertNotifier) {
			defer wg.Done()
			nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := n.NotifyHealthCheckAlert(nctx, alert); err != nil {
				slog.Warn("health check notification error", "error", err)
			}
		}(n)
	}
	wg.Wait()
}
