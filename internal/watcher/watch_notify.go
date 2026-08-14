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
	"github.com/adham90/opentrace/internal/safe"
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
	Environment    string  `json:"environment,omitempty"`
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
	// Prefer the alert's denormalized env (PR 2 copied it from the watch at
	// CreateAlert time) and fall back to the watch itself if the alert row
	// somehow predates that denormalization.
	env := alert.Environment
	if env == "" {
		env = watch.Environment
	}

	payload := WatchWebhookPayload{
		AlertID:        alert.ID,
		WatchID:        alert.WatchID,
		Metric:         alert.TriggerMetric(),
		Urgency:        string(alert.Urgency),
		Summary:        alert.Summary,
		TriggerValue:   alert.TriggerValue(),
		ThresholdValue: alert.ThresholdValue(),
		Service:        string(watch.Service),
		Environment:    env,
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
	env := alert.Environment
	if env == "" {
		env = watch.Environment
	}
	slog.Info("watch alert fired",
		"alert_id", alert.ID,
		"watch_id", alert.WatchID,
		"metric", alert.TriggerMetric(),
		"value", alert.TriggerValue(),
		"threshold", alert.ThresholdValue(),
		"urgency", alert.Urgency,
		"service", watch.Service,
		"environment", env,
		"summary", alert.Summary,
	)
	return nil
}

const (
	// notifyTimeout bounds a single notifier's delivery attempt.
	notifyTimeout = 30 * time.Second
	// maxInFlightNotifyBatches bounds how many alert dispatches may be in
	// flight at once, so a wedged webhook cannot spawn goroutines without end.
	maxInFlightNotifyBatches = 32
)

// NotifyAllWatchAlert sends a watch alert to all notifiers concurrently and
// waits for them. Errors are logged. Callers on a latency-sensitive path
// (the scheduler tick, the ingest-driven stream evaluator) must use a
// notifyDispatcher instead — a single unreachable webhook burns notifyTimeout
// here, which would stall every remaining due watch.
func NotifyAllWatchAlert(ctx context.Context, notifiers []WatchAlertNotifier, alert *store.WatchAlert, watch *store.Watch) {
	var wg sync.WaitGroup
	for _, n := range notifiers {
		wg.Add(1)
		go func(n WatchAlertNotifier) {
			defer wg.Done()
			nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
			defer cancel()
			if err := n.NotifyWatchAlert(nctx, alert, watch); err != nil {
				slog.Warn("watch notification error", "alert_id", alert.ID, "error", err)
			}
		}(n)
	}
	wg.Wait()
}

// notifyDispatcher delivers watch alerts off the caller's goroutine so a slow
// or unreachable notifier never delays watch evaluation.
type notifyDispatcher struct {
	sem chan struct{}
	wg  sync.WaitGroup
}

func newNotifyDispatcher() *notifyDispatcher {
	return &notifyDispatcher{sem: make(chan struct{}, maxInFlightNotifyBatches)}
}

// dispatch hands the alert to the notifiers in the background and returns
// immediately. It drops (and logs) the delivery if too many dispatches are
// already in flight.
func (d *notifyDispatcher) dispatch(ctx context.Context, notifiers []WatchAlertNotifier, alert *store.WatchAlert, watch *store.Watch) {
	if len(notifiers) == 0 {
		return
	}
	// The caller's context dies with the tick; notifications outlive it.
	nctx := context.WithoutCancel(ctx)
	select {
	case d.sem <- struct{}{}:
	default:
		slog.Warn("watch notification dropped: dispatcher saturated",
			"alert_id", alert.ID, "watch_id", watch.ID)
		return
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() { <-d.sem }()
		safe.Run("watcher.notify", func() {
			NotifyAllWatchAlert(nctx, notifiers, alert, watch)
		})
	}()
}

// wait blocks until all in-flight dispatches finish (shutdown and tests).
func (d *notifyDispatcher) wait() {
	d.wg.Wait()
}

// waitFor blocks until all in-flight dispatches finish or timeout elapses.
// It reports whether the drain completed. Shutdown paths use this so a wedged
// notifier cannot hold the process open past its shutdown budget, while a
// healthy delivery still gets a chance to land.
func (d *notifyDispatcher) waitFor(timeout time.Duration) bool {
	return waitGroupFor(&d.wg, timeout)
}

// waitGroupFor waits on wg for at most timeout, reporting whether it finished.
func waitGroupFor(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
