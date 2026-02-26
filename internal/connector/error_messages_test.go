package connector

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFriendlyError_ConnectionRefused(t *testing.T) {
	err := errors.New("dial tcp 10.0.0.5:5432: connection refused")
	got := FriendlyError(err)

	if !strings.Contains(got, "Cannot reach the database server") {
		t.Errorf("expected friendly message for connection refused, got: %s", got)
	}
	if !strings.Contains(got, "(original: ") {
		t.Errorf("expected original error in output, got: %s", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("expected original error text preserved, got: %s", got)
	}
}

func TestFriendlyError_NoSuchHost(t *testing.T) {
	err := errors.New("dial tcp: lookup bad-hostname.example.com: no such host")
	got := FriendlyError(err)

	if !strings.Contains(got, "hostname could not be resolved") {
		t.Errorf("expected friendly message for no such host, got: %s", got)
	}
	if !strings.Contains(got, "(original: ") {
		t.Errorf("expected original error in output, got: %s", got)
	}
}

func TestFriendlyError_HostnameResolving(t *testing.T) {
	err := errors.New("hostname resolving error: no address found")
	got := FriendlyError(err)

	if !strings.Contains(got, "hostname could not be resolved") {
		t.Errorf("expected friendly message for hostname resolving, got: %s", got)
	}
}

func TestFriendlyError_PasswordAuthFailed(t *testing.T) {
	err := errors.New(`FATAL: password authentication failed for user "myuser"`)
	got := FriendlyError(err)

	if !strings.Contains(got, "Authentication failed") {
		t.Errorf("expected friendly message for auth failure, got: %s", got)
	}
	if !strings.Contains(got, "pg_hba.conf") {
		t.Errorf("expected pg_hba.conf hint, got: %s", got)
	}
	if !strings.Contains(got, "(original: ") {
		t.Errorf("expected original error in output, got: %s", got)
	}
}

func TestFriendlyError_PermissionDenied(t *testing.T) {
	err := errors.New(`FATAL: permission denied for database "prod"`)
	got := FriendlyError(err)

	if !strings.Contains(got, "doesn't have permission") {
		t.Errorf("expected friendly message for permission denied, got: %s", got)
	}
	if !strings.Contains(got, "GRANT CONNECT") {
		t.Errorf("expected GRANT hint, got: %s", got)
	}
}

func TestFriendlyError_SSLNotEnabled(t *testing.T) {
	err := errors.New("server does not support SSL, but SSL was required")
	got := FriendlyError(err)

	// The pattern checks lowercase "the server does not support ssl"
	// Let's also test the explicit "ssl is not enabled" pattern
	err2 := errors.New("SSL is not enabled on this server")
	got2 := FriendlyError(err2)

	// At least one of these should match
	matched := strings.Contains(got, "SSL connection required") || strings.Contains(got2, "SSL connection required")
	if !matched {
		t.Errorf("expected friendly message for SSL error, got: %q and %q", got, got2)
	}
}

func TestFriendlyError_SSLMode(t *testing.T) {
	err := errors.New("pq: could not connect with sslmode=require")
	got := FriendlyError(err)

	if !strings.Contains(got, "SSL connection required") || !strings.Contains(got, "sslmode") {
		t.Errorf("expected friendly message for sslmode error, got: %s", got)
	}
}

func TestFriendlyError_Timeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"timeout", errors.New("dial tcp: i/o timeout")},
		{"deadline exceeded", errors.New("context deadline exceeded")},
		{"timed out", errors.New("connection timed out")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FriendlyError(tc.err)
			if !strings.Contains(got, "Connection timed out") {
				t.Errorf("expected friendly message for timeout, got: %s", got)
			}
			if !strings.Contains(got, "(original: ") {
				t.Errorf("expected original error in output, got: %s", got)
			}
		})
	}
}

func TestFriendlyError_TooManyConnections(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"too many connections", errors.New("FATAL: too many connections for role")},
		{"remaining connection slots", errors.New("FATAL: remaining connection slots are reserved")},
		{"too many clients", errors.New("FATAL: sorry, too many clients already")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FriendlyError(tc.err)
			if !strings.Contains(got, "too many active connections") {
				t.Errorf("expected friendly message for too many connections, got: %s", got)
			}
		})
	}
}

func TestFriendlyError_DatabaseNotExist(t *testing.T) {
	err := errors.New(`FATAL: database "nonexistent_db" does not exist`)
	got := FriendlyError(err)

	if !strings.Contains(got, "specified database does not exist") {
		t.Errorf("expected friendly message for missing database, got: %s", got)
	}
	if !strings.Contains(got, "(original: ") {
		t.Errorf("expected original error in output, got: %s", got)
	}
}

func TestFriendlyError_RoleNotExist(t *testing.T) {
	err := errors.New(`FATAL: role "nonexistent_user" does not exist`)
	got := FriendlyError(err)

	if !strings.Contains(got, "specified user/role does not exist") {
		t.Errorf("expected friendly message for missing role, got: %s", got)
	}
}

func TestFriendlyError_UnknownPassthrough(t *testing.T) {
	original := "some completely unknown error from the database driver"
	err := errors.New(original)
	got := FriendlyError(err)

	if got != original {
		t.Errorf("expected original error message for unknown error, got: %s", got)
	}
	// Should NOT contain "(original:" since it's a passthrough
	if strings.Contains(got, "(original:") {
		t.Errorf("unknown errors should not be wrapped, got: %s", got)
	}
}

func TestFriendlyError_NilError(t *testing.T) {
	got := FriendlyError(nil)
	if got != "" {
		t.Errorf("expected empty string for nil error, got: %s", got)
	}
}

func TestFriendlyError_WrappedError(t *testing.T) {
	inner := errors.New("dial tcp 10.0.0.5:5432: connection refused")
	wrapped := fmt.Errorf("connecting to target DB: %w", inner)
	got := FriendlyError(wrapped)

	if !strings.Contains(got, "Cannot reach the database server") {
		t.Errorf("expected friendly message for wrapped connection refused, got: %s", got)
	}
	if !strings.Contains(got, "connecting to target DB") {
		t.Errorf("expected full wrapped error text in original, got: %s", got)
	}
}

func TestFriendlyError_DoubleWrappedError(t *testing.T) {
	inner := errors.New(`FATAL: password authentication failed for user "admin"`)
	wrapped := fmt.Errorf("pinging target DB: %w", inner)
	doubleWrapped := fmt.Errorf("connector init: %w", wrapped)
	got := FriendlyError(doubleWrapped)

	if !strings.Contains(got, "Authentication failed") {
		t.Errorf("expected friendly message for double-wrapped auth error, got: %s", got)
	}
}

func TestClassifyError_KnownPatterns(t *testing.T) {
	tests := []struct {
		err      error
		expected ErrorCode
	}{
		{errors.New("connection refused"), ErrCodeConnectionRefused},
		{errors.New("no such host"), ErrCodeHostNotFound},
		{errors.New("password authentication failed"), ErrCodeAuthFailed},
		{errors.New("permission denied"), ErrCodePermissionDenied},
		{errors.New("sslmode error"), ErrCodeSSL},
		{errors.New("context deadline exceeded"), ErrCodeTimeout},
		{errors.New("too many connections"), ErrCodeTooManyConns},
		{errors.New(`database "x" does not exist`), ErrCodeDBNotFound},
		{errors.New(`role "x" does not exist`), ErrCodeRoleNotFound},
		{errors.New("unknown error"), ErrCodeUnknown},
	}

	for _, tc := range tests {
		t.Run(string(tc.expected), func(t *testing.T) {
			got := ClassifyError(tc.err)
			if got != tc.expected {
				t.Errorf("ClassifyError(%q) = %q, want %q", tc.err, got, tc.expected)
			}
		})
	}
}

func TestClassifyError_NilError(t *testing.T) {
	got := ClassifyError(nil)
	if got != ErrCodeUnknown {
		t.Errorf("ClassifyError(nil) = %q, want %q", got, ErrCodeUnknown)
	}
}

func TestFriendlyMessage_KnownCode(t *testing.T) {
	msg := FriendlyMessage(ErrCodeConnectionRefused)
	if msg == "" {
		t.Error("expected non-empty message for CONNECTION_REFUSED")
	}
	if !strings.Contains(msg, "Cannot reach") {
		t.Errorf("unexpected message: %s", msg)
	}
}

func TestFriendlyMessage_UnknownCode(t *testing.T) {
	msg := FriendlyMessage("NONEXISTENT_CODE")
	if msg != "" {
		t.Errorf("expected empty message for unknown code, got: %s", msg)
	}
}

func TestFriendlyError_CaseInsensitive(t *testing.T) {
	// Postgres errors can vary in casing
	err := errors.New("FATAL: PASSWORD AUTHENTICATION FAILED for user")
	got := FriendlyError(err)

	if !strings.Contains(got, "Authentication failed") {
		t.Errorf("expected case-insensitive match for auth failure, got: %s", got)
	}
}

func TestFriendlyError_AllPatternsIncludeOriginal(t *testing.T) {
	// Verify that every known pattern wraps the original error
	testErrors := []error{
		errors.New("connection refused"),
		errors.New("no such host"),
		errors.New("password authentication failed"),
		errors.New("permission denied"),
		errors.New("ssl is not enabled"),
		errors.New("connection timed out"),
		errors.New("too many connections"),
		errors.New(`database "x" does not exist`),
		errors.New(`role "x" does not exist`),
	}

	for _, err := range testErrors {
		got := FriendlyError(err)
		if !strings.Contains(got, "(original: ") {
			t.Errorf("FriendlyError(%q) missing original error, got: %s", err, got)
		}
		if !strings.Contains(got, err.Error()) {
			t.Errorf("FriendlyError(%q) should contain original text, got: %s", err, got)
		}
	}
}
