package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// mockHealthCheckStore implements store.HealthCheckStore for testing.
type mockHealthCheckStore struct {
	checks  map[string]*store.HealthCheck
	results map[string][]store.HealthCheckResult
	nextID  int
	err     error
}

func newMockHealthCheckStore() *mockHealthCheckStore {
	return &mockHealthCheckStore{
		checks:  make(map[string]*store.HealthCheck),
		results: make(map[string][]store.HealthCheckResult),
	}
}

func (m *mockHealthCheckStore) Create(_ context.Context, params store.CreateHealthCheckParams) (*store.HealthCheck, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.nextID++
	id := "hc_" + string(rune('0'+m.nextID))
	method := params.Method
	if method == "" {
		method = "GET"
	}
	interval := params.IntervalSecs
	if interval <= 0 {
		interval = 60
	}
	timeout := params.TimeoutSecs
	if timeout <= 0 {
		timeout = 10
	}
	expected := params.ExpectedStatus
	if expected <= 0 {
		expected = 200
	}
	hc := &store.HealthCheck{
		ID:             id,
		Name:           params.Name,
		URL:            params.URL,
		Method:         method,
		IntervalSecs:   interval,
		TimeoutSecs:    timeout,
		ExpectedStatus: expected,
		Enabled:        true,
		CreatedAt:      time.Now(),
	}
	m.checks[id] = hc
	return hc, nil
}

func (m *mockHealthCheckStore) Get(_ context.Context, id string) (*store.HealthCheck, error) {
	if m.err != nil {
		return nil, m.err
	}
	hc, ok := m.checks[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return hc, nil
}

func (m *mockHealthCheckStore) List(_ context.Context) ([]store.HealthCheck, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []store.HealthCheck
	for _, hc := range m.checks {
		result = append(result, *hc)
	}
	return result, nil
}

func (m *mockHealthCheckStore) Delete(_ context.Context, id string) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.checks[id]; !ok {
		return store.ErrNotFound
	}
	delete(m.checks, id)
	delete(m.results, id)
	return nil
}

func (m *mockHealthCheckStore) SetEnabled(_ context.Context, id string, enabled bool) error {
	if m.err != nil {
		return m.err
	}
	hc, ok := m.checks[id]
	if !ok {
		return store.ErrNotFound
	}
	hc.Enabled = enabled
	return nil
}

func (m *mockHealthCheckStore) RecordResult(_ context.Context, result store.HealthCheckResult) error {
	if m.err != nil {
		return m.err
	}
	m.results[result.HealthCheckID] = append(m.results[result.HealthCheckID], result)
	return nil
}

func (m *mockHealthCheckStore) LatestResults(_ context.Context, healthcheckID string, limit int) ([]store.HealthCheckResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	results := m.results[healthcheckID]
	if len(results) > limit {
		results = results[len(results)-limit:]
	}
	return results, nil
}

func (m *mockHealthCheckStore) UptimeSummaries(_ context.Context, since time.Time) ([]store.UptimeSummary, error) {
	if m.err != nil {
		return nil, m.err
	}
	var summaries []store.UptimeSummary
	for _, hc := range m.checks {
		us := store.UptimeSummary{
			HealthCheckID: hc.ID,
			Name:          hc.Name,
			URL:           hc.URL,
			CurrentStatus: "up",
			UptimePct:     100.0,
			TotalChecks:   10,
		}
		summaries = append(summaries, us)
	}
	return summaries, nil
}

func (m *mockHealthCheckStore) PruneResults(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

// --- Tests ---

func TestListHealthchecksHandler_Empty(t *testing.T) {
	hcs := newMockHealthCheckStore()
	handler := listHealthchecksHandler(hcs)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if text != "No health checks configured. Use create_healthcheck to add one." {
		t.Errorf("unexpected text: %s", text)
	}
}

func TestListHealthchecksHandler_WithChecks(t *testing.T) {
	hcs := newMockHealthCheckStore()
	hcs.Create(context.Background(), store.CreateHealthCheckParams{
		Name: "My API", URL: "https://api.example.com/health",
	})

	handler := listHealthchecksHandler(hcs)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}
	if count := resp["count"].(float64); count != 1 {
		t.Errorf("count = %v, want 1", count)
	}
}

func TestCreateHealthcheckHandler_Success(t *testing.T) {
	hcs := newMockHealthCheckStore()
	handler := createHealthcheckHandler(hcs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"name": "Production API",
		"url":  "https://api.example.com/health",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}
	if resp["name"] != "Production API" {
		t.Errorf("name = %v, want Production API", resp["name"])
	}
	if resp["enabled"] != true {
		t.Errorf("enabled = %v, want true", resp["enabled"])
	}
}

func TestCreateHealthcheckHandler_MissingName(t *testing.T) {
	hcs := newMockHealthCheckStore()
	handler := createHealthcheckHandler(hcs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"url": "https://example.com",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing name")
	}
}

func TestCreateHealthcheckHandler_MissingURL(t *testing.T) {
	hcs := newMockHealthCheckStore()
	handler := createHealthcheckHandler(hcs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"name": "My Check",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing URL")
	}
}

func TestDeleteHealthcheckHandler_Success(t *testing.T) {
	hcs := newMockHealthCheckStore()
	hc, _ := hcs.Create(context.Background(), store.CreateHealthCheckParams{
		Name: "To Delete", URL: "https://del.com",
	})

	handler := deleteHealthcheckHandler(hcs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"id": hc.ID,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["status"] != "deleted" {
		t.Errorf("status = %v, want deleted", resp["status"])
	}
}

func TestDeleteHealthcheckHandler_NotFound(t *testing.T) {
	hcs := newMockHealthCheckStore()
	handler := deleteHealthcheckHandler(hcs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"id": "nonexistent",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for nonexistent check")
	}
}

func TestUptimeStatusHandler_Success(t *testing.T) {
	hcs := newMockHealthCheckStore()
	hcs.Create(context.Background(), store.CreateHealthCheckParams{
		Name: "API", URL: "https://api.example.com",
	})

	handler := uptimeStatusHandler(hcs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"hours": float64(24),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["count"].(float64) != 1 {
		t.Errorf("count = %v, want 1", resp["count"])
	}
}

func TestUptimeStatusHandler_Empty(t *testing.T) {
	hcs := newMockHealthCheckStore()
	handler := uptimeStatusHandler(hcs)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if text != "No health checks configured. Use create_healthcheck to add one." {
		t.Errorf("unexpected text: %s", text)
	}
}

func TestListHealthchecksHandler_NilStore(t *testing.T) {
	handler := listHealthchecksHandler(nil)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for nil store")
	}
}
