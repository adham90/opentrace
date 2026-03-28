package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

func testServer() *mcpsdk.Server {
	return mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: "test", Version: "0.0.1"},
		&mcpsdk.ServerOptions{},
	)
}

func testDeps() Deps {
	return Deps{
		Stores: store.Stores{
			ErrorGroupStore:           mocks.NewErrorGroupStore(),
			DeployStore:               mocks.NewDeployStore(),
			CodeEntityStore:           mocks.NewCodeEntityStore(),
			InvestigationSessionStore: mocks.NewInvestigationSessionStore(),
			SettingsStore:             mocks.NewSettingsStore(),
			LogStore:                  mocks.NewLogStore(),
			DSStore:                   mocks.NewDataSourceStore(),
			HealthCheckStore:          mocks.NewHealthCheckStore(),
		},
	}
}

func TestAddResources_NilDeps(t *testing.T) {
	addResources(testServer(), Deps{})
}

func TestAddResources_WithAllStores(t *testing.T) {
	addResources(testServer(), testDeps())
}

func TestServiceStatusHandler_ValidService(t *testing.T) {
	handler := serviceStatusHandler(testDeps())
	req := &mcpsdk.ReadResourceRequest{
		Params: &mcpsdk.ReadResourceParams{URI: "opentrace://services/my-service/status"},
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if data["service"] != "my-service" {
		t.Errorf("service = %v, want %q", data["service"], "my-service")
	}
}

func TestCodeRiskSummaryHandler_EmptyStore(t *testing.T) {
	result, err := codeRiskSummaryHandler(mocks.NewCodeEntityStore())(context.Background(), &mcpsdk.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
}

func TestActiveInvestigationsHandler_EmptyStore(t *testing.T) {
	result, err := activeInvestigationsHandler(mocks.NewInvestigationSessionStore())(context.Background(), &mcpsdk.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
}

func TestConfigCurrentHandler_ReturnsJSON(t *testing.T) {
	result, err := configCurrentHandler(mocks.NewSettingsStore())(context.Background(), &mcpsdk.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Contents[0].MIMEType != "application/json" {
		t.Errorf("mime = %q, want application/json", result.Contents[0].MIMEType)
	}
}

func TestServicesListHandler_EmptyStore(t *testing.T) {
	result, err := servicesListHandler(mocks.NewLogStore())(context.Background(), &mcpsdk.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || len(result.Contents) == 0 {
		t.Fatal("expected content")
	}
}

func TestConnectorsStatusHandler_EmptyStore(t *testing.T) {
	result, err := connectorsStatusHandler(mocks.NewDataSourceStore())(context.Background(), &mcpsdk.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || len(result.Contents) == 0 {
		t.Fatal("expected content")
	}
}

func TestHealthchecksSummaryHandler_EmptyStore(t *testing.T) {
	result, err := healthchecksSummaryHandler(mocks.NewHealthCheckStore())(context.Background(), &mcpsdk.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || len(result.Contents) == 0 {
		t.Fatal("expected content")
	}
}
