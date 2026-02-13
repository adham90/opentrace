package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/store"
)

func TestAcknowledgeAlertHandler_Success(t *testing.T) {
	alertID := uuid.New()
	as := &mockAlertStore{
		alerts: []store.Alert{
			{ID: alertID, Title: "Test Alert", Severity: store.SeverityWarning, Read: false},
		},
	}
	handler := acknowledgeAlertHandler(as)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"alert_id": alertID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "acknowledged successfully") {
		t.Errorf("expected 'acknowledged successfully', got: %s", text)
	}
}

func TestAcknowledgeAlertHandler_AlreadyRead(t *testing.T) {
	alertID := uuid.New()
	as := &mockAlertStore{
		alerts: []store.Alert{
			{ID: alertID, Title: "Test Alert", Severity: store.SeverityWarning, Read: true},
		},
	}
	handler := acknowledgeAlertHandler(as)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"alert_id": alertID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "already acknowledged") {
		t.Errorf("expected 'already acknowledged', got: %s", text)
	}
}

func TestAcknowledgeAlertHandler_NotFound(t *testing.T) {
	as := &mockAlertStore{}
	handler := acknowledgeAlertHandler(as)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"alert_id": uuid.New().String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing alert")
	}

	text := resultText(t, result)
	if !contains(text, "alert not found") {
		t.Errorf("expected 'alert not found', got: %s", text)
	}
}

func TestAcknowledgeAlertHandler_MissingID(t *testing.T) {
	as := &mockAlertStore{}
	handler := acknowledgeAlertHandler(as)

	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing alert_id")
	}
}

func TestDismissAlertHandler_Success(t *testing.T) {
	alertID := uuid.New()
	as := &mockAlertStore{
		alerts: []store.Alert{
			{ID: alertID, Title: "Test Alert", Severity: store.SeverityCritical, Dismissed: false},
		},
	}
	handler := dismissAlertHandler(as)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"alert_id": alertID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "dismissed successfully") {
		t.Errorf("expected 'dismissed successfully', got: %s", text)
	}
}

func TestDismissAlertHandler_AlreadyDismissed(t *testing.T) {
	alertID := uuid.New()
	as := &mockAlertStore{
		alerts: []store.Alert{
			{ID: alertID, Title: "Test Alert", Severity: store.SeverityWarning, Dismissed: true},
		},
	}
	handler := dismissAlertHandler(as)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"alert_id": alertID.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "already dismissed") {
		t.Errorf("expected 'already dismissed', got: %s", text)
	}
}

func TestAcknowledgeAllAlertsHandler(t *testing.T) {
	as := &mockAlertStore{
		alerts: []store.Alert{
			{ID: uuid.New(), Title: "Alert 1", CreatedAt: time.Now()},
			{ID: uuid.New(), Title: "Alert 2", CreatedAt: time.Now()},
		},
	}
	handler := acknowledgeAllAlertsHandler(as)

	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "All alerts acknowledged") {
		t.Errorf("expected 'All alerts acknowledged', got: %s", text)
	}
}

func TestDismissAllAlertsHandler(t *testing.T) {
	as := &mockAlertStore{}
	handler := dismissAllAlertsHandler(as)

	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "All alerts dismissed") {
		t.Errorf("expected 'All alerts dismissed', got: %s", text)
	}
}
