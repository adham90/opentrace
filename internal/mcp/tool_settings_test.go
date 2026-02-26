package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/store"
)

// mockSettingsStore implements store.SettingsStore for testing.
type mockSettingsStore struct {
	retention *store.RetentionSettings
	err       error
}

func (m *mockSettingsStore) GetRetention(_ context.Context) (*store.RetentionSettings, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.retention != nil {
		return m.retention, nil
	}
	return &store.RetentionSettings{RetentionDays: 30}, nil
}

func (m *mockSettingsStore) SetRetention(_ context.Context, s store.RetentionSettings) error {
	if m.err != nil {
		return m.err
	}
	m.retention = &s
	return nil
}

func (m *mockSettingsStore) GetAPIKey(_ context.Context) (string, error)          { return "", nil }
func (m *mockSettingsStore) SetAPIKey(_ context.Context, _ string) error          { return nil }
func (m *mockSettingsStore) GetCORSOrigins(_ context.Context) (string, error)     { return "", nil }
func (m *mockSettingsStore) SetCORSOrigins(_ context.Context, _ string) error     { return nil }
func (m *mockSettingsStore) GetMaxQueryRows(_ context.Context) (int, error)       { return 0, nil }
func (m *mockSettingsStore) SetMaxQueryRows(_ context.Context, _ int) error       { return nil }
func (m *mockSettingsStore) GetStatementTimeout(_ context.Context) (int, error)   { return 0, nil }
func (m *mockSettingsStore) SetStatementTimeout(_ context.Context, _ int) error   { return nil }
func (m *mockSettingsStore) GetMCPName(_ context.Context) (string, error)         { return "", nil }
func (m *mockSettingsStore) SetMCPName(_ context.Context, _ string) error         { return nil }
func (m *mockSettingsStore) GetSamplingRules(_ context.Context) ([]store.SamplingRule, error) {
	return nil, nil
}
func (m *mockSettingsStore) SetSamplingRules(_ context.Context, _ []store.SamplingRule) error {
	return nil
}

// --- get_settings tests ---

func TestGetSettingsHandler_Success(t *testing.T) {
	ss := &mockSettingsStore{
		retention: &store.RetentionSettings{RetentionDays: 90},
	}
	handler := getSettingsHandler(ss)

	result, err := handler(context.Background(), makeRequest(nil))
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

	if resp["retention_days"] != float64(90) {
		t.Errorf("retention_days = %v, want 90", resp["retention_days"])
	}
}

func TestGetSettingsHandler_Error(t *testing.T) {
	ss := &mockSettingsStore{err: errors.New("db error")}
	handler := getSettingsHandler(ss)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}

// --- update_retention tests ---

func TestUpdateRetentionHandler_Success(t *testing.T) {
	ss := &mockSettingsStore{}
	handler := updateRetentionHandler(ss)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"retention_days": float64(60),
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "60 days") {
		t.Errorf("expected '60 days' in response, got: %s", text)
	}

	if ss.retention == nil || ss.retention.RetentionDays != 60 {
		t.Errorf("retention not updated correctly: %v", ss.retention)
	}
}

func TestUpdateRetentionHandler_InvalidRange(t *testing.T) {
	ss := &mockSettingsStore{}
	handler := updateRetentionHandler(ss)

	tests := []struct {
		name string
		args map[string]any
	}{
		{"too low", map[string]any{"retention_days": float64(0)}},
		{"too high", map[string]any{"retention_days": float64(500)}},
		{"missing", map[string]any{}},
		{"negative", map[string]any{"retention_days": float64(-1)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := handler(context.Background(), makeRequest(tc.args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Fatal("expected error for invalid retention_days")
			}
		})
	}
}

func TestUpdateRetentionHandler_StoreError(t *testing.T) {
	ss := &mockSettingsStore{err: errors.New("write failed")}
	handler := updateRetentionHandler(ss)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"retention_days": float64(30),
	}))

	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when store fails")
	}
}
