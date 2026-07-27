package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// WriteJSON
// ---------------------------------------------------------------------------

func TestWriteJSON(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		value      any
		wantStatus int
		wantCT     string
	}{
		{
			name:       "200 with map payload",
			status:     http.StatusOK,
			value:      map[string]string{"key": "value"},
			wantStatus: http.StatusOK,
			wantCT:     "application/json",
		},
		{
			name:       "201 with struct payload",
			status:     http.StatusCreated,
			value:      struct{ Name string }{"alice"},
			wantStatus: http.StatusCreated,
			wantCT:     "application/json",
		},
		{
			name:       "204 with nil payload",
			status:     http.StatusNoContent,
			value:      nil,
			wantStatus: http.StatusNoContent,
			wantCT:     "application/json",
		},
		{
			name:       "500 with slice payload",
			status:     http.StatusInternalServerError,
			value:      []int{1, 2, 3},
			wantStatus: http.StatusInternalServerError,
			wantCT:     "application/json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteJSON(w, tc.status, tc.value)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			if got := w.Header().Get("Content-Type"); got != tc.wantCT {
				t.Errorf("Content-Type = %q, want %q", got, tc.wantCT)
			}
			// Body must be valid JSON.
			var raw json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
				t.Errorf("body is not valid JSON: %v (body=%q)", err, w.Body.String())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WriteError
// ---------------------------------------------------------------------------

func TestWriteError(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		msg        string
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "400 bad request",
			status:     http.StatusBadRequest,
			msg:        "bad request",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "bad request",
		},
		{
			name:       "404 not found",
			status:     http.StatusNotFound,
			msg:        "not found",
			wantStatus: http.StatusNotFound,
			wantMsg:    "not found",
		},
		{
			name:       "500 internal error",
			status:     http.StatusInternalServerError,
			msg:        "internal error",
			wantStatus: http.StatusInternalServerError,
			wantMsg:    "internal error",
		},
		{
			name:       "empty message",
			status:     http.StatusBadRequest,
			msg:        "",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteError(w, tc.status, tc.msg)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tc.wantStatus)
			}

			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not valid JSON: %v", err)
			}
			if body["error"] != tc.wantMsg {
				t.Errorf("error = %q, want %q", body["error"], tc.wantMsg)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WriteDetailedError
// ---------------------------------------------------------------------------

func TestWriteDetailedError(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		message     string
		code        string
		details     map[string]any
		wantStatus  int
		wantError   string
		wantCode    string
		wantDetails bool
	}{
		{
			name:        "with details map",
			status:      http.StatusUnprocessableEntity,
			message:     "validation failed",
			code:        "VALIDATION_ERROR",
			details:     map[string]any{"field": "email", "reason": "required"},
			wantStatus:  http.StatusUnprocessableEntity,
			wantError:   "validation failed",
			wantCode:    "VALIDATION_ERROR",
			wantDetails: true,
		},
		{
			name:        "without details (nil)",
			status:      http.StatusBadRequest,
			message:     "bad input",
			code:        "BAD_INPUT",
			details:     nil,
			wantStatus:  http.StatusBadRequest,
			wantError:   "bad input",
			wantCode:    "BAD_INPUT",
			wantDetails: false,
		},
		{
			name:        "with empty details map",
			status:      http.StatusConflict,
			message:     "conflict",
			code:        "CONFLICT",
			details:     map[string]any{},
			wantStatus:  http.StatusConflict,
			wantError:   "conflict",
			wantCode:    "CONFLICT",
			wantDetails: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteDetailedError(w, tc.status, tc.message, tc.code, tc.details)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tc.wantStatus)
			}

			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not valid JSON: %v", err)
			}

			if got, _ := body["error"].(string); got != tc.wantError {
				t.Errorf("error = %q, want %q", got, tc.wantError)
			}
			if got, _ := body["code"].(string); got != tc.wantCode {
				t.Errorf("code = %q, want %q", got, tc.wantCode)
			}

			_, hasDetails := body["details"]
			if hasDetails != tc.wantDetails {
				t.Errorf("has details = %v, want %v", hasDetails, tc.wantDetails)
			}

			if tc.wantDetails && tc.details != nil {
				details, ok := body["details"].(map[string]any)
				if !ok {
					t.Fatalf("details is not a map")
				}
				for k, v := range tc.details {
					if details[k] != v {
						t.Errorf("details[%q] = %v, want %v", k, details[k], v)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FormatJSONError
// ---------------------------------------------------------------------------

func TestFormatJSONError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
		contains []string
	}{
		{
			name:     "syntax error",
			err:      &json.SyntaxError{Offset: 42},
			expected: "request body",
			contains: []string{"syntax error", "byte offset 42", "request body"},
		},
		{
			name: "unmarshal type error",
			err: &json.UnmarshalTypeError{
				Value: "string",
				Type:  reflect.TypeOf(0),
				Field: "age",
			},
			expected: "payload",
			contains: []string{"field", "age", "int", "string", "payload"},
		},
		{
			name:     "generic error",
			err:      errors.New("unexpected EOF"),
			expected: "config",
			contains: []string{"unexpected EOF", "config"},
		},
		{
			name:     "syntax error at offset 0",
			err:      &json.SyntaxError{Offset: 0},
			expected: "body",
			contains: []string{"syntax error", "byte offset 0"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatJSONError(tc.err, tc.expected)
			for _, sub := range tc.contains {
				if !strings.Contains(got, sub) {
					t.Errorf("FormatJSONError() = %q, want it to contain %q", got, sub)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// UserFromContext / WithUser
// ---------------------------------------------------------------------------

func TestUserContext(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		user := &store.User{ID: "u1", Email: "alice@example.com", Role: store.RoleAdmin}
		ctx := WithUser(context.Background(), user)
		got := UserFromContext(ctx)
		if got == nil {
			t.Fatal("UserFromContext returned nil")
		}
		if got.ID != user.ID {
			t.Errorf("ID = %q, want %q", got.ID, user.ID)
		}
		if got.Email != user.Email {
			t.Errorf("Email = %q, want %q", got.Email, user.Email)
		}
		if got.Role != user.Role {
			t.Errorf("Role = %q, want %q", got.Role, user.Role)
		}
	})

	t.Run("empty context returns nil", func(t *testing.T) {
		got := UserFromContext(context.Background())
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("wrong type in context returns nil", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ctxKeyUser, "not-a-user")
		got := UserFromContext(ctx)
		if got != nil {
			t.Errorf("expected nil for wrong type, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// SessionFromContext / WithSession
// ---------------------------------------------------------------------------

func TestSessionContext(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		session := &store.Session{ID: "s1", UserID: "u1", Token: "tok123"}
		ctx := WithSession(context.Background(), session)
		got := SessionFromContext(ctx)
		if got == nil {
			t.Fatal("SessionFromContext returned nil")
		}
		if got.ID != session.ID {
			t.Errorf("ID = %q, want %q", got.ID, session.ID)
		}
		if got.UserID != session.UserID {
			t.Errorf("UserID = %q, want %q", got.UserID, session.UserID)
		}
		if got.Token != session.Token {
			t.Errorf("Token = %q, want %q", got.Token, session.Token)
		}
	})

	t.Run("empty context returns nil", func(t *testing.T) {
		got := SessionFromContext(context.Background())
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("wrong type in context returns nil", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ctxKeySession, 42)
		got := SessionFromContext(ctx)
		if got != nil {
			t.Errorf("expected nil for wrong type, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// CSRFToken
// ---------------------------------------------------------------------------

func TestCSRFToken(t *testing.T) {
	t.Run("with token set", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), CtxKeyCSRFToken, "abc123")
		got := CSRFToken(ctx)
		if got != "abc123" {
			t.Errorf("CSRFToken = %q, want %q", got, "abc123")
		}
	})

	t.Run("without token returns empty string", func(t *testing.T) {
		got := CSRFToken(context.Background())
		if got != "" {
			t.Errorf("CSRFToken = %q, want empty string", got)
		}
	})

	t.Run("wrong type returns empty string", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), CtxKeyCSRFToken, 12345)
		got := CSRFToken(ctx)
		if got != "" {
			t.Errorf("CSRFToken = %q, want empty string", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ParseDurationWithDays
// ---------------------------------------------------------------------------

func TestParseDurationWithDays(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{
			name:  "standard hours",
			input: "1h",
			want:  time.Hour,
		},
		{
			name:  "standard minutes",
			input: "30m",
			want:  30 * time.Minute,
		},
		{
			name:  "standard seconds",
			input: "90s",
			want:  90 * time.Second,
		},
		{
			name:  "7 days",
			input: "7d",
			want:  7 * 24 * time.Hour,
		},
		{
			name:  "30 days",
			input: "30d",
			want:  30 * 24 * time.Hour,
		},
		{
			name:  "1 day",
			input: "1d",
			want:  24 * time.Hour,
		},
		{
			name:    "invalid input",
			input:   "abc",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "just d with no number",
			input:   "d",
			wantErr: true,
		},
		{
			name:    "negative-ish day format not parseable by Atoi prefix",
			input:   "xd",
			wantErr: true,
		},
		{
			name:  "combined standard duration",
			input: "1h30m",
			want:  time.Hour + 30*time.Minute,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDurationWithDays(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for input %q, got %v", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseDurationWithDays(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ParseSinceParam
// ---------------------------------------------------------------------------

func TestParseSinceParam(t *testing.T) {
	t.Run("valid RFC3339 timestamp", func(t *testing.T) {
		input := "2024-01-15T10:30:00Z"
		got, err := ParseSinceParam(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want, _ := time.Parse(time.RFC3339, input)
		if !got.Equal(want) {
			t.Errorf("ParseSinceParam(%q) = %v, want %v", input, got, want)
		}
	})

	t.Run("valid RFC3339 with timezone", func(t *testing.T) {
		input := "2024-06-01T08:00:00+05:00"
		got, err := ParseSinceParam(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want, _ := time.Parse(time.RFC3339, input)
		if !got.Equal(want) {
			t.Errorf("ParseSinceParam(%q) = %v, want %v", input, got, want)
		}
	})

	t.Run("valid duration 24h", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := ParseSinceParam("24h")
		after := time.Now().UTC()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The result should be approximately 24 hours in the past.
		earliest := before.Add(-24 * time.Hour)
		latest := after.Add(-24 * time.Hour)
		if got.Before(earliest.Add(-time.Second)) || got.After(latest.Add(time.Second)) {
			t.Errorf("ParseSinceParam(24h) = %v, want between %v and %v", got, earliest, latest)
		}
	})

	t.Run("valid duration with days 7d", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := ParseSinceParam("7d")
		after := time.Now().UTC()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		earliest := before.Add(-7 * 24 * time.Hour)
		latest := after.Add(-7 * 24 * time.Hour)
		if got.Before(earliest.Add(-time.Second)) || got.After(latest.Add(time.Second)) {
			t.Errorf("ParseSinceParam(7d) = %v, want between %v and %v", got, earliest, latest)
		}
	})

	t.Run("invalid input", func(t *testing.T) {
		_, err := ParseSinceParam("not-a-time")
		if err == nil {
			t.Error("expected error for invalid input")
		}
	})

	t.Run("empty string", func(t *testing.T) {
		_, err := ParseSinceParam("")
		if err == nil {
			t.Error("expected error for empty string")
		}
	})
}

// ---------------------------------------------------------------------------
// Audit
// ---------------------------------------------------------------------------

// mockAuditStore is a minimal mock for store.AuditStore used in Audit tests.
type mockAuditStore struct {
	lastParams store.LogAuditParams
	logCalled  bool
	logErr     error
}

func (m *mockAuditStore) Log(ctx context.Context, params store.LogAuditParams) error {
	m.logCalled = true
	m.lastParams = params
	return m.logErr
}

func (m *mockAuditStore) Recent(ctx context.Context, limit int) ([]store.AuditEntry, error) {
	return nil, nil
}

func (m *mockAuditStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
}

func TestAudit(t *testing.T) {
	t.Run("nil store is no-op", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/action", nil)
		user := &store.User{ID: "u1", Email: "a@b.com"}
		req = req.WithContext(WithUser(req.Context(), user))
		// Should not panic.
		Audit(req, nil, "test.action", "thing", "t1", "details")
	})

	t.Run("nil user is no-op", func(t *testing.T) {
		mock := &mockAuditStore{}
		req := httptest.NewRequest(http.MethodPost, "/action", nil)
		// No user in context.
		Audit(req, mock, "test.action", "thing", "t1", "details")
		if mock.logCalled {
			t.Error("expected Log not to be called with nil user")
		}
	})

	t.Run("successful audit log", func(t *testing.T) {
		mock := &mockAuditStore{}
		user := &store.User{ID: "u1", Email: "admin@example.com"}
		req := httptest.NewRequest(http.MethodPost, "/action", nil)
		req.RemoteAddr = "192.168.1.1:54321"
		req = req.WithContext(WithUser(req.Context(), user))

		Audit(req, mock, "user.delete", "user", "u2", "deleted user u2")

		if !mock.logCalled {
			t.Fatal("expected Log to be called")
		}
		p := mock.lastParams
		if p.UserID != "u1" {
			t.Errorf("UserID = %q, want %q", p.UserID, "u1")
		}
		if p.UserEmail != "admin@example.com" {
			t.Errorf("UserEmail = %q, want %q", p.UserEmail, "admin@example.com")
		}
		if p.Action != "user.delete" {
			t.Errorf("Action = %q, want %q", p.Action, "user.delete")
		}
		if p.TargetType != "user" {
			t.Errorf("TargetType = %q, want %q", p.TargetType, "user")
		}
		if p.TargetID != "u2" {
			t.Errorf("TargetID = %q, want %q", p.TargetID, "u2")
		}
		if p.Details != "deleted user u2" {
			t.Errorf("Details = %q, want %q", p.Details, "deleted user u2")
		}
		if p.IPAddress != "192.168.1.1:54321" {
			t.Errorf("IPAddress = %q, want %q", p.IPAddress, "192.168.1.1:54321")
		}
	})

	t.Run("store error does not panic", func(t *testing.T) {
		mock := &mockAuditStore{logErr: fmt.Errorf("db write failed")}
		user := &store.User{ID: "u1", Email: "a@b.com"}
		req := httptest.NewRequest(http.MethodPost, "/action", nil)
		req = req.WithContext(WithUser(req.Context(), user))

		// Should not panic even if store returns an error.
		Audit(req, mock, "test.action", "thing", "t1", "details")
		if !mock.logCalled {
			t.Error("expected Log to be called even when it returns an error")
		}
	})
}

// ---------------------------------------------------------------------------
// GenerateAPIKey
// ---------------------------------------------------------------------------

func TestGenerateAPIKey(t *testing.T) {
	t.Run("has ot_ prefix", func(t *testing.T) {
		key, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(key, "ot_") {
			t.Errorf("key = %q, want prefix %q", key, "ot_")
		}
	})

	t.Run("length is 67", func(t *testing.T) {
		key, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// "ot_" (3) + hex(32 bytes) = 3 + 64 = 67
		if len(key) != 67 {
			t.Errorf("len(key) = %d, want 67 (key=%q)", len(key), key)
		}
	})

	t.Run("keys are unique", func(t *testing.T) {
		key1, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		key2, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if key1 == key2 {
			t.Error("two generated keys should not be equal")
		}
	})

	t.Run("hex portion is valid hex", func(t *testing.T) {
		key, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		hexPart := key[3:] // strip "ot_"
		for _, c := range hexPart {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("key contains non-hex character %q in hex portion %q", string(c), hexPart)
				break
			}
		}
	})
}

// ---------------------------------------------------------------------------
// MaskAPIKey
// ---------------------------------------------------------------------------

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{
			name: "empty string",
			key:  "",
			want: "",
		},
		{
			name: "single character",
			key:  "a",
			want: "a****",
		},
		{
			name: "exactly 8 characters",
			key:  "12345678",
			want: "1****",
		},
		{
			name: "less than 8 characters (3)",
			key:  "abc",
			want: "a****",
		},
		{
			name: "less than 8 characters (7)",
			key:  "abcdefg",
			want: "a****",
		},
		{
			name: "9 characters (more than 8)",
			key:  "123456789",
			want: "12345678****",
		},
		{
			name: "full API key",
			key:  "ot_abcdef1234567890abcdef1234567890abcdef1234567890abcdef12345678",
			want: "ot_abcde****",
		},
		{
			name: "exactly at boundary (8 chars, short path)",
			key:  "abcdefgh",
			want: "a****",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskAPIKey(tc.key)
			if got != tc.want {
				t.Errorf("MaskAPIKey(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsMaskedValue
// ---------------------------------------------------------------------------

func TestIsMaskedValue(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want bool
	}{
		{
			name: "ends with four asterisks",
			val:  "ot_abcde****",
			want: true,
		},
		{
			name: "just four asterisks",
			val:  "****",
			want: true,
		},
		{
			name: "ends with three asterisks only",
			val:  "abc***",
			want: false,
		},
		{
			name: "no asterisks",
			val:  "ot_abcdef",
			want: false,
		},
		{
			name: "empty string",
			val:  "",
			want: false,
		},
		{
			name: "asterisks in the middle",
			val:  "ab****cd",
			want: false,
		},
		{
			name: "five asterisks at end",
			val:  "ab*****",
			want: true,
		},
		{
			name: "single char + four asterisks",
			val:  "a****",
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsMaskedValue(tc.val)
			if got != tc.want {
				t.Errorf("IsMaskedValue(%q) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}
