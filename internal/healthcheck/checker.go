package healthcheck

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/httpclient"
	"github.com/adham90/opentrace/pkg/store"
)

// maxBodyReadBytes limits how much of the response body we read for body matching (1 MB).
const maxBodyReadBytes = 1 << 20

// defaultRetries is used when hc.Retries is 0 (backward compatible — no retries).
const defaultRetries = 0

// retryDelays defines the sleep durations between retry attempts.
var retryDelays = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// Checker performs a single HTTP health check probe.
type Checker struct {
	client *http.Client
}

// NewChecker creates a Checker backed by a shared HTTP connection pool.
// Redirects are not followed so the raw status code is visible.
func NewChecker() *Checker {
	return &Checker{
		client: httpclient.NewNoRedirect(0), // per-request timeout set via context
	}
}

// Check probes the given health check endpoint and returns a result.
// If the health check has retries configured, failed checks are retried
// with short delays before declaring the endpoint down. Only the final
// result is recorded — retries are transparent.
func (c *Checker) Check(ctx context.Context, hc store.HealthCheck) store.HealthCheckResult {
	maxAttempts := hc.Retries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var result store.HealthCheckResult
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result = c.singleCheck(ctx, hc)

		// If the check passed, return immediately.
		if result.Status == store.HealthCheckUp || result.Status == store.HealthCheckDegraded {
			return result
		}

		// If this isn't the last attempt, sleep before retrying.
		if attempt < maxAttempts {
			delay := retryDelays[0]
			if attempt-1 < len(retryDelays) {
				delay = retryDelays[attempt-1]
			}
			slog.Debug("healthcheck retry",
				"name", hc.Name,
				"id", hc.ID,
				"attempt", attempt,
				"max_attempts", maxAttempts,
				"error", result.Error,
			)
			select {
			case <-ctx.Done():
				return result
			case <-time.After(delay):
			}
		}
	}

	return result
}

// singleCheck performs one HTTP probe (no retries).
func (c *Checker) singleCheck(ctx context.Context, hc store.HealthCheck) store.HealthCheckResult {
	timeout := time.Duration(hc.TimeoutSecs) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, hc.Method, hc.URL, nil)
	if err != nil {
		return store.HealthCheckResult{
			HealthCheckID: hc.ID,
			Status:        store.HealthCheckDown,
			Error:         err.Error(),
			CheckedAt:     time.Now().UTC(),
		}
	}
	req.Header.Set("User-Agent", "OpenTrace-HealthCheck/1.0")

	start := time.Now()
	resp, err := c.client.Do(req)
	elapsed := int(time.Since(start).Milliseconds())

	if err != nil {
		return store.HealthCheckResult{
			HealthCheckID: hc.ID,
			Status:        store.HealthCheckDown,
			ResponseMs:    &elapsed,
			Error:         err.Error(),
			CheckedAt:     time.Now().UTC(),
		}
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	result := store.HealthCheckResult{
		HealthCheckID: hc.ID,
		StatusCode:    &code,
		ResponseMs:    &elapsed,
		CheckedAt:     time.Now().UTC(),
	}

	// Determine status from HTTP status code.
	switch {
	case code == hc.ExpectedStatus:
		result.Status = store.HealthCheckUp
	case code >= 200 && code < 400:
		// Got a success-ish code but not the expected one → degraded
		result.Status = store.HealthCheckDegraded
	default:
		result.Status = store.HealthCheckDown
	}

	// Response body matching: if configured, verify the body contains the expected string.
	if hc.ExpectedBody != "" && result.Status == store.HealthCheckUp {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyReadBytes))
		if readErr != nil {
			result.Status = store.HealthCheckDown
			result.Error = "failed to read response body: " + readErr.Error()
			return result
		}
		if !strings.Contains(string(body), hc.ExpectedBody) {
			result.Status = store.HealthCheckDown
			result.Error = "response body did not contain expected string"
			return result
		}
	}

	// Slow response → degraded (>5s or >50% of timeout)
	slowThreshold := timeout / 2
	if result.Status == store.HealthCheckUp && time.Duration(elapsed)*time.Millisecond > slowThreshold {
		result.Status = store.HealthCheckDegraded
	}

	return result
}
