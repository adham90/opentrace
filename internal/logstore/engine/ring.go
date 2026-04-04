package engine

import (
	"sync"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

const ringSize = 256

// RingBuffer is a fixed-size circular buffer of log entries for tail support.
// Writers call Push; tail subscribers call Snapshot + Subscribe.
type RingBuffer struct {
	mu      sync.RWMutex
	entries [ringSize]chunk.Entry
	head    int // next write position
	count   int // total entries written (for detecting empty ring)

	// Subscribers
	subMu   sync.Mutex
	subs    map[int]chan []chunk.Entry
	nextSub int
}

// NewRingBuffer creates a new ring buffer.
func NewRingBuffer() *RingBuffer {
	return &RingBuffer{
		subs: make(map[int]chan []chunk.Entry),
	}
}

// Push adds entries to the ring buffer and notifies subscribers.
func (rb *RingBuffer) Push(entries []chunk.Entry) {
	rb.mu.Lock()
	for _, e := range entries {
		rb.entries[rb.head%ringSize] = e
		rb.head++
		rb.count++
	}
	rb.mu.Unlock()

	// Fan out to subscribers (non-blocking)
	rb.subMu.Lock()
	for _, ch := range rb.subs {
		select {
		case ch <- entries:
		default:
			// Subscriber is slow, drop the batch (they'll catch up via Snapshot)
		}
	}
	rb.subMu.Unlock()
}

// Snapshot returns the most recent entries in the ring (up to ringSize).
// Entries are returned oldest-first.
func (rb *RingBuffer) Snapshot() []chunk.Entry {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 {
		return nil
	}

	n := rb.count
	if n > ringSize {
		n = ringSize
	}

	result := make([]chunk.Entry, n)
	start := rb.head - n
	for i := range n {
		result[i] = rb.entries[(start+i)%ringSize]
	}
	return result
}

// Subscribe returns a channel that receives new entry batches, and an unsubscribe function.
// The channel has a buffer of 16 batches.
func (rb *RingBuffer) Subscribe() (<-chan []chunk.Entry, func()) {
	ch := make(chan []chunk.Entry, 16)

	rb.subMu.Lock()
	id := rb.nextSub
	rb.nextSub++
	rb.subs[id] = ch
	rb.subMu.Unlock()

	unsubscribe := func() {
		rb.subMu.Lock()
		delete(rb.subs, id)
		close(ch) // close so any range loop exits
		rb.subMu.Unlock()
	}

	return ch, unsubscribe
}
