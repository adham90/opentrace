package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"
)

// baseDialer is the underlying dialer; guardedDialContext wraps it to reject
// link-local destinations after DNS resolution (SSRF hardening).
var baseDialer = &net.Dialer{
	Timeout:   10 * time.Second,
	KeepAlive: 30 * time.Second,
}

// guardedDialContext blocks connections to link-local addresses (169.254.0.0/16,
// fe80::/10), which is where cloud metadata services live (169.254.169.254).
// Health-check URLs and connector hosts are user-supplied, so this closes the
// highest-value SSRF target. Private ranges (10/8, 172.16/12, 192.168/16) and
// loopback are intentionally ALLOWED — monitoring internal/self-hosted infra is
// the tool's purpose. Set OPENTRACE_ALLOW_LINK_LOCAL=true to disable this guard.
func guardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if os.Getenv("OPENTRACE_ALLOW_LINK_LOCAL") != "true" {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		// The address is already resolved to an IP:port by the resolver only for
		// literal IPs; for hostnames we check via a control hook below instead.
		if ip := net.ParseIP(host); ip != nil && ip.IsLinkLocalUnicast() {
			return nil, fmt.Errorf("connection to link-local address %s blocked", host)
		}
	}
	return baseDialer.DialContext(ctx, network, addr)
}

// controlBlockLinkLocal runs after DNS resolution, before connect, so it also
// catches hostnames that resolve to link-local IPs (DNS-rebinding to metadata).
func controlBlockLinkLocal(network, address string, c syscall.RawConn) error {
	if os.Getenv("OPENTRACE_ALLOW_LINK_LOCAL") == "true" {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLinkLocalUnicast() {
		return fmt.Errorf("connection to link-local address %s blocked", host)
	}
	return nil
}

// sharedTransport is a pooled HTTP transport reused across all clients.
// This avoids creating new TCP connections for every request.
var sharedTransport = &http.Transport{
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	IdleConnTimeout:       90 * time.Second,
	DialContext:           guardedDialContext,
	TLSHandshakeTimeout:   5 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

func init() {
	// Control fires after DNS resolution with the concrete IP, catching
	// hostnames (including rebinding) that DialContext's literal-IP check misses.
	baseDialer.Control = controlBlockLinkLocal
}

// New returns an *http.Client backed by the shared connection pool.
// The timeout applies per-request (including redirects, reading body, etc.).
func New(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: sharedTransport,
		Timeout:   timeout,
	}
}

// NewNoRedirect returns an *http.Client that does not follow redirects.
// Used by health checks to see the raw status code.
func NewNoRedirect(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: sharedTransport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
