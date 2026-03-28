package notifications

import (
	"fmt"
	"sync"
	"time"
)

// HealthWatcher notifies when health checks transition from UP to DOWN.
type HealthWatcher struct {
	dispatcher *Dispatcher
	lastState  map[string]string // checkID → "up" or "down"
	mu         sync.Mutex
}

// NewHealthWatcher creates a health watcher.
func NewHealthWatcher(d *Dispatcher) *HealthWatcher {
	return &HealthWatcher{
		dispatcher: d,
		lastState:  make(map[string]string),
	}
}

// HealthCheckEvent represents a health check result.
type HealthCheckEvent struct {
	CheckID       string
	Name          string
	URL           string
	Status        string // "up" or "down"
	StatusCode    int
	ResponseMs    float64
	ErrorMessage  string
}

// OnHealthCheckResult is called after each health check execution.
// Only notifies on state transitions (UP → DOWN).
func (w *HealthWatcher) OnHealthCheckResult(e HealthCheckEvent) {
	if w.dispatcher == nil {
		return
	}

	w.mu.Lock()
	prev, exists := w.lastState[e.CheckID]
	w.lastState[e.CheckID] = e.Status
	w.mu.Unlock()

	// Only notify on UP → DOWN transition
	if e.Status == "down" && (!exists || prev == "up") {
		detail := fmt.Sprintf("HTTP %d", e.StatusCode)
		if e.ErrorMessage != "" {
			detail = e.ErrorMessage
		}

		w.dispatcher.Notify(Notification{
			Type:     NotifyHealthCheckDown,
			Severity: "critical",
			Title:    fmt.Sprintf("Health check DOWN: %s", e.Name),
			Summary:  fmt.Sprintf("%s (%s) is down. %s", e.Name, e.URL, detail),
			Context: map[string]any{
				"check_id":     e.CheckID,
				"name":         e.Name,
				"url":          e.URL,
				"status_code":  e.StatusCode,
				"response_ms":  e.ResponseMs,
				"error":        e.ErrorMessage,
				"checked_at":   time.Now().Format(time.RFC3339),
				"suggested_action": fmt.Sprintf("Check endpoint health: healthchecks(action: \"uptime\")"),
			},
		})
	}
}
