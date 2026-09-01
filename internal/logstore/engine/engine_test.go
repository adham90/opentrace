package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

func TestEncodeDecodeID(t *testing.T) {
	tests := []struct {
		hour  int64
		chunk int
		row   int
	}{
		{0, 0, 0},
		{1, 0, 0},
		{500000, 0, 42},
		{500000, 3, 49999},
		{500000, 255, 8388607}, // max chunk, max row
	}

	for _, tt := range tests {
		id := EncodeID(tt.hour, tt.chunk, tt.row)
		h, c, r := DecodeID(id)
		if h != tt.hour || c != tt.chunk || r != tt.row {
			t.Errorf("EncodeID(%d, %d, %d) = %d → DecodeID = (%d, %d, %d)",
				tt.hour, tt.chunk, tt.row, id, h, c, r)
		}
		// Verify ID is positive (int64)
		if id < 0 {
			t.Errorf("ID %d is negative", id)
		}
	}
}

func TestIDTimeSortable(t *testing.T) {
	// IDs from later hours should be greater than IDs from earlier hours
	id1 := EncodeID(100, 0, 0)
	id2 := EncodeID(101, 0, 0)
	if id1 >= id2 {
		t.Errorf("ID for hour 100 (%d) should be < ID for hour 101 (%d)", id1, id2)
	}

	// Within same hour, higher chunk = higher ID
	id3 := EncodeID(100, 0, 49999)
	id4 := EncodeID(100, 1, 0)
	if id3 >= id4 {
		t.Errorf("ID for chunk 0 row 49999 (%d) should be < ID for chunk 1 row 0 (%d)", id3, id4)
	}

	// Within same chunk, higher row = higher ID
	id5 := EncodeID(100, 0, 0)
	id6 := EncodeID(100, 0, 1)
	if id5 >= id6 {
		t.Errorf("ID for row 0 (%d) should be < ID for row 1 (%d)", id5, id6)
	}
}

func TestRingBuffer(t *testing.T) {
	ring := NewRingBuffer()

	// Empty ring
	snap := ring.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected empty snapshot, got %d entries", len(snap))
	}

	// Push some entries
	entries := []chunk.Entry{
		{ID: 1, Message: "hello"},
		{ID: 2, Message: "world"},
	}
	ring.Push(entries)

	snap = ring.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(snap))
	}
	if snap[0].ID != 1 || snap[1].ID != 2 {
		t.Errorf("snapshot order: got %d, %d", snap[0].ID, snap[1].ID)
	}
}

func TestRingBufferOverflow(t *testing.T) {
	ring := NewRingBuffer()

	// Push more than ringSize entries
	for i := range 300 {
		ring.Push([]chunk.Entry{{ID: int64(i)}})
	}

	snap := ring.Snapshot()
	if len(snap) != ringSize {
		t.Fatalf("expected %d entries, got %d", ringSize, len(snap))
	}

	// Should contain the last 256 entries (44-299)
	if snap[0].ID != 300-ringSize {
		t.Errorf("oldest entry: want %d, got %d", 300-ringSize, snap[0].ID)
	}
	if snap[ringSize-1].ID != 299 {
		t.Errorf("newest entry: want 299, got %d", snap[ringSize-1].ID)
	}
}

func TestRingBufferSubscribe(t *testing.T) {
	ring := NewRingBuffer()
	ch, unsub := ring.Subscribe()

	// Push entries after subscribing
	go func() {
		time.Sleep(10 * time.Millisecond) // ensure subscriber is ready
		ring.Push([]chunk.Entry{{ID: 42, Message: "test"}})
	}()

	// Subscriber should receive
	select {
	case received := <-ch:
		if len(received) != 1 || received[0].ID != 42 {
			t.Errorf("received unexpected: %v", received)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not receive within 2s")
	}

	unsub()
}

func TestRingBufferProjectsBodiesAndBoundsSubscriberBatch(t *testing.T) {
	ring := NewRingBuffer()
	ch, unsub := ring.Subscribe()
	defer unsub()

	entries := make([]chunk.Entry, ringSize+10)
	for i := range entries {
		entries[i] = chunk.Entry{ID: int64(i + 1), Message: "tail", Body: []byte(`{"large":"body"}`)}
	}
	ring.Push(entries)

	received := <-ch
	if len(received) != ringSize {
		t.Fatalf("subscriber batch = %d entries, want %d", len(received), ringSize)
	}
	if received[0].ID != 11 || received[len(received)-1].ID != int64(len(entries)) {
		t.Fatalf("subscriber received wrong suffix: first=%d last=%d", received[0].ID, received[len(received)-1].ID)
	}
	for i := range received {
		if len(received[i].Body) != 0 {
			t.Fatalf("subscriber entry %d retained body", i)
		}
	}
	if snap := ring.Snapshot(); len(snap) != ringSize || len(snap[0].Body) != 0 {
		t.Fatalf("snapshot was not projected/bounded: len=%d", len(snap))
	}
}

func TestRingBufferMultipleSubscribers(t *testing.T) {
	ring := NewRingBuffer()
	ch1, unsub1 := ring.Subscribe()
	ch2, unsub2 := ring.Subscribe()

	go func() {
		time.Sleep(10 * time.Millisecond)
		ring.Push([]chunk.Entry{{ID: 1}})
	}()

	for _, ch := range []<-chan []chunk.Entry{ch1, ch2} {
		select {
		case received := <-ch:
			if received[0].ID != 1 {
				t.Errorf("wrong ID: %d", received[0].ID)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("subscriber timeout")
		}
	}

	unsub1()
	unsub2()
}

func TestWALWriterAppend(t *testing.T) {
	dir := t.TempDir()
	ring := NewRingBuffer()

	w, err := NewWALWriter(dir, ring)
	if err != nil {
		t.Fatalf("NewWALWriter: %v", err)
	}
	defer w.Close()

	entries := []chunk.Entry{
		{Level: "info", Service: "api", Message: "hello"},
		{Level: "error", Service: "api", Message: "boom"},
	}

	result, err := w.Append(entries)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Check IDs were assigned
	if result[0].ID == 0 {
		t.Error("first entry ID should not be 0")
	}
	if result[1].ID <= result[0].ID {
		t.Error("second ID should be > first ID")
	}

	// Check received_at was set
	if result[0].ReceivedAt == 0 {
		t.Error("received_at should be set")
	}

	// Check entry count
	if w.EntryCount() != 2 {
		t.Errorf("entry count: want 2, got %d", w.EntryCount())
	}

	// Check ring buffer received the entries
	snap := ring.Snapshot()
	if len(snap) != 2 {
		t.Errorf("ring snapshot: want 2, got %d", len(snap))
	}
}

func TestWALWriterCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	ring := NewRingBuffer()

	// Write some entries
	w, err := NewWALWriter(dir, ring)
	if err != nil {
		t.Fatalf("NewWALWriter: %v", err)
	}

	_, err = w.Append([]chunk.Entry{
		{Level: "info", Service: "api", Message: "entry 1"},
		{Level: "info", Service: "api", Message: "entry 2"},
		{Level: "info", Service: "api", Message: "entry 3"},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	w.Close()

	// Simulate crash recovery: reopen
	ring2 := NewRingBuffer()
	w2, err := NewWALWriter(dir, ring2)
	if err != nil {
		t.Fatalf("NewWALWriter (recovery): %v", err)
	}
	defer w2.Close()

	// Should have replayed 3 entries
	if w2.EntryCount() != 3 {
		t.Errorf("recovered entry count: want 3, got %d", w2.EntryCount())
	}

	// Append more entries — IDs should continue from 3
	result, err := w2.Append([]chunk.Entry{
		{Level: "info", Service: "api", Message: "entry 4"},
	})
	if err != nil {
		t.Fatalf("Append after recovery: %v", err)
	}

	if w2.EntryCount() != 4 {
		t.Errorf("total entry count: want 4, got %d", w2.EntryCount())
	}

	// Verify the new entry's ID encodes row=3 (4th entry, 0-indexed)
	_, _, row := DecodeID(result[0].ID)
	if row != 3 {
		t.Errorf("recovered entry row: want 3, got %d", row)
	}
}

func TestWALWriterRotate(t *testing.T) {
	dir := t.TempDir()
	ring := NewRingBuffer()

	w, err := NewWALWriter(dir, ring)
	if err != nil {
		t.Fatalf("NewWALWriter: %v", err)
	}
	defer w.Close()

	_, err = w.Append([]chunk.Entry{
		{Level: "info", Service: "api", Message: "before rotate"},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Rotate
	sealPath, sealHour, count, err := w.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	if count != 1 {
		t.Errorf("seal entry count: want 1, got %d", count)
	}
	if sealHour == 0 {
		t.Error("seal hour should not be 0")
	}
	if sealPath == "" {
		t.Error("seal path should not be empty")
	}

	t.Logf("rotated: sealPath=%s sealHour=%d count=%d", sealPath, sealHour, count)

	// After rotation, entry count should be 0
	if w.EntryCount() != 0 {
		t.Errorf("entry count after rotate: want 0, got %d", w.EntryCount())
	}

	// Can still append to the new WAL
	_, err = w.Append([]chunk.Entry{
		{Level: "info", Service: "api", Message: "after rotate"},
	})
	if err != nil {
		t.Fatalf("Append after rotate: %v", err)
	}
	if w.EntryCount() != 1 {
		t.Errorf("entry count after post-rotate append: want 1, got %d", w.EntryCount())
	}
}

func TestWALWriterConcurrent(t *testing.T) {
	dir := t.TempDir()
	ring := NewRingBuffer()

	w, err := NewWALWriter(dir, ring)
	if err != nil {
		t.Fatalf("NewWALWriter: %v", err)
	}
	defer w.Close()

	// 10 goroutines, each appending 100 entries
	var wg sync.WaitGroup
	for g := range 10 {
		wg.Add(1)
		go func(goroutine int) {
			defer wg.Done()
			for i := range 100 {
				_, err := w.Append([]chunk.Entry{
					{Level: "info", Service: "api", Message: "concurrent"},
				})
				if err != nil {
					t.Errorf("goroutine %d entry %d: %v", goroutine, i, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	if w.EntryCount() != 1000 {
		t.Errorf("total entries: want 1000, got %d", w.EntryCount())
	}

	// All IDs should be unique
	seen := make(map[int64]bool, 1000)
	snap := ring.Snapshot() // only last 256
	for _, e := range snap {
		if seen[e.ID] {
			t.Errorf("duplicate ID: %d", e.ID)
		}
		seen[e.ID] = true
	}
}
