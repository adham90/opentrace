package connector

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
	"github.com/google/uuid"
)

// --- mock stores ---

type mockServerStore struct {
	servers []store.Server
	err     error
}

func (m *mockServerStore) Register(_ context.Context, _ store.RegisterServerParams) (*store.Server, error) {
	return nil, m.err
}

func (m *mockServerStore) GetByID(_ context.Context, id uuid.UUID) (*store.Server, error) {
	if m.err != nil {
		return nil, m.err
	}
	for i := range m.servers {
		if m.servers[i].ID == id {
			return &m.servers[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockServerStore) List(_ context.Context, _ store.ListServerParams) ([]store.Server, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.servers, nil
}

func (m *mockServerStore) Update(_ context.Context, _ uuid.UUID, _ store.UpdateServerParams) (*store.Server, error) {
	return nil, m.err
}

func (m *mockServerStore) UpdateHeartbeat(_ context.Context, _ uuid.UUID) error {
	return m.err
}

func (m *mockServerStore) Delete(_ context.Context, _ uuid.UUID) error {
	return m.err
}

func (m *mockServerStore) MarkStaleOffline(_ context.Context, _ time.Duration) (int, error) {
	return 0, m.err
}

type mockMetricStore struct {
	points []store.MetricPoint
	err    error
}

func (m *mockMetricStore) BatchInsert(_ context.Context, _ uuid.UUID, _ time.Time, _ []store.MetricSample) (int, error) {
	return 0, m.err
}

func (m *mockMetricStore) Query(_ context.Context, _ store.MetricQuery) ([]store.MetricPoint, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.points, nil
}

func (m *mockMetricStore) LatestByServer(_ context.Context, _ uuid.UUID) ([]store.MetricPoint, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.points, nil
}

func (m *mockMetricStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, m.err
}

// --- constructor tests ---

func TestNewMetricsConnector(t *testing.T) {
	ss := &mockServerStore{}
	ms := &mockMetricStore{}
	c := NewMetricsConnector(ss, ms)
	if c == nil {
		t.Fatal("NewMetricsConnector returned nil")
	}
	if c.serverStore != ss {
		t.Error("serverStore not set correctly")
	}
	if c.metricStore != ms {
		t.Error("metricStore not set correctly")
	}
}

// --- Type() ---

func TestMetricsConnector_Type(t *testing.T) {
	c := NewMetricsConnector(&mockServerStore{}, &mockMetricStore{})
	if c.Type() != ConnectorServerMetrics {
		t.Errorf("Type() = %q, want %q", c.Type(), ConnectorServerMetrics)
	}
}

// --- Tools() ---

func TestMetricsConnector_Tools(t *testing.T) {
	c := NewMetricsConnector(&mockServerStore{}, &mockMetricStore{})
	tools := c.Tools()
	if len(tools) != 3 {
		t.Fatalf("len(Tools()) = %d, want 3", len(tools))
	}

	expectedNames := map[string]bool{
		"list_servers":         true,
		"query_server_metrics": true,
		"server_health_summary": true,
	}
	for _, tool := range tools {
		if !expectedNames[tool.Name] {
			t.Errorf("unexpected tool name: %q", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		if tool.Handler == nil {
			t.Errorf("tool %q has nil handler", tool.Name)
		}
	}
}

// --- Close() ---

func TestMetricsConnector_Close(t *testing.T) {
	c := NewMetricsConnector(&mockServerStore{}, &mockMetricStore{})
	if err := c.Close(); err != nil {
		t.Errorf("Close() returned unexpected error: %v", err)
	}
}

// --- TestConnection ---

func TestMetricsConnector_TestConnection(t *testing.T) {
	c := NewMetricsConnector(&mockServerStore{}, &mockMetricStore{})
	err := c.TestConnection(context.Background())
	if err != nil {
		t.Errorf("TestConnection() returned unexpected error: %v", err)
	}
}

func TestMetricsConnector_TestConnection_Error(t *testing.T) {
	ss := &mockServerStore{err: store.ErrNotFound}
	c := NewMetricsConnector(ss, &mockMetricStore{})
	err := c.TestConnection(context.Background())
	if err == nil {
		t.Error("TestConnection() should return error when store fails")
	}
}

// --- handleListServers ---

func TestMetricsConnector_ListServers_Empty(t *testing.T) {
	c := NewMetricsConnector(&mockServerStore{servers: nil}, &mockMetricStore{})
	tools := c.Tools()
	var listTool Tool
	for _, tool := range tools {
		if tool.Name == "list_servers" {
			listTool = tool
			break
		}
	}

	result, err := listTool.Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !searchStr(result, "No monitored servers found") {
		t.Errorf("expected 'No monitored servers found', got: %s", result)
	}
}

func TestMetricsConnector_ListServers_WithServers(t *testing.T) {
	now := time.Now().UTC()
	ss := &mockServerStore{
		servers: []store.Server{
			{
				ID:         uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				Hostname:   "web-1",
				IPAddress:  "10.0.0.1",
				OS:         "linux",
				Arch:       "amd64",
				Status:     store.ServerOnline,
				LastSeenAt: &now,
			},
			{
				ID:        uuid.MustParse("22222222-2222-2222-2222-222222222222"),
				Hostname:  "db-1",
				IPAddress: "10.0.0.2",
				OS:        "linux",
				Arch:      "arm64",
				Status:    store.ServerOffline,
			},
		},
	}
	c := NewMetricsConnector(ss, &mockMetricStore{})
	tools := c.Tools()
	var listTool Tool
	for _, tool := range tools {
		if tool.Name == "list_servers" {
			listTool = tool
			break
		}
	}

	result, err := listTool.Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !searchStr(result, "web-1") {
		t.Errorf("result should contain hostname 'web-1': %s", result)
	}
	if !searchStr(result, "db-1") {
		t.Errorf("result should contain hostname 'db-1': %s", result)
	}
	if !searchStr(result, "Monitored servers (2)") {
		t.Errorf("result should contain server count: %s", result)
	}
	// Server without LastSeenAt should show "never"
	if !searchStr(result, "never") {
		t.Errorf("server without LastSeenAt should show 'never': %s", result)
	}
}

// --- handleQueryMetrics ---

func TestMetricsConnector_QueryMetrics_MissingServerID(t *testing.T) {
	c := NewMetricsConnector(&mockServerStore{}, &mockMetricStore{})
	tools := c.Tools()
	var queryTool Tool
	for _, tool := range tools {
		if tool.Name == "query_server_metrics" {
			queryTool = tool
			break
		}
	}

	_, err := queryTool.Handler(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing server_id")
	}
}

func TestMetricsConnector_QueryMetrics_InvalidServerID(t *testing.T) {
	c := NewMetricsConnector(&mockServerStore{}, &mockMetricStore{})
	tools := c.Tools()
	var queryTool Tool
	for _, tool := range tools {
		if tool.Name == "query_server_metrics" {
			queryTool = tool
			break
		}
	}

	_, err := queryTool.Handler(context.Background(), map[string]any{
		"server_id": "not-a-uuid",
	})
	if err == nil {
		t.Fatal("expected error for invalid server_id")
	}
}

func TestMetricsConnector_QueryMetrics_NoResults(t *testing.T) {
	c := NewMetricsConnector(&mockServerStore{}, &mockMetricStore{points: nil})
	tools := c.Tools()
	var queryTool Tool
	for _, tool := range tools {
		if tool.Name == "query_server_metrics" {
			queryTool = tool
			break
		}
	}

	result, err := queryTool.Handler(context.Background(), map[string]any{
		"server_id": "11111111-1111-1111-1111-111111111111",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !searchStr(result, "No metrics found") {
		t.Errorf("expected 'No metrics found', got: %s", result)
	}
}

func TestMetricsConnector_QueryMetrics_WithResults(t *testing.T) {
	serverID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	ms := &mockMetricStore{
		points: []store.MetricPoint{
			{
				ServerID:    serverID,
				Timestamp:   time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
				MetricName:  "cpu.usage_percent",
				MetricValue: 72.5,
				Unit:        "%",
			},
		},
	}
	c := NewMetricsConnector(&mockServerStore{}, ms)
	tools := c.Tools()
	var queryTool Tool
	for _, tool := range tools {
		if tool.Name == "query_server_metrics" {
			queryTool = tool
			break
		}
	}

	result, err := queryTool.Handler(context.Background(), map[string]any{
		"server_id":   serverID.String(),
		"metric_name": "cpu.usage_percent",
		"limit":       float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !searchStr(result, "Found 1 metric points") {
		t.Errorf("result should contain count: %s", result)
	}
	if !searchStr(result, "cpu.usage_percent") {
		t.Errorf("result should contain metric name: %s", result)
	}
}

// --- handleHealthSummary ---

func TestMetricsConnector_HealthSummary_MissingServerID(t *testing.T) {
	c := NewMetricsConnector(&mockServerStore{}, &mockMetricStore{})
	tools := c.Tools()
	var healthTool Tool
	for _, tool := range tools {
		if tool.Name == "server_health_summary" {
			healthTool = tool
			break
		}
	}

	_, err := healthTool.Handler(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing server_id")
	}
}

func TestMetricsConnector_HealthSummary_ServerNotFound(t *testing.T) {
	ss := &mockServerStore{servers: nil}
	c := NewMetricsConnector(ss, &mockMetricStore{})
	tools := c.Tools()
	var healthTool Tool
	for _, tool := range tools {
		if tool.Name == "server_health_summary" {
			healthTool = tool
			break
		}
	}

	_, err := healthTool.Handler(context.Background(), map[string]any{
		"server_id": "11111111-1111-1111-1111-111111111111",
	})
	if err == nil {
		t.Fatal("expected error when server not found")
	}
}

func TestMetricsConnector_HealthSummary_Success(t *testing.T) {
	serverID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	now := time.Now().UTC()
	ss := &mockServerStore{
		servers: []store.Server{
			{
				ID:         serverID,
				Hostname:   "web-1",
				IPAddress:  "10.0.0.1",
				OS:         "linux",
				Arch:       "amd64",
				Status:     store.ServerOnline,
				LastSeenAt: &now,
			},
		},
	}
	ms := &mockMetricStore{
		points: []store.MetricPoint{
			{
				ServerID:    serverID,
				MetricName:  "cpu.usage_percent",
				MetricValue: 45.2,
				Unit:        "%",
			},
			{
				ServerID:    serverID,
				MetricName:  "memory.usage_percent",
				MetricValue: 62.8,
				Unit:        "%",
				Labels:      map[string]string{"host": "web-1"},
			},
		},
	}
	c := NewMetricsConnector(ss, ms)
	tools := c.Tools()
	var healthTool Tool
	for _, tool := range tools {
		if tool.Name == "server_health_summary" {
			healthTool = tool
			break
		}
	}

	result, err := healthTool.Handler(context.Background(), map[string]any{
		"server_id": serverID.String(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !searchStr(result, "web-1") {
		t.Errorf("result should contain hostname: %s", result)
	}
	if !searchStr(result, "cpu.usage_percent") {
		t.Errorf("result should contain cpu metric: %s", result)
	}
	if !searchStr(result, "memory.usage_percent") {
		t.Errorf("result should contain memory metric: %s", result)
	}
	if !searchStr(result, "62.80") {
		t.Errorf("result should contain formatted metric value: %s", result)
	}
}

func TestMetricsConnector_HealthSummary_NoMetrics(t *testing.T) {
	serverID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	ss := &mockServerStore{
		servers: []store.Server{
			{
				ID:       serverID,
				Hostname: "web-1",
				Status:   store.ServerUnknown,
			},
		},
	}
	ms := &mockMetricStore{points: []store.MetricPoint{}}
	c := NewMetricsConnector(ss, ms)
	tools := c.Tools()
	var healthTool Tool
	for _, tool := range tools {
		if tool.Name == "server_health_summary" {
			healthTool = tool
			break
		}
	}

	result, err := healthTool.Handler(context.Background(), map[string]any{
		"server_id": serverID.String(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !searchStr(result, "No metrics available") {
		t.Errorf("result should indicate no metrics: %s", result)
	}
}
