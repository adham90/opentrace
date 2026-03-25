package logger

import (
	"context"
	"log/slog"
	"strings"
)

// FanOutHandler wraps a slog.Handler and publishes events to a Broadcaster.
type FanOutHandler struct {
	inner       slog.Handler
	broadcaster *Broadcaster
	attrs       []slog.Attr
	groups      []string
}

// NewFanOutHandler creates a handler that delegates to inner and publishes to broadcaster.
func NewFanOutHandler(inner slog.Handler, broadcaster *Broadcaster) *FanOutHandler {
	return &FanOutHandler{
		inner:       inner,
		broadcaster: broadcaster,
	}
}

// Enabled reports whether the inner handler is enabled for the given level.
func (h *FanOutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle calls the inner handler and then publishes the record as an Event.
func (h *FanOutHandler) Handle(ctx context.Context, record slog.Record) error {
	err := h.inner.Handle(ctx, record)

	attrs := make(map[string]any)

	// Add pre-configured attrs (from WithAttrs calls).
	for _, a := range h.attrs {
		key := h.prefixedKey(a.Key)
		attrs[key] = a.Value.Any()
	}

	// Add attrs from the record itself.
	record.Attrs(func(a slog.Attr) bool {
		key := h.prefixedKey(a.Key)
		attrs[key] = a.Value.Any()
		return true
	})

	event := Event{
		Time:    record.Time,
		Level:   record.Level,
		Message: record.Message,
	}
	if len(attrs) > 0 {
		event.Attrs = attrs
	}

	h.broadcaster.Publish(event)

	return err
}

// WithAttrs returns a new FanOutHandler with the given attrs appended.
func (h *FanOutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(combined, h.attrs)
	combined = append(combined, attrs...)

	return &FanOutHandler{
		inner:       h.inner.WithAttrs(attrs),
		broadcaster: h.broadcaster,
		attrs:       combined,
		groups:      h.groups,
	}
}

// WithGroup returns a new FanOutHandler with the given group appended.
func (h *FanOutHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	groups := make([]string, len(h.groups), len(h.groups)+1)
	copy(groups, h.groups)
	groups = append(groups, name)

	return &FanOutHandler{
		inner:       h.inner.WithGroup(name),
		broadcaster: h.broadcaster,
		attrs:       h.attrs,
		groups:      groups,
	}
}

// prefixedKey returns the attr key prefixed with group names (e.g., "group.key").
func (h *FanOutHandler) prefixedKey(key string) string {
	if len(h.groups) == 0 {
		return key
	}
	var b strings.Builder
	for _, g := range h.groups {
		b.WriteString(g)
		b.WriteByte('.')
	}
	b.WriteString(key)
	return b.String()
}
