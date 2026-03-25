package logger

import (
	"log/slog"
	"sync"
	"time"
)

// Event represents a log event broadcast to subscribers.
type Event struct {
	Time    time.Time      `json:"time"`
	Level   slog.Level     `json:"level"`
	Message string         `json:"message"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// Broadcaster fans out log events to subscribers.
type Broadcaster struct {
	mu   sync.RWMutex
	subs map[uint64]chan Event
	next uint64
}

// NewBroadcaster creates a new Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subs: make(map[uint64]chan Event),
	}
}

// Subscribe registers a new subscriber and returns an ID and channel.
// bufSize controls channel buffer capacity. Events are dropped for slow consumers.
func (b *Broadcaster) Subscribe(bufSize int) (id uint64, ch <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id = b.next
	b.next++

	c := make(chan Event, bufSize)
	b.subs[id] = c
	return id, c
}

// Unsubscribe removes a subscriber by ID and closes its channel.
func (b *Broadcaster) Unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if c, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(c)
	}
}

// Publish sends an event to all subscribers. Non-blocking -- drops events for full channels.
func (b *Broadcaster) Publish(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, c := range b.subs {
		select {
		case c <- event:
		default:
			// Drop event for slow consumer.
		}
	}
}

// SubCount returns the number of active subscribers.
func (b *Broadcaster) SubCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return len(b.subs)
}
