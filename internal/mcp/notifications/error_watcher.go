package notifications

import (
	"fmt"
	"sync"
	"time"
)

// ErrorWatcher detects new error groups appearing rapidly and notifies.
type ErrorWatcher struct {
	dispatcher *Dispatcher
	counts     map[string]*errorCount // fingerprint → count tracker
	notified   map[string]time.Time   // fingerprint → last notification time
	mu         sync.Mutex
}

type errorCount struct {
	count          int
	firstSeen      time.Time
	exceptionClass string
	message        string
	service        string
}

// NewErrorWatcher creates an error watcher.
func NewErrorWatcher(d *Dispatcher) *ErrorWatcher {
	return &ErrorWatcher{
		dispatcher: d,
		counts:     make(map[string]*errorCount),
		notified:   make(map[string]time.Time),
	}
}

// ErrorEvent represents a new or recurring error detected during log ingestion.
type ErrorEvent struct {
	Fingerprint     string
	ExceptionClass  string
	Message         string
	Service         string
	OccurrenceCount int
	FirstSeenAt     time.Time
	AffectedUsers   int
	StackPreview    string
}

// OnError is called from the log ingest pipeline on each error log.
// Tracks occurrences internally. Notifies when a fingerprint reaches 3 occurrences
// within a 10-minute window.
func (w *ErrorWatcher) OnError(e ErrorEvent) {
	if w.dispatcher == nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Don't re-notify within 1 hour
	if lastNotified, exists := w.notified[e.Fingerprint]; exists {
		if time.Since(lastNotified) < 1*time.Hour {
			// Still count it, just don't notify
			if ec, ok := w.counts[e.Fingerprint]; ok {
				ec.count++
			}
			return
		}
	}

	// Track this occurrence
	ec, exists := w.counts[e.Fingerprint]
	if !exists {
		ec = &errorCount{
			firstSeen:      time.Now(),
			exceptionClass: e.ExceptionClass,
			message:        e.Message,
			service:        e.Service,
		}
		w.counts[e.Fingerprint] = ec
	}
	ec.count++

	// Reset if the window expired (older than 10 minutes)
	if time.Since(ec.firstSeen) > 10*time.Minute {
		ec.count = 1
		ec.firstSeen = time.Now()
		return
	}

	// Notify at 3 occurrences
	if ec.count < 3 {
		return
	}

	w.notified[e.Fingerprint] = time.Now()

	// Clean up old entries
	now := time.Now()
	for fp, t := range w.notified {
		if now.Sub(t) > 2*time.Hour {
			delete(w.notified, fp)
			delete(w.counts, fp)
		}
	}

	msgPreview := ec.message
	if len(msgPreview) > 120 {
		msgPreview = msgPreview[:120] + "..."
	}

	w.dispatcher.Notify(Notification{
		Type:     NotifyNewErrorGroup,
		Severity: "error",
		Title:    fmt.Sprintf("New error: %s", ec.exceptionClass),
		Summary: fmt.Sprintf(
			"%s in %s — %d occurrences in %s. %s",
			ec.exceptionClass, ec.service,
			ec.count, time.Since(ec.firstSeen).Round(time.Second),
			msgPreview,
		),
		Context: map[string]any{
			"fingerprint":      e.Fingerprint,
			"exception_class":  ec.exceptionClass,
			"message":          ec.message,
			"service":          ec.service,
			"occurrence_count": ec.count,
			"window":           time.Since(ec.firstSeen).Round(time.Second).String(),
			"suggested_action": fmt.Sprintf("Investigate with: errors(action: \"detail\", fingerprint: \"%s\")", e.Fingerprint),
		},
	})
}
