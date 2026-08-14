package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

// baseDialer is the underlying dialer; guardedDialContext wraps it to reject
// link-local destinations after DNS resolution (SSRF hardening).
var baseDialer = &net.Dialer{
	Timeout:   10 * time.Second,
	KeepAlive: 30 * time.Second,
}

// allowLinkLocalEnv disables the SSRF guard when set to "true".
const allowLinkLocalEnv = "OPENTRACE_ALLOW_LINK_LOCAL"

// blockedMetadataIPs are cloud instance-metadata endpoints that fall outside the
// link-local ranges and therefore need to be named explicitly. The IPv4 IMDS
// address (169.254.169.254) and the ECS task metadata address (169.254.170.2)
// are already covered by the link-local check.
var blockedMetadataIPs = []net.IP{
	net.ParseIP("fd00:ec2::254"),   // AWS IMDS over IPv6 (unique-local, not link-local)
	net.ParseIP("100.100.100.200"), // Alibaba Cloud metadata
	net.ParseIP("192.0.0.192"),     // Oracle Cloud metadata
}

// parseHostIP parses a host taken from an address, tolerating the forms the
// stdlib's net.ParseIP rejects: bracketed IPv6 literals and zone-qualified
// literals such as "fe80::1%en0". Without stripping the zone, ParseIP returns
// nil and a zoned link-local address slips past the guard entirely.
// Returns nil when the host is a DNS name rather than an IP literal.
func parseHostIP(host string) net.IP {
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	return net.ParseIP(host)
}

// checkBlockedAddr reports an error when addr targets a link-local address
// (169.254.0.0/16, fe80::/10) or a known cloud metadata endpoint. Health-check
// URLs and connector hosts are user-supplied, so this closes the highest-value
// SSRF target. Private ranges (10/8, 172.16/12, 192.168/16, fc00::/7) and
// loopback are intentionally ALLOWED — monitoring internal/self-hosted infra is
// the tool's purpose. Set OPENTRACE_ALLOW_LINK_LOCAL=true to disable the guard.
func checkBlockedAddr(addr string) error {
	if os.Getenv(allowLinkLocalEnv) == "true" {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := parseHostIP(host)
	if ip == nil {
		return nil // a DNS name; the Control hook re-checks the resolved IP
	}
	// Normalise IPv4-mapped IPv6 ("::ffff:169.254.169.254") to its IPv4 form so
	// the range checks below cannot be dodged by spelling.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return fmt.Errorf("connection to link-local address %s blocked", host)
	}
	for _, blocked := range blockedMetadataIPs {
		if ip.Equal(blocked) {
			return fmt.Errorf("connection to cloud metadata address %s blocked", host)
		}
	}
	return nil
}

// guardedDialContext rejects blocked destinations given as literals in the URL.
func guardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if err := checkBlockedAddr(addr); err != nil {
		return nil, err
	}
	return baseDialer.DialContext(ctx, network, addr)
}

// controlBlockLinkLocal runs after DNS resolution, before connect, so it also
// catches hostnames that resolve to blocked IPs. Because it inspects the exact
// address the kernel is about to connect to, it also closes DNS rebinding
// within a single dial: a name that resolved to a benign IP at check time
// cannot be swapped for a metadata IP afterwards.
func controlBlockLinkLocal(network, address string, c syscall.RawConn) error {
	return checkBlockedAddr(address)
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
