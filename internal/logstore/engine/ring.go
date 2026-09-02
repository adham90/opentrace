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
	// Tail is a summary stream. Retaining opaque bodies in the ring and in every
	// subscriber channel made one large SDK batch pin tens of megabytes even
	// though details are addressable by ID. A push larger than the ring can only
	// leave its newest ringSize entries visible, so project exactly that suffix.
	if len(entries) > ringSize {
		entries = entries[len(entries)-ringSize:]
	}
	projected := make([]chunk.Entry, len(entries))
	for i := range entries {
		projected[i] = entries[i]
		projected[i].Body = nil
	}

	rb.mu.Lock()
	for _, e := range projected {
		rb.entries[rb.head%ringSize] = e
		rb.head++
		rb.count++
	}
	rb.mu.Unlock()

	// Fan out to subscribers (non-blocking)
	rb.subMu.Lock()
	for _, ch := range rb.subs {
		select {
		case ch <- projected:
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
	return rb.snapshotLocked()
}

// snapshotLocked builds the snapshot; caller holds rb.mu (read or write).
func (rb *RingBuffer) snapshotLocked() []chunk.Entry {
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

// SnapshotAndSubscribe atomically takes a snapshot and registers a subscriber,
// so a batch pushed between the two can't land in neither (which silently and
// permanently dropped it from a tail stream). Push takes rb.mu before subMu, so
// holding both here means every batch is either already in the snapshot or
// still to be delivered on the channel. Entries may therefore appear in both;
// consumers dedupe by entry ID.
func (rb *RingBuffer) SnapshotAndSubscribe() ([]chunk.Entry, <-chan []chunk.Entry, func()) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	snapshot := rb.snapshotLocked()
	ch, unsubscribe := rb.Subscribe()
	return snapshot, ch, unsubscribe
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
