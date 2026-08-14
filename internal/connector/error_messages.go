package connector

import (
	"fmt"
	"strings"
)

// ErrorCode identifies the category of a connection error.
type ErrorCode string

const (
	ErrCodeConnectionRefused ErrorCode = "CONNECTION_REFUSED"
	ErrCodeHostNotFound      ErrorCode = "HOST_NOT_FOUND"
	ErrCodeAuthFailed        ErrorCode = "AUTH_FAILED"
	ErrCodePermissionDenied  ErrorCode = "PERMISSION_DENIED"
	ErrCodeSSL               ErrorCode = "SSL_ERROR"
	ErrCodeTimeout           ErrorCode = "TIMEOUT"
	ErrCodeTooManyConns      ErrorCode = "TOO_MANY_CONNECTIONS"
	ErrCodeDBNotFound        ErrorCode = "DATABASE_NOT_FOUND"
	ErrCodeRoleNotFound      ErrorCode = "ROLE_NOT_FOUND"
	ErrCodeUnknown           ErrorCode = "UNKNOWN"
)

type errorPattern struct {
	// anyOf matches if the error contains ANY of these substrings (OR logic).
	anyOf []string
	// allOf matches if the error contains ALL of these substrings (AND logic).
	// When both anyOf and allOf are empty, the pattern never matches.
	// When only allOf is set, all substrings must be present.
	// When only anyOf is set, at least one must be present.
	// When both are set, allOf is checked first; if it passes, the match succeeds.
	allOf   []string
	code    ErrorCode
	message string
}

// errorPatterns lists known Postgres/network error patterns, ordered by
// specificity. The first match wins, so more specific patterns (e.g.
// "password authentication failed") should appear before broader ones
// (e.g. "authentication failed").
var errorPatterns = []errorPattern{
	{
		anyOf:   []string{"connection refused"},
		code:    ErrCodeConnectionRefused,
		message: "Cannot reach the database server. Verify the host and port are correct and the database is running.",
	},
	{
		anyOf:   []string{"no such host", "hostname resolving"},
		code:    ErrCodeHostNotFound,
		message: "The database hostname could not be resolved. Check for typos in the hostname.",
	},
	{
		anyOf:   []string{"password authentication failed"},
		code:    ErrCodeAuthFailed,
		message: "Authentication failed. Check the username and password, and verify the user has password login enabled in pg_hba.conf.",
	},
	{
		anyOf:   []string{"permission denied"},
		code:    ErrCodePermissionDenied,
		message: "This user doesn't have permission to access the database. Grant access with: GRANT CONNECT ON DATABASE dbname TO username;",
	},
	{
		anyOf:   []string{"ssl is not enabled", "ssl off", "sslmode", "ssl/tls", "the server does not support ssl"},
		code:    ErrCodeSSL,
		message: "SSL connection required but the database doesn't support it, or vice versa. Check the sslmode setting.",
	},
	{
		anyOf:   []string{"timeout", "deadline exceeded", "timed out"},
		code:    ErrCodeTimeout,
		message: "Connection timed out. The database may be overloaded or a firewall is blocking the connection.",
	},
	{
		anyOf:   []string{"too many connections", "remaining connection slots", "too many clients"},
		code:    ErrCodeTooManyConns,
		message: "The database has too many active connections. Close unused connections or increase max_connections.",
	},
	{
		allOf:   []string{"database", "does not exist"},
		code:    ErrCodeDBNotFound,
		message: "The specified database does not exist. Check the database name for typos.",
	},
	{
		allOf:   []string{"role", "does not exist"},
		code:    ErrCodeRoleNotFound,
		message: "The specified user/role does not exist. Check the username for typos.",
	},
}

// FriendlyError maps a raw database/network error to a user-friendly message.
// If no known pattern matches, the original error message is returned unchanged.
func FriendlyError(err error) string {
	if err == nil {
		return ""
	}

	// Driver errors frequently echo the DSN back (including its password), and
	// this message is shown to users and written to logs.
	msg := RedactSecrets(err.Error())
	lower := strings.ToLower(msg)

	for _, ep := range errorPatterns {
		if matchesPattern(lower, ep) {
			return fmt.Sprintf("%s (original: %s)", ep.message, msg)
		}
	}

	return msg
}

// ClassifyError returns the error code for a given error.
// Useful for structured API responses.
func ClassifyError(err error) ErrorCode {
	if err == nil {
		return ErrCodeUnknown
	}

	lower := strings.ToLower(err.Error())

	for _, ep := range errorPatterns {
		if matchesPattern(lower, ep) {
			return ep.code
		}
	}

	return ErrCodeUnknown
}

// FriendlyMessage returns only the suggestion part (without the original error)
// for a given error code.
func FriendlyMessage(code ErrorCode) string {
	for _, ep := range errorPatterns {
		if ep.code == code {
			return ep.message
		}
	}
	return ""
}

// matchesPattern checks whether the lowered error message matches the given
// error pattern. If allOf is set, all substrings must be present. If anyOf
// is set, at least one substring must be present. If both are set, allOf
// takes precedence (all must match).
func matchesPattern(lower string, ep errorPattern) bool {
	if len(ep.allOf) > 0 {
		for _, p := range ep.allOf {
			if !strings.Contains(lower, p) {
				return false
			}
		}
		return true
	}
	if len(ep.anyOf) > 0 {
		for _, p := range ep.anyOf {
			if strings.Contains(lower, p) {
				return true
			}
		}
	}
	return false
}
