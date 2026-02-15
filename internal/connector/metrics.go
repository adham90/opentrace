package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/store"
	"github.com/google/uuid"
)

// MetricsConnector implements DataSource for server metrics queries.
type MetricsConnector struct {
	serverStore store.ServerStore
	metricStore store.MetricStore
}

// NewMetricsConnector creates a new MetricsConnector.
func NewMetricsConnector(ss store.ServerStore, ms store.MetricStore) *MetricsConnector {
	return &MetricsConnector{serverStore: ss, metricStore: ms}
}

func (c *MetricsConnector) Type() ConnectorType { return ConnectorServerMetrics }

func (c *MetricsConnector) TestConnection(ctx context.Context) error {
	_, err := c.serverStore.List(ctx)
	return err
}

func (c *MetricsConnector) Tools() []Tool {
	return []Tool{
		{
			Name: "list_servers",
			Description: `List all monitored servers with their current status.
Returns hostname, IP, OS, architecture, status (online/offline/unknown), and last seen time.
Use this to see which servers are being monitored and their health status.`,
			Params:  []ToolParam{},
			Handler: c.handleListServers,
		},
		{
			Name: "query_server_metrics",
			Description: `Query time-series metrics for a specific server.
Metrics include: cpu.usage_percent, memory.usage_percent, disk.usage_percent, load.1m, load.5m, etc.
- server_id: UUID of the server (from list_servers)
- metric_name: specific metric to query (e.g. "cpu.usage_percent")
- start: only metrics after this time (ISO 8601)
- end: only metrics before this time (ISO 8601)
- limit: max results (default 100)`,
			Params: []ToolParam{
				{Name: "server_id", Type: "string", Required: true},
				{Name: "metric_name", Type: "string", Required: false},
				{Name: "start", Type: "string", Required: false},
				{Name: "end", Type: "string", Required: false},
				{Name: "limit", Type: "int", Required: false},
			},
			Handler: c.handleQueryMetrics,
		},
		{
			Name: "server_health_summary",
			Description: `Get a current health snapshot for a server — latest value for every metric.
Useful for quickly assessing server health without specifying individual metric names.
- server_id: UUID of the server (from list_servers)`,
			Params: []ToolParam{
				{Name: "server_id", Type: "string", Required: true},
			},
			Handler: c.handleHealthSummary,
		},
	}
}

func (c *MetricsConnector) Close() error { return nil }

func (c *MetricsConnector) handleListServers(ctx context.Context, args map[string]any) (string, error) {
	servers, err := c.serverStore.List(ctx)
	if err != nil {
		return "", fmt.Errorf("listing servers: %w", err)
	}

	if len(servers) == 0 {
		return "No monitored servers found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Monitored servers (%d):\n\n", len(servers)))
	for _, s := range servers {
		lastSeen := "never"
		if s.LastSeenAt != nil {
			lastSeen = s.LastSeenAt.Format("2006-01-02 15:04:05 UTC")
		}
		sb.WriteString(fmt.Sprintf("- %s (ID: %s)\n  Status: %s | IP: %s | OS: %s/%s | Last seen: %s\n",
			s.Hostname, s.ID, s.Status, s.IPAddress, s.OS, s.Arch, lastSeen))
	}
	return sb.String(), nil
}

func (c *MetricsConnector) handleQueryMetrics(ctx context.Context, args map[string]any) (string, error) {
	serverIDStr, _ := args["server_id"].(string)
	if serverIDStr == "" {
		return "", fmt.Errorf("server_id is required")
	}
	serverID, err := uuid.Parse(serverIDStr)
	if err != nil {
		return "", fmt.Errorf("invalid server_id: %w", err)
	}

	q := store.MetricQuery{ServerID: serverID}
	if v, ok := args["metric_name"].(string); ok {
		q.MetricName = v
	}
	if v, ok := args["start"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.Start = &t
		}
	}
	if v, ok := args["end"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.End = &t
		}
	}
	if v, ok := args["limit"].(float64); ok {
		q.Limit = int(v)
	}

	points, err := c.metricStore.Query(ctx, q)
	if err != nil {
		return "", fmt.Errorf("querying metrics: %w", err)
	}

	if len(points) == 0 {
		return "No metrics found matching the given criteria.", nil
	}

	data, err := json.MarshalIndent(points, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling metrics: %w", err)
	}

	return fmt.Sprintf("Found %d metric points:\n%s", len(points), string(data)), nil
}

func (c *MetricsConnector) handleHealthSummary(ctx context.Context, args map[string]any) (string, error) {
	serverIDStr, _ := args["server_id"].(string)
	if serverIDStr == "" {
		return "", fmt.Errorf("server_id is required")
	}
	serverID, err := uuid.Parse(serverIDStr)
	if err != nil {
		return "", fmt.Errorf("invalid server_id: %w", err)
	}

	// Get server info
	srv, err := c.serverStore.GetByID(ctx, serverID)
	if err != nil {
		return "", fmt.Errorf("getting server: %w", err)
	}

	// Get latest metrics
	latest, err := c.metricStore.LatestByServer(ctx, serverID)
	if err != nil {
		return "", fmt.Errorf("querying latest metrics: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Server: %s (%s)\n", srv.Hostname, srv.Status))
	sb.WriteString(fmt.Sprintf("OS: %s/%s | IP: %s\n", srv.OS, srv.Arch, srv.IPAddress))
	if srv.LastSeenAt != nil {
		sb.WriteString(fmt.Sprintf("Last seen: %s\n", srv.LastSeenAt.Format("2006-01-02 15:04:05 UTC")))
	}
	sb.WriteString("\nLatest metrics:\n")

	if len(latest) == 0 {
		sb.WriteString("  No metrics available.\n")
	} else {
		for _, m := range latest {
			line := fmt.Sprintf("  %s: %.2f", m.MetricName, m.MetricValue)
			if m.Unit != "" {
				line += " " + m.Unit
			}
			if len(m.Labels) > 0 {
				labelsJSON, _ := json.Marshal(m.Labels)
				line += " " + string(labelsJSON)
			}
			sb.WriteString(line + "\n")
		}
	}

	return sb.String(), nil
}
