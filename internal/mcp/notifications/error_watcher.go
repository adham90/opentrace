package notifications

import (
	"fmt"
	"sync"
	"time"
)

// ErrorWatcher detects new error groups appearing rapidly and notifies.
type ErrorWatcher struct {
	dispatcher *Dispatcher
	seen       map[string]time.Time // fingerprint → first notification time
	mu         sync.Mutex
}

// NewErrorWatcher creates an error watcher.
func NewErrorWatcher(d *Dispatcher) *ErrorWatcher {
	return &ErrorWatcher{
		dispatcher: d,
		seen:       make(map[string]time.Time),
	}
}

// ErrorEvent represents a new or recurring error detected during log ingestion.
type ErrorEvent struct {
	Fingerprint    string
	ExceptionClass string
	Message        string
	Service        string
	OccurrenceCount int
	FirstSeenAt    time.Time
	AffectedUsers  int
	StackPreview   string // first few lines of stack trace
}

// OnError is called from the log ingest pipeline when an error log creates or updates an error group.
// It notifies when a NEW fingerprint appears 3+ times within 10 minutes.
func (w *ErrorWatcher) OnError(e ErrorEvent) {
	if w.dispatcher == nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Already notified about this fingerprint recently?
	if lastNotified, exists := w.seen[e.Fingerprint]; exists {
		// Don't re-notify within 1 hour
		if time.Since(lastNotified) < 1*time.Hour {
			return
		}
	}

	// Only notify if it's a rapid occurrence (3+ in 10 min)
	if e.OccurrenceCount < 3 || time.Since(e.FirstSeenAt) > 10*time.Minute {
		return
	}

	w.seen[e.Fingerprint] = time.Now()

	// Clean up old entries
	for fp, t := range w.seen {
		if time.Since(t) > 2*time.Hour {
			delete(w.seen, fp)
		}
	}

	msgPreview := e.Message
	if len(msgPreview) > 120 {
		msgPreview = msgPreview[:120] + "..."
	}

	w.dispatcher.Notify(Notification{
		Type:     NotifyNewErrorGroup,
		Severity: "error",
		Title:    fmt.Sprintf("New error: %s", e.ExceptionClass),
		Summary: fmt.Sprintf(
			"%s in %s — %d occurrences in %s. %s",
			e.ExceptionClass, e.Service,
			e.OccurrenceCount, time.Since(e.FirstSeenAt).Round(time.Second),
			msgPreview,
		),
		Context: map[string]any{
			"fingerprint":      e.Fingerprint,
			"exception_class":  e.ExceptionClass,
			"message":          e.Message,
			"service":          e.Service,
			"occurrence_count": e.OccurrenceCount,
			"first_seen_at":    e.FirstSeenAt.Format(time.RFC3339),
			"affected_users":   e.AffectedUsers,
			"stack_preview":    e.StackPreview,
			"suggested_action": fmt.Sprintf("Investigate with: errors(action: \"detail\", fingerprint: \"%s\")", e.Fingerprint),
		},
	})
}
