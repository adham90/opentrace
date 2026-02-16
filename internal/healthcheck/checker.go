package healthcheck

import (
	"context"
	"net/http"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// Checker performs a single HTTP health check probe.
type Checker struct {
	client *http.Client
}

// NewChecker creates a Checker with a default transport (no redirects followed).
func NewChecker() *Checker {
	return &Checker{
		client: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Check probes the given health check endpoint and returns a result.
func (c *Checker) Check(ctx context.Context, hc store.HealthCheck) store.HealthCheckResult {
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

	// Determine status
	switch {
	case code == hc.ExpectedStatus:
		result.Status = store.HealthCheckUp
	case code >= 200 && code < 400:
		// Got a success-ish code but not the expected one → degraded
		result.Status = store.HealthCheckDegraded
	default:
		result.Status = store.HealthCheckDown
	}

	// Slow response → degraded (>5s or >50% of timeout)
	slowThreshold := timeout / 2
	if result.Status == store.HealthCheckUp && time.Duration(elapsed)*time.Millisecond > slowThreshold {
		result.Status = store.HealthCheckDegraded
	}

	return result
}
