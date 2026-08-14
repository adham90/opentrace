package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNew_ReturnsClientWithTimeout(t *testing.T) {
	c := New(5 * time.Second)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.Timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", c.Timeout)
	}
}

func TestNew_UsesSharedTransport(t *testing.T) {
	c1 := New(5 * time.Second)
	c2 := New(10 * time.Second)
	if c1.Transport != c2.Transport {
		t.Error("expected both clients to share the same transport")
	}
}

func TestNewNoRedirect_DoesNotFollow(t *testing.T) {
	redirected := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			redirected = true
			w.WriteHeader(200)
			return
		}
		http.Redirect(w, r, "/target", http.StatusFound)
	}))
	defer srv.Close()

	c := NewNoRedirect(5 * time.Second)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302 (redirect not followed)", resp.StatusCode)
	}
	if redirected {
		t.Error("redirect was followed, but NoRedirect client should not follow")
	}
}

func TestNew_CanMakeRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := New(5 * time.Second)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestNew_ConnectionReuse(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := New(5 * time.Second)

	// Make multiple requests — they should reuse connections via the pool
	for i := 0; i < 5; i++ {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
	}

	if requestCount != 5 {
		t.Errorf("expected 5 requests, got %d", requestCount)
	}
}

func TestCheckBlockedAddr(t *testing.T) {
	t.Setenv("OPENTRACE_ALLOW_LINK_LOCAL", "")

	tests := []struct {
		name    string
		addr    string
		blocked bool
	}{
		{"ipv4 metadata", "169.254.169.254:80", true},
		{"ipv4 ecs metadata", "169.254.170.2:80", true},
		{"ipv4 link-local", "169.254.1.1:443", true},
		{"ipv6 link-local", "[fe80::1]:80", true},
		{"ipv6 link-local with zone", "[fe80::1%en0]:80", true},
		{"ipv6 link-local metadata with zone", "[fe80::a9fe:a9fe%eth0]:80", true},
		{"ipv6 link-local no brackets", "fe80::1", true},
		{"ipv4-mapped metadata", "[::ffff:169.254.169.254]:80", true},
		{"aws ipv6 imds", "[fd00:ec2::254]:80", true},
		{"alibaba metadata", "100.100.100.200:80", true},
		{"oracle metadata", "192.0.0.192:80", true},
		{"loopback allowed", "127.0.0.1:8080", false},
		{"ipv6 loopback allowed", "[::1]:8080", false},
		{"rfc1918 allowed", "10.1.2.3:80", false},
		{"unique-local allowed", "[fd12:3456::1]:80", false},
		{"public allowed", "93.184.216.34:443", false},
		{"hostname deferred to control hook", "example.com:443", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkBlockedAddr(tt.addr)
			if tt.blocked && err == nil {
				t.Errorf("checkBlockedAddr(%q) = nil, want blocked", tt.addr)
			}
			if !tt.blocked && err != nil {
				t.Errorf("checkBlockedAddr(%q) = %v, want allowed", tt.addr, err)
			}
		})
	}
}

func TestCheckBlockedAddr_EscapeHatch(t *testing.T) {
	t.Setenv("OPENTRACE_ALLOW_LINK_LOCAL", "true")
	if err := checkBlockedAddr("169.254.169.254:80"); err != nil {
		t.Errorf("guard should be disabled by OPENTRACE_ALLOW_LINK_LOCAL=true, got %v", err)
	}
}

func TestGuardedDialContext_BlocksZonedLinkLocal(t *testing.T) {
	t.Setenv("OPENTRACE_ALLOW_LINK_LOCAL", "")
	if _, err := guardedDialContext(context.Background(), "tcp", "[fe80::1%en0]:80"); err == nil {
		t.Fatal("zoned link-local literal was not blocked by the dialer")
	}
}
