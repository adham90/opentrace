package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
	"gopkg.in/yaml.v3"
)

func TestOpenAPISpecIsValidYAML(t *testing.T) {
	if len(openapiSpec) == 0 {
		t.Fatal("openapiSpec is empty")
	}

	var doc map[string]any
	if err := yaml.Unmarshal(openapiSpec, &doc); err != nil {
		t.Fatalf("openapiSpec is not valid YAML: %v", err)
	}

	// Verify key OpenAPI fields are present
	if doc["openapi"] == nil {
		t.Error("missing 'openapi' version field")
	}
	if doc["info"] == nil {
		t.Error("missing 'info' field")
	}
	if doc["paths"] == nil {
		t.Error("missing 'paths' field")
	}
	if doc["components"] == nil {
		t.Error("missing 'components' field")
	}

	// Verify it's OpenAPI 3.0.x
	version, ok := doc["openapi"].(string)
	if !ok || !strings.HasPrefix(version, "3.0") {
		t.Errorf("openapi version = %q, want 3.0.x", version)
	}
}

func TestSwaggerUIEndpoint(t *testing.T) {
	srv := NewServer(nil, nil, connector.NewRegistry(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "swagger-ui") {
		t.Error("response body does not contain 'swagger-ui'")
	}
	if !strings.Contains(body, "/api/docs/openapi.yaml") {
		t.Error("response body does not contain spec URL '/api/docs/openapi.yaml'")
	}
}

func TestOpenAPISpecEndpoint(t *testing.T) {
	srv := NewServer(nil, nil, connector.NewRegistry(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/docs/openapi.yaml", nil)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/yaml" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/yaml")
	}

	cacheControl := w.Header().Get("Cache-Control")
	if !strings.Contains(cacheControl, "max-age=3600") {
		t.Errorf("Cache-Control = %q, want to contain 'max-age=3600'", cacheControl)
	}

	// Verify the body is valid YAML
	var doc map[string]any
	if err := yaml.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("response body is not valid YAML: %v", err)
	}
}
