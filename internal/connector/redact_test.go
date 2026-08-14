package connector

import (
	"errors"
	"strings"
	"testing"
)

// Finding #2: connection strings reach slog through the circuit-breaker name
// and reach users through FriendlyError, so credentials must be stripped.
func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		mustNotHas string
		want       string
	}{
		{
			name:       "postgres uri",
			in:         "postgres://app:S3cretPW@db.internal:5432/prod",
			mustNotHas: "S3cretPW",
			want:       "postgres://app:***@db.internal:5432/prod",
		},
		{
			name:       "postgres uri with sslmode",
			in:         "postgresql://app:p%40ss@db:5432/prod?sslmode=require",
			mustNotHas: "p%40ss",
		},
		{
			name:       "mysql dsn",
			in:         "root:hunter2@tcp(db.internal:3306)/prod?parseTime=true",
			mustNotHas: "hunter2",
			want:       "root:***@tcp(db.internal:3306)/prod?parseTime=true",
		},
		{
			name:       "mysql url",
			in:         "mysql://root:hunter2@db.internal:3306/prod",
			mustNotHas: "hunter2",
		},
		{
			name:       "redis url without user",
			in:         "redis://:topsecret@cache.internal:6379/0",
			mustNotHas: "topsecret",
			want:       "redis://:***@cache.internal:6379/0",
		},
		{
			name:       "key value connection string",
			in:         "host=db user=app password=S3cretPW dbname=prod",
			mustNotHas: "S3cretPW",
			want:       "host=db user=app password=*** dbname=prod",
		},
		{
			name:       "turso auth token query param",
			in:         "https://mydb-org.turso.io?authToken=eyJhbGciOi",
			mustNotHas: "eyJhbGciOi",
		},
		{
			name:       "driver error echoing the dsn",
			in:         "dial tcp: error connecting to root:hunter2@tcp(db:3306)/prod",
			mustNotHas: "hunter2",
		},
		{
			name: "nothing to redact",
			in:   "postgres://db.internal:5432/prod",
			want: "postgres://db.internal:5432/prod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactSecrets(tt.in)
			if tt.mustNotHas != "" && strings.Contains(got, tt.mustNotHas) {
				t.Errorf("RedactSecrets(%q) = %q, still contains secret %q", tt.in, got, tt.mustNotHas)
			}
			if tt.want != "" && got != tt.want {
				t.Errorf("RedactSecrets(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestConnectorLabel(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"postgres://app:S3cretPW@db.internal:5432/prod", "postgres://db.internal:5432/prod"},
		{"redis://:topsecret@cache.internal:6379/0", "redis://cache.internal:6379/0"},
		{"root:hunter2@tcp(db.internal:3306)/prod?parseTime=true", "tcp(db.internal:3306)/prod"},
		{"https://mydb-org.turso.io?authToken=eyJhbGciOi", "https://mydb-org.turso.io"},
		{"", "connector"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := ConnectorLabel(tt.in)
			if got != tt.want {
				t.Errorf("ConnectorLabel(%q) = %q, want %q", tt.in, got, tt.want)
			}
			for _, secret := range []string{"S3cretPW", "topsecret", "hunter2", "eyJhbGciOi"} {
				if strings.Contains(got, secret) {
					t.Errorf("ConnectorLabel(%q) leaked %q", tt.in, secret)
				}
			}
		})
	}
}

// Even if a caller passes a raw DSN, the breaker (which logs its name on every
// state transition) must not hold a password.
func TestNewCircuitBreaker_RedactsName(t *testing.T) {
	cb := NewCircuitBreaker("postgres://app:S3cretPW@db.internal:5432/prod", DefaultCircuitBreakerConfig())
	if strings.Contains(cb.name, "S3cretPW") {
		t.Errorf("circuit breaker name leaks credentials: %q", cb.name)
	}
}

func TestFriendlyError_RedactsCredentials(t *testing.T) {
	err := errors.New("connection refused: postgres://app:S3cretPW@db.internal:5432/prod")
	got := FriendlyError(err)
	if strings.Contains(got, "S3cretPW") {
		t.Errorf("FriendlyError leaks credentials: %q", got)
	}
}
