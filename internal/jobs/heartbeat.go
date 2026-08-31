package jobs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/adham90/opentrace/internal/httpclient"
)

// heartbeatTimeout bounds a single ping. The schedule fires every minute, so a
// ping that outlives its own interval has already failed.
const heartbeatTimeout = 10 * time.Second

// HeartbeatInterval is how often the liveness ping fires. One minute is the
// smallest window the common free monitors (healthchecks.io, cronitor) accept.
const HeartbeatInterval = 60 * time.Second

// heartbeatClient is shared so successive pings reuse the connection.
var heartbeatClient = httpclient.New(heartbeatTimeout)

// PingHeartbeat GETs url to prove this process is alive, for an external
// monitor to alarm on when the pings stop.
//
// The ping is deliberately unconditional — it reports liveness, not health.
// Gating it on internal checks would turn "OpenTrace is broken" back into
// silence, which is the exact failure this exists to catch. It runs through the
// job queue for the same reason: a wedged SQLite stops the pings, and an
// OpenTrace that cannot write is an OpenTrace that cannot alert.
func PingHeartbeat(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating heartbeat request: %w", err)
	}

	resp, err := heartbeatClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending heartbeat: %w", err)
	}
	defer resp.Body.Close()
	// Drain so the connection returns to the pool instead of being closed.
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("heartbeat returned status %d", resp.StatusCode)
	}
	return nil
}
