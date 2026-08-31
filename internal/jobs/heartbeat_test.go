package jobs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPingHeartbeat(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "ok", status: http.StatusOK, wantErr: false},
		{name: "no content", status: http.StatusNoContent, wantErr: false},
		{name: "client error", status: http.StatusNotFound, wantErr: true},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			err := PingHeartbeat(context.Background(), srv.URL)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for status %d, got nil", tt.status)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for status %d: %v", tt.status, err)
			}
			if gotMethod != http.MethodGet {
				t.Errorf("method = %q, want GET", gotMethod)
			}
		})
	}
}

// A dead endpoint must surface as an error rather than being swallowed —
// the whole point is that the operator finds out.
func TestPingHeartbeatUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	if err := PingHeartbeat(context.Background(), url); err == nil {
		t.Fatal("expected error pinging a closed server, got nil")
	}
}

func TestPingHeartbeatCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := PingHeartbeat(ctx, srv.URL); err == nil {
		t.Fatal("expected error with a cancelled context, got nil")
	}
}
