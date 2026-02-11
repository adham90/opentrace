package watcher

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// RunEvent represents a single event from a watcher run execution.
type RunEvent struct {
	Type     string         `json:"type"`                // thinking, tool_call, observation, final, error
	Content  string         `json:"content"`
	ToolName string         `json:"tool_name,omitempty"`
	Args     map[string]any `json:"args,omitempty"`
	Time     time.Time      `json:"time"`
}

// EventHub is an in-memory event broadcaster keyed by run ID.
// It allows the executor to publish events and SSE handlers to subscribe.
type EventHub struct {
	mu     sync.RWMutex
	runs   map[uuid.UUID]*runStream
	timers []*time.Timer
	closed bool
}

type runStream struct {
	mu     sync.Mutex
	events []RunEvent
	subs   map[int]chan RunEvent
	nextID int
	done   bool
	cancel context.CancelFunc
}

// NewEventHub creates a new EventHub.
func NewEventHub() *EventHub {
	return &EventHub{
		runs: make(map[uuid.UUID]*runStream),
	}
}

// Register creates a new stream for a run with an optional cancel function.
func (h *EventHub) Register(runID uuid.UUID, cancel context.CancelFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runs[runID] = &runStream{
		subs:   make(map[int]chan RunEvent),
		cancel: cancel,
	}
}

// Cancel cancels a running run by calling its cancel function.
// Returns true if the run was found and cancelled.
func (h *EventHub) Cancel(runID uuid.UUID) bool {
	h.mu.RLock()
	rs, ok := h.runs[runID]
	h.mu.RUnlock()
	if !ok {
		return false
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.done || rs.cancel == nil {
		return false
	}
	rs.cancel()
	return true
}

// Publish appends an event to the run's buffer and fans out to all subscribers.
// Non-blocking: if a subscriber's channel is full, the event is dropped for that client.
func (h *EventHub) Publish(runID uuid.UUID, event RunEvent) {
	h.mu.RLock()
	rs, ok := h.runs[runID]
	h.mu.RUnlock()
	if !ok {
		return
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()

	rs.events = append(rs.events, event)
	for _, ch := range rs.subs {
		select {
		case ch <- event:
		default:
			// slow client — drop event (they can fetch from stored details later)
		}
	}
}

// Subscribe returns buffered events for catch-up, a channel for new events,
// an unsubscribe function, and whether the run was found.
func (h *EventHub) Subscribe(runID uuid.UUID) (ch <-chan RunEvent, buffered []RunEvent, unsubFn func(), ok bool) {
	h.mu.RLock()
	rs, found := h.runs[runID]
	h.mu.RUnlock()
	if !found {
		return nil, nil, nil, false
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()

	// Copy buffered events for catch-up
	buffered = make([]RunEvent, len(rs.events))
	copy(buffered, rs.events)

	// If already done, return buffered events and a closed channel
	if rs.done {
		closed := make(chan RunEvent)
		close(closed)
		return closed, buffered, func() {}, true
	}

	subCh := make(chan RunEvent, 64)
	id := rs.nextID
	rs.nextID++
	rs.subs[id] = subCh

	unsubFn = func() {
		rs.mu.Lock()
		defer rs.mu.Unlock()
		delete(rs.subs, id)
	}

	return subCh, buffered, unsubFn, true
}

// MarkDone closes all subscriber channels for the run and schedules cleanup.
func (h *EventHub) MarkDone(runID uuid.UUID) {
	h.mu.RLock()
	rs, ok := h.runs[runID]
	h.mu.RUnlock()
	if !ok {
		return
	}

	rs.mu.Lock()
	rs.done = true
	for id, ch := range rs.subs {
		close(ch)
		delete(rs.subs, id)
	}
	rs.mu.Unlock()

	// Schedule cleanup after 60s to allow late subscribers to get buffered events.
	h.mu.Lock()
	if !h.closed {
		t := time.AfterFunc(60*time.Second, func() {
			h.mu.Lock()
			delete(h.runs, runID)
			h.mu.Unlock()
		})
		h.timers = append(h.timers, t)
	} else {
		delete(h.runs, runID)
	}
	h.mu.Unlock()
}

// Close stops all pending cleanup timers and removes all run streams.
// Call this during graceful shutdown after the scheduler has stopped.
func (h *EventHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for _, t := range h.timers {
		t.Stop()
	}
	h.timers = nil
	for id, rs := range h.runs {
		rs.mu.Lock()
		if !rs.done {
			rs.done = true
			for sid, ch := range rs.subs {
				close(ch)
				delete(rs.subs, sid)
			}
		}
		rs.mu.Unlock()
		delete(h.runs, id)
	}
}
