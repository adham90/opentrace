package logger

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestNewBroadcaster(t *testing.T) {
	b := NewBroadcaster()
	if b == nil {
		t.Fatal("NewBroadcaster returned nil")
	}
	if b.SubCount() != 0 {
		t.Fatalf("expected 0 subscribers, got %d", b.SubCount())
	}
}

func TestSubscribeUnsubscribeLifecycle(t *testing.T) {
	b := NewBroadcaster()

	id1, ch1 := b.Subscribe(10)
	if ch1 == nil {
		t.Fatal("Subscribe returned nil channel")
	}
	if b.SubCount() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", b.SubCount())
	}

	id2, ch2 := b.Subscribe(10)
	if ch2 == nil {
		t.Fatal("Subscribe returned nil channel")
	}
	if id1 == id2 {
		t.Fatal("expected unique subscriber IDs")
	}
	if b.SubCount() != 2 {
		t.Fatalf("expected 2 subscribers, got %d", b.SubCount())
	}

	b.Unsubscribe(id1)
	if b.SubCount() != 1 {
		t.Fatalf("expected 1 subscriber after unsubscribe, got %d", b.SubCount())
	}

	b.Unsubscribe(id2)
	if b.SubCount() != 0 {
		t.Fatalf("expected 0 subscribers after unsubscribe, got %d", b.SubCount())
	}
}

func TestUnsubscribeClosesChannel(t *testing.T) {
	b := NewBroadcaster()
	id, ch := b.Subscribe(10)

	b.Unsubscribe(id)

	// Reading from a closed channel should return zero value and false.
	_, ok := <-ch
	if ok {
		t.Fatal("expected channel to be closed after Unsubscribe")
	}
}

func TestUnsubscribeNonexistentID(t *testing.T) {
	b := NewBroadcaster()
	// Should not panic.
	b.Unsubscribe(999)
}

func TestPublishDeliversToSubscriber(t *testing.T) {
	b := NewBroadcaster()
	_, ch := b.Subscribe(10)

	event := Event{
		Time:    time.Now(),
		Level:   slog.LevelInfo,
		Message: "test message",
		Attrs:   map[string]any{"key": "value"},
	}
	b.Publish(event)

	select {
	case received := <-ch:
		if received.Message != event.Message {
			t.Fatalf("expected message %q, got %q", event.Message, received.Message)
		}
		if received.Level != event.Level {
			t.Fatalf("expected level %v, got %v", event.Level, received.Level)
		}
		if received.Attrs["key"] != "value" {
			t.Fatalf("expected attr key=value, got %v", received.Attrs["key"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestPublishMultipleSubscribers(t *testing.T) {
	b := NewBroadcaster()
	_, ch1 := b.Subscribe(10)
	_, ch2 := b.Subscribe(10)
	_, ch3 := b.Subscribe(10)

	event := Event{
		Time:    time.Now(),
		Level:   slog.LevelWarn,
		Message: "broadcast",
	}
	b.Publish(event)

	for i, ch := range []<-chan Event{ch1, ch2, ch3} {
		select {
		case received := <-ch:
			if received.Message != "broadcast" {
				t.Fatalf("subscriber %d: expected message %q, got %q", i, "broadcast", received.Message)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out waiting for event", i)
		}
	}
}

func TestPublishDropsForSlowConsumer(t *testing.T) {
	b := NewBroadcaster()
	_, ch := b.Subscribe(1) // buffer size of 1

	// Fill the buffer.
	b.Publish(Event{Message: "first"})

	// This should be dropped, not block.
	done := make(chan struct{})
	go func() {
		b.Publish(Event{Message: "second"})
		close(done)
	}()

	select {
	case <-done:
		// Publish returned without blocking -- correct behavior.
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on slow consumer")
	}

	// Only the first event should be in the channel.
	received := <-ch
	if received.Message != "first" {
		t.Fatalf("expected first event, got %q", received.Message)
	}

	// Channel should be empty now.
	select {
	case e := <-ch:
		t.Fatalf("expected empty channel, got event %q", e.Message)
	default:
		// Good, channel is empty.
	}
}

func TestPublishConcurrent(t *testing.T) {
	b := NewBroadcaster()
	_, ch := b.Subscribe(100)

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			b.Publish(Event{Message: "concurrent"})
		}(i)
	}
	wg.Wait()

	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			goto done
		}
	}
done:
	if count != 10 {
		t.Fatalf("expected 10 events, got %d", count)
	}
}

func TestSubCountAccuracy(t *testing.T) {
	b := NewBroadcaster()

	if b.SubCount() != 0 {
		t.Fatalf("expected 0, got %d", b.SubCount())
	}

	ids := make([]uint64, 5)
	for i := range 5 {
		id, _ := b.Subscribe(1)
		ids[i] = id
	}

	if b.SubCount() != 5 {
		t.Fatalf("expected 5, got %d", b.SubCount())
	}

	b.Unsubscribe(ids[0])
	b.Unsubscribe(ids[2])
	if b.SubCount() != 3 {
		t.Fatalf("expected 3, got %d", b.SubCount())
	}

	// Unsubscribing same ID again should be no-op.
	b.Unsubscribe(ids[0])
	if b.SubCount() != 3 {
		t.Fatalf("expected 3 after duplicate unsubscribe, got %d", b.SubCount())
	}
}

// --- FanOutHandler tests ---

func TestFanOutHandlerCallsInnerAndPublishes(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	bc := NewBroadcaster()
	handler := NewFanOutHandler(inner, bc)

	_, ch := bc.Subscribe(10)

	logger := slog.New(handler)
	logger.Info("hello", "foo", "bar")

	// Check inner handler received the log.
	if buf.Len() == 0 {
		t.Fatal("inner handler did not receive log")
	}

	// Check broadcaster published the event.
	select {
	case event := <-ch:
		if event.Message != "hello" {
			t.Fatalf("expected message %q, got %q", "hello", event.Message)
		}
		if event.Level != slog.LevelInfo {
			t.Fatalf("expected level INFO, got %v", event.Level)
		}
		if event.Attrs["foo"] != "bar" {
			t.Fatalf("expected attr foo=bar, got %v", event.Attrs["foo"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast event")
	}
}

func TestFanOutHandlerEnabled(t *testing.T) {
	inner := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})
	bc := NewBroadcaster()
	handler := NewFanOutHandler(inner, bc)

	ctx := context.Background()
	if handler.Enabled(ctx, slog.LevelInfo) {
		t.Fatal("expected INFO to be disabled when inner level is WARN")
	}
	if !handler.Enabled(ctx, slog.LevelWarn) {
		t.Fatal("expected WARN to be enabled")
	}
	if !handler.Enabled(ctx, slog.LevelError) {
		t.Fatal("expected ERROR to be enabled")
	}
}

func TestFanOutHandlerWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	bc := NewBroadcaster()
	handler := NewFanOutHandler(inner, bc)

	withAttrs := handler.WithAttrs([]slog.Attr{
		slog.String("component", "test"),
	})

	_, ch := bc.Subscribe(10)

	logger := slog.New(withAttrs)
	logger.Info("with attrs", "extra", "val")

	select {
	case event := <-ch:
		if event.Attrs["component"] != "test" {
			t.Fatalf("expected attr component=test, got %v", event.Attrs["component"])
		}
		if event.Attrs["extra"] != "val" {
			t.Fatalf("expected attr extra=val, got %v", event.Attrs["extra"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast event")
	}
}

func TestFanOutHandlerWithGroup(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	bc := NewBroadcaster()
	handler := NewFanOutHandler(inner, bc)

	withGroup := handler.WithGroup("request")

	_, ch := bc.Subscribe(10)

	logger := slog.New(withGroup)
	logger.Info("grouped", "method", "GET")

	select {
	case event := <-ch:
		if event.Attrs["request.method"] != "GET" {
			t.Fatalf("expected attr request.method=GET, got attrs: %v", event.Attrs)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast event")
	}
}

func TestFanOutHandlerWithGroupNested(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	bc := NewBroadcaster()
	handler := NewFanOutHandler(inner, bc)

	nested := handler.WithGroup("http").(*FanOutHandler).WithGroup("request")

	_, ch := bc.Subscribe(10)

	logger := slog.New(nested)
	logger.Info("nested groups", "path", "/api")

	select {
	case event := <-ch:
		if event.Attrs["http.request.path"] != "/api" {
			t.Fatalf("expected attr http.request.path=/api, got attrs: %v", event.Attrs)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast event")
	}
}

func TestFanOutHandlerWithAttrsAndGroup(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	bc := NewBroadcaster()
	handler := NewFanOutHandler(inner, bc)

	// Add group then attrs -- attrs should be prefixed with the group.
	combined := handler.WithGroup("server").(*FanOutHandler).WithAttrs([]slog.Attr{
		slog.String("host", "localhost"),
	})

	_, ch := bc.Subscribe(10)

	logger := slog.New(combined)
	logger.Info("combined", "port", 8080)

	select {
	case event := <-ch:
		if event.Attrs["server.host"] != "localhost" {
			t.Fatalf("expected attr server.host=localhost, got attrs: %v", event.Attrs)
		}
		val, ok := event.Attrs["server.port"]
		if !ok {
			t.Fatalf("expected attr server.port, got attrs: %v", event.Attrs)
		}
		// slog passes int64 for int values.
		if v, ok := val.(int64); !ok || v != 8080 {
			t.Fatalf("expected attr server.port=8080 (int64), got %v (%T)", val, val)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast event")
	}
}

func TestFanOutHandlerWithEmptyGroup(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	bc := NewBroadcaster()
	handler := NewFanOutHandler(inner, bc)

	// Empty group name should return the same handler.
	same := handler.WithGroup("")
	if same != handler {
		t.Fatal("expected WithGroup(\"\") to return the same handler")
	}
}

func TestFanOutHandlerNoAttrsOmitted(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	bc := NewBroadcaster()
	handler := NewFanOutHandler(inner, bc)

	_, ch := bc.Subscribe(10)

	logger := slog.New(handler)
	logger.Info("no attrs")

	select {
	case event := <-ch:
		if event.Attrs != nil {
			t.Fatalf("expected nil attrs for log with no attributes, got %v", event.Attrs)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast event")
	}
}

func TestFanOutHandlerImplementsInterface(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, nil)
	bc := NewBroadcaster()
	handler := NewFanOutHandler(inner, bc)

	// Compile-time check that FanOutHandler implements slog.Handler.
	var _ slog.Handler = handler
}
