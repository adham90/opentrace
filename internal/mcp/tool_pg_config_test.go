package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestPgConfigCheckHandler_Success(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		result: &connector.QueryResult{
			Columns: []string{"name", "setting", "unit", "category", "short_desc", "source", "boot_val", "reset_val"},
			Rows: [][]any{
				{"shared_buffers", "16384", "8kB", "Resource Usage / Memory", "Sets shared memory buffers", "configuration file", "1024", "16384"},
				{"autovacuum", "on", nil, "Autovacuum", "Starts the autovacuum subprocess", "default", "on", "on"},
				{"work_mem", "4096", "kB", "Resource Usage / Memory", "Sets work memory", "default", "4096", "4096"},
			},
			RowCount: 3,
		},
	})

	handler := pgConfigCheckHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "shared_buffers") {
		t.Errorf("expected shared_buffers in output, got: %s", text)
	}
	if !contains(text, "settings") {
		t.Errorf("expected settings key in output, got: %s", text)
	}
}

func TestPgConfigCheckHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := pgConfigCheckHandler(registry)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector")
	}
	text := resultText(t, result)
	if !contains(text, "No database connector") {
		t.Errorf("expected no-connector message, got: %s", text)
	}
}

func TestPgConfigCheckHandler_QueryError(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            errors.New("connection refused"),
	})

	handler := pgConfigCheckHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}

func TestCheckConfigWarning_AutovacuumOff(t *testing.T) {
	warnings := checkConfigWarning("autovacuum", "off", "")
	if len(warnings) == 0 {
		t.Error("expected warning when autovacuum is off")
	}
	if !contains(warnings[0], "CRITICAL") {
		t.Errorf("expected CRITICAL warning, got: %s", warnings[0])
	}
}

func TestCheckConfigWarning_LowSharedBuffers(t *testing.T) {
	warnings := checkConfigWarning("shared_buffers", "1024", "8kB")
	if len(warnings) == 0 {
		t.Error("expected warning for low shared_buffers")
	}
}

func TestCheckConfigWarning_HighRandomPageCost(t *testing.T) {
	warnings := checkConfigWarning("random_page_cost", "4", "")
	if len(warnings) == 0 {
		t.Error("expected warning for high random_page_cost")
	}
}

func TestCheckConfigWarning_NoWarning(t *testing.T) {
	warnings := checkConfigWarning("shared_buffers", "131072", "8kB") // 1GB
	if len(warnings) != 0 {
		t.Errorf("expected no warning for adequate shared_buffers, got: %v", warnings)
	}
}
