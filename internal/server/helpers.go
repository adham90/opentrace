package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// contextKey is the type for context value keys in this package.
type contextKey int

const (
	ctxKeyUser contextKey = iota
	ctxKeySession
)

// WriteJSON writes a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// WriteError writes a JSON error response.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// WriteDetailedError writes a JSON error with a code and optional details.
func WriteDetailedError(w http.ResponseWriter, status int, message, code string, details map[string]any) {
	resp := map[string]any{
		"error": message,
		"code":  code,
	}
	if details != nil {
		resp["details"] = details
	}
	WriteJSON(w, status, resp)
}

// FormatJSONError produces a user-friendly message from a JSON parse error.
func FormatJSONError(err error, expected string) string {
	if syntaxErr, ok := err.(*json.SyntaxError); ok {
		return fmt.Sprintf("invalid JSON %s: syntax error at byte offset %d", expected, syntaxErr.Offset)
	}
	if typeErr, ok := err.(*json.UnmarshalTypeError); ok {
		return fmt.Sprintf("invalid JSON %s: field %q expects %s but got %s", expected, typeErr.Field, typeErr.Type, typeErr.Value)
	}
	return fmt.Sprintf("invalid JSON %s: %s", expected, err.Error())
}

// UserFromContext returns the authenticated user from the request context, or nil.
func UserFromContext(ctx context.Context) *store.User {
	u, _ := ctx.Value(ctxKeyUser).(*store.User)
	return u
}

// SessionFromContext returns the session from the request context, or nil.
func SessionFromContext(ctx context.Context) *store.Session {
	s, _ := ctx.Value(ctxKeySession).(*store.Session)
	return s
}

// WithUser stores a user in the context.
func WithUser(ctx context.Context, user *store.User) context.Context {
	return context.WithValue(ctx, ctxKeyUser, user)
}

// WithSession stores a session in the context.
func WithSession(ctx context.Context, session *store.Session) context.Context {
	return context.WithValue(ctx, ctxKeySession, session)
}

// ParseSinceParam parses a duration string like "24h", "7d" or an RFC3339
// timestamp into a time.Time.
func ParseSinceParam(s string) (time.Time, error) {
	// Try as RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try as duration
	d, err := ParseDurationWithDays(s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().UTC().Add(-d), nil
}

// ParseDurationWithDays extends time.ParseDuration to support "d" suffix for days.
func ParseDurationWithDays(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err == nil {
		return d, nil
	}
	// Handle "d" suffix
	if len(s) > 1 && s[len(s)-1] == 'd' {
		n, err := strconv.Atoi(s[:len(s)-1])
		if err == nil {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	return 0, err
}
