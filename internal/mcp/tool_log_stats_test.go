package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// mockLogStore implements store.LogStore for MCP tests.
type mockLogStore struct {
	entries []store.LogEntry
}

func (m *mockLogStore) BatchInsert(_ context.Context, entries []store.LogEntry) (int, error) {
	m.entries = append(m.entries, entries...)
	return len(entries), nil
}

func (m *mockLogStore) Search(_ context.Context, params store.LogSearchParams) ([]store.LogEntry, error) {
	var result []store.LogEntry
	for _, e := range m.entries {
		if params.Query != "" && !strings.Contains(strings.ToLower(e.Message), strings.ToLower(params.Query)) {
			continue
		}
		if params.Level != "" && e.Level != params.Level {
			continue
		}
		if params.Service != "" && e.Service != params.Service {
			continue
		}
		if params.Environment != "" && e.Environment != params.Environment {
			continue
		}
		if params.EventType != "" && e.EventType != params.EventType {
			continue
		}
		if params.Start != nil && e.Timestamp.Before(*params.Start) {
			continue
		}
		if params.End != nil && e.Timestamp.After(*params.End) {
			continue
		}
		result = append(result, e)
		if params.Limit > 0 && len(result) >= params.Limit {
			break
		}
	}
	return result, nil
}

func (m *mockLogStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

func (m *mockLogStore) CountByLevel(_ context.Context, params store.LogCountParams) (map[string]int, error) {
	counts := make(map[string]int)
	for _, e := range m.entries {
		if e.Timestamp.Before(params.Since) || !e.Timestamp.Before(params.Until) {
			continue
		}
		if params.Service != "" && e.Service != params.Service {
			continue
		}
		if params.Level != "" && e.Level != params.Level {
			continue
		}
		if params.Environment != "" && e.Environment != params.Environment {
			continue
		}
		counts[e.Level]++
	}
	return counts, nil
}

func (m *mockLogStore) DistinctValues(_ context.Context, field string, params store.LogCountParams) ([]string, error) {
	seen := make(map[string]bool)
	for _, e := range m.entries {
		if e.Timestamp.Before(params.Since) || !e.Timestamp.Before(params.Until) {
			continue
		}
		var val string
		switch field {
		case "service":
			val = e.Service
		case "level":
			val = e.Level
		case "environment":
			val = e.Environment
		case "event_type":
			val = e.EventType
		}
		if val != "" {
			seen[val] = true
		}
	}
	var result []string
	for v := range seen {
		result = append(result, v)
	}
	return result, nil
}

func (m *mockLogStore) MetadataKeys(_ context.Context, _ store.LogCountParams) ([]string, error) {
	seen := make(map[string]bool)
	for _, e := range m.entries {
		for k := range e.Metadata {
			seen[k] = true
		}
	}
	var result []string
	for k := range seen {
		result = append(result, k)
	}
	return result, nil
}

func (m *mockLogStore) GetByID(_ context.Context, id int64) (*store.LogEntry, error) {
	for i := range m.entries {
		if m.entries[i].ID == id {
			return &m.entries[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockLogStore) RecordBatch(_ context.Context, _ string, _ int) error { return nil }
func (m *mockLogStore) GetBatch(_ context.Context, _ string) (*store.BatchRecord, error) { return nil, nil }
func (m *mockLogStore) PruneBatches(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

func (m *mockLogStore) CountByService(_ context.Context, params store.LogCountParams) ([]store.ServiceLogCount, error) {
	svcMap := make(map[string]*store.ServiceLogCount)
	for _, e := range m.entries {
		if e.Timestamp.Before(params.Since) || !e.Timestamp.Before(params.Until) {
			continue
		}
		if params.Service != "" && e.Service != params.Service {
			continue
		}
		if params.Level != "" && e.Level != params.Level {
			continue
		}
		if params.Environment != "" && e.Environment != params.Environment {
			continue
		}
		sc, ok := svcMap[e.Service]
		if !ok {
			sc = &store.ServiceLogCount{Service: e.Service}
			svcMap[e.Service] = sc
		}
		sc.Total++
		if e.Level == "error" || e.Level == "fatal" {
			sc.ErrorCount++
		}
	}
	var result []store.ServiceLogCount
	for _, sc := range svcMap {
		result = append(result, *sc)
	}
	return result, nil
}

func TestLogStatsHandler_GroupByLevel(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{Level: "info", Service: "web", Timestamp: now.Add(-10 * time.Minute)},
			{Level: "info", Service: "web", Timestamp: now.Add(-9 * time.Minute)},
			{Level: "error", Service: "web", Timestamp: now.Add(-8 * time.Minute)},
			{Level: "warn", Service: "api", Timestamp: now.Add(-7 * time.Minute)},
			{Level: "fatal", Service: "api", Timestamp: now.Add(-6 * time.Minute)},
		},
	}

	handler := logStatsHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"time_range": "1h",
		"group_by":   "level",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["total_logs"].(float64) != 5 {
		t.Errorf("total_logs = %v, want 5", resp["total_logs"])
	}

	byLevel, ok := resp["by_level"].(map[string]any)
	if !ok {
		t.Fatal("expected by_level field")
	}
	if byLevel["info"].(float64) != 2 {
		t.Errorf("info = %v, want 2", byLevel["info"])
	}
	if byLevel["error"].(float64) != 1 {
		t.Errorf("error = %v, want 1", byLevel["error"])
	}

	// error_rate_pct: (1 error + 1 fatal) / 5 total = 40%
	errRate := resp["error_rate_pct"].(float64)
	if errRate != 40 {
		t.Errorf("error_rate_pct = %v, want 40", errRate)
	}
}

func TestLogStatsHandler_GroupByService(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{Level: "info", Service: "web-api", Timestamp: now.Add(-10 * time.Minute)},
			{Level: "error", Service: "web-api", Timestamp: now.Add(-9 * time.Minute)},
			{Level: "info", Service: "worker", Timestamp: now.Add(-8 * time.Minute)},
			{Level: "info", Service: "worker", Timestamp: now.Add(-7 * time.Minute)},
			{Level: "error", Service: "worker", Timestamp: now.Add(-6 * time.Minute)},
			{Level: "error", Service: "worker", Timestamp: now.Add(-5 * time.Minute)},
		},
	}

	handler := logStatsHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"time_range": "1h",
		"group_by":   "service",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["total_logs"].(float64) != 6 {
		t.Errorf("total_logs = %v, want 6", resp["total_logs"])
	}

	byService, ok := resp["by_service"].([]any)
	if !ok {
		t.Fatal("expected by_service field")
	}
	if len(byService) < 2 {
		t.Fatalf("expected at least 2 services, got %d", len(byService))
	}
}

func TestLogStatsHandler_GroupByPattern(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.10 port 5432", Timestamp: now.Add(-10 * time.Minute)},
			{Level: "error", Service: "web", Message: "connection refused to 192.168.1.20 port 5432", Timestamp: now.Add(-9 * time.Minute)},
			{Level: "error", Service: "worker", Message: "connection refused to 192.168.1.10 port 5432", Timestamp: now.Add(-8 * time.Minute)},
			{Level: "error", Service: "web", Message: "context deadline exceeded after 30 seconds", Timestamp: now.Add(-7 * time.Minute)},
			{Level: "error", Service: "api", Message: "context deadline exceeded after 60 seconds", Timestamp: now.Add(-6 * time.Minute)},
		},
	}

	handler := logStatsHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"time_range": "1h",
		"group_by":   "pattern",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["total_errors"].(float64) != 5 {
		t.Errorf("total_errors = %v, want 5", resp["total_errors"])
	}

	patterns, ok := resp["patterns"].([]any)
	if !ok {
		t.Fatal("expected patterns field")
	}
	// "connection refused to * port *" and "context deadline exceeded after * seconds" should cluster
	if len(patterns) != 2 {
		t.Errorf("expected 2 patterns, got %d", len(patterns))
	}

	// Top pattern should be "connection refused" with count 3.
	top := patterns[0].(map[string]any)
	if top["count"].(float64) != 3 {
		t.Errorf("top pattern count = %v, want 3", top["count"])
	}

	// Should have a dominant pattern warning (3/5 = 60% > 50%).
	warnings, _ := resp["warnings"].([]any)
	if len(warnings) == 0 {
		t.Error("expected dominant pattern warning")
	}
}

func TestLogStatsHandler_EmptyLogs(t *testing.T) {
	ls := &mockLogStore{}

	handler := logStatsHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"time_range": "1h",
		"group_by":   "level",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["total_logs"].(float64) != 0 {
		t.Errorf("total_logs = %v, want 0", resp["total_logs"])
	}
}

func TestLogStatsHandler_ServiceFilter(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{Level: "info", Service: "web", Timestamp: now.Add(-10 * time.Minute)},
			{Level: "error", Service: "web", Timestamp: now.Add(-9 * time.Minute)},
			{Level: "info", Service: "worker", Timestamp: now.Add(-8 * time.Minute)},
		},
	}

	handler := logStatsHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"time_range": "1h",
		"group_by":   "level",
		"service":    "web",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Only web logs should be counted.
	if resp["total_logs"].(float64) != 2 {
		t.Errorf("total_logs = %v, want 2", resp["total_logs"])
	}
}

func TestLogStatsHandler_InvalidTimeRange(t *testing.T) {
	ls := &mockLogStore{}

	handler := logStatsHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"time_range": "banana",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid time_range")
	}
}

func TestLogStatsHandler_InvalidGroupBy(t *testing.T) {
	ls := &mockLogStore{}

	handler := logStatsHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"group_by": "banana",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid group_by")
	}
}

func TestNormalizeMessage(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			"connection refused to 192.168.1.100:5432",
			"connection refused to *:*",
		},
		{
			"user abc12345-abcd-1234-abcd-1234abcd5678 not found",
			"user * not found",
		},
		{
			`failed to process "order #12345"`,
			`failed to process "*"`,
		},
		{
			"timeout after 30 seconds for request 42",
			"timeout after * seconds for request *",
		},
	}
	for _, tt := range tests {
		got := normalizeMessage(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeMessage(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestParseTimeRange(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"15m", 15 * time.Minute, false},
		{"1h", time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"", time.Hour, false},
		{"banana", 0, true},
	}
	for _, tt := range tests {
		got, err := parseTimeRange(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseTimeRange(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.expected {
			t.Errorf("parseTimeRange(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}
