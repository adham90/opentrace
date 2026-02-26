package httpclient

import (
	"net"
	"net/http"
	"time"
)

// sharedTransport is a pooled HTTP transport reused across all clients.
// This avoids creating new TCP connections for every request.
var sharedTransport = &http.Transport{
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 10,
	IdleConnTimeout:     90 * time.Second,
	DialContext: (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	TLSHandshakeTimeout:   5 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
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
