package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Env-aware fakes: each embeds the shared stub and overrides only the method
// the resource handlers call, honouring the Environment filter the way the
// sqlite stores do.
// ---------------------------------------------------------------------------

type envErrorGroupStore struct {
	*mocks.ErrorGroupStore
	byEnv map[string][]store.ErrorGroup
}

func (s *envErrorGroupStore) List(_ context.Context, params store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	var out []store.ErrorGroup
	for env, groups := range s.byEnv {
		if params.Environment != "" && params.Environment != env {
			continue
		}
		for _, g := range groups {
			if params.Service != "" && params.Service != g.Service {
				continue
			}
			out = append(out, g)
		}
	}
	return out, nil
}

type envLogStore struct {
	*mocks.LogStore
	byEnv map[string][]string
}

func (s *envLogStore) CountByService(_ context.Context, params store.LogCountParams) ([]store.ServiceLogCount, error) {
	var out []store.ServiceLogCount
	for env, services := range s.byEnv {
		if params.Environment != "" && params.Environment != env {
			continue
		}
		for _, svc := range services {
			out = append(out, store.ServiceLogCount{Service: svc, Total: 1})
		}
	}
	return out, nil
}

type envDataSourceStore struct {
	*mocks.DataSourceStore
	byEnv map[string][]store.DataSource
}

func (s *envDataSourceStore) List(_ context.Context, params store.ListDataSourceParams) ([]store.DataSource, error) {
	var out []store.DataSource
	for env, sources := range s.byEnv {
		if params.Environment != "" && params.Environment != env {
			continue
		}
		out = append(out, sources...)
	}
	return out, nil
}

type envHealthCheckStore struct {
	*mocks.HealthCheckStore
	byEnv map[string][]store.HealthCheck
	since time.Time
}

func (s *envHealthCheckStore) List(_ context.Context, params store.ListHealthCheckParams) ([]store.HealthCheck, error) {
	var out []store.HealthCheck
	for env, checks := range s.byEnv {
		if params.Environment != "" && params.Environment != env {
			continue
		}
		out = append(out, checks...)
	}
	return out, nil
}

// UptimeSummaries deliberately ignores the environment (as the real store
// does) so the handler must do the filtering itself.
func (s *envHealthCheckStore) UptimeSummaries(_ context.Context, since time.Time) ([]store.UptimeSummary, error) {
	s.since = since
	var out []store.UptimeSummary
	for _, checks := range s.byEnv {
		for _, c := range checks {
			out = append(out, store.UptimeSummary{HealthCheckID: c.ID, Name: c.Name, URL: c.URL})
		}
	}
	return out, nil
}

func scopedDeps() (Deps, *envHealthCheckStore) {
	hcs := &envHealthCheckStore{
		HealthCheckStore: mocks.NewHealthCheckStore(),
		byEnv: map[string][]store.HealthCheck{
			"staging":    {{ID: "hc-staging", Name: "staging-api", URL: "https://staging.internal/health"}},
			"production": {{ID: "hc-prod", Name: "prod-api", URL: "https://prod.internal/health"}},
		},
	}
	deps := Deps{
		Stores: store.Stores{
			ErrorGroupStore: &envErrorGroupStore{
				ErrorGroupStore: mocks.NewErrorGroupStore(),
				byEnv: map[string][]store.ErrorGroup{
					"staging": {{Fingerprint: "fp-staging", Environment: "staging", Service: "checkout", ExceptionClass: "StagingError"}},
					"production": {
						{Fingerprint: "fp-prod-1", Environment: "production", Service: "checkout", ExceptionClass: "ProdError"},
						{Fingerprint: "fp-prod-2", Environment: "production", Service: "checkout", ExceptionClass: "ProdError2"},
					},
				},
			},
			LogStore: &envLogStore{
				LogStore: mocks.NewLogStore(),
				byEnv: map[string][]string{
					"staging":    {"checkout-staging"},
					"production": {"checkout-prod"},
				},
			},
			DSStore: &envDataSourceStore{
				DataSourceStore: mocks.NewDataSourceStore(),
				byEnv: map[string][]store.DataSource{
					"staging":    {{ID: uuid.New(), Name: "staging-db"}},
					"production": {{ID: uuid.New(), Name: "prod-db"}},
				},
			},
			HealthCheckStore: hcs,
		},
	}
	return deps, hcs
}

func stagingCtx() context.Context {
	return envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"staging"}})
}

func readResource(t *testing.T, h func(context.Context, *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error), ctx context.Context, uri string) (string, error) {
	t.Helper()
	res, err := h(ctx, &mcpsdk.ReadResourceRequest{Params: &mcpsdk.ReadResourceParams{URI: uri}})
	if err != nil {
		return "", err
	}
	if res == nil || len(res.Contents) == 0 {
		t.Fatal("expected resource contents")
	}
	return res.Contents[0].Text, nil
}

// TestResources_EnvScopedTokenCannotReadOtherEnv is the core authorization
// test: a token scoped to staging must not see production data through any
// resource.
func TestResources_EnvScopedTokenCannotReadOtherEnv(t *testing.T) {
	deps, _ := scopedDeps()
	ctx := stagingCtx()

	t.Run("service status", func(t *testing.T) {
		text, err := readResource(t, serviceStatusHandler(deps), ctx, "opentrace://services/checkout/status")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(text, "fp-prod") || strings.Contains(text, "ProdError") {
			t.Errorf("staging token leaked production error groups: %s", text)
		}
		if !strings.Contains(text, "fp-staging") {
			t.Errorf("staging token should still see staging errors: %s", text)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			t.Fatalf("bad JSON: %v", err)
		}
		if data["active_errors"] != 1.0 {
			t.Errorf("active_errors = %v, want 1", data["active_errors"])
		}
	})

	t.Run("service list", func(t *testing.T) {
		text, err := readResource(t, servicesListHandler(deps.LogStore), ctx, "opentrace://services/list")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(text, "checkout-prod") {
			t.Errorf("staging token leaked production service names: %s", text)
		}
		if !strings.Contains(text, "checkout-staging") {
			t.Errorf("staging services missing: %s", text)
		}
	})

	t.Run("connectors", func(t *testing.T) {
		text, err := readResource(t, connectorsStatusHandler(deps.DSStore), ctx, "opentrace://connectors/status")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(text, "prod-db") {
			t.Errorf("staging token leaked production connectors: %s", text)
		}
		if !strings.Contains(text, "staging-db") {
			t.Errorf("staging connectors missing: %s", text)
		}
	})

	t.Run("healthchecks", func(t *testing.T) {
		text, err := readResource(t, healthchecksSummaryHandler(deps.HealthCheckStore), ctx, "opentrace://healthchecks/summary")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(text, "prod.internal") || strings.Contains(text, "hc-prod") {
			t.Errorf("staging token leaked production health checks: %s", text)
		}
		if !strings.Contains(text, "hc-staging") {
			t.Errorf("staging health checks missing: %s", text)
		}
	})
}

// TestResources_EmptyScopeIsDenied — a token with no environments reads
// nothing, including the non-env-partitioned resources.
func TestResources_EmptyScopeIsDenied(t *testing.T) {
	deps, _ := scopedDeps()
	ctx := envscope.With(context.Background(), envscope.EnvScope{})

	handlers := map[string]func(context.Context, *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error){
		"service status": serviceStatusHandler(deps),
		"service list":   servicesListHandler(deps.LogStore),
		"connectors":     connectorsStatusHandler(deps.DSStore),
		"healthchecks":   healthchecksSummaryHandler(deps.HealthCheckStore),
		"code risk":      codeRiskSummaryHandler(mocks.NewCodeEntityStore()),
		"config":         configCurrentHandler(mocks.NewSettingsStore()),
	}
	for name, h := range handlers {
		res, err := h(ctx, &mcpsdk.ReadResourceRequest{Params: &mcpsdk.ReadResourceParams{URI: "opentrace://services/checkout/status"}})
		if err == nil {
			t.Errorf("%s: expected denial for empty scope, got %v", name, res)
		}
	}
}

// TestResources_WildcardScopeSeesEverything keeps the legacy wildcard token
// working.
func TestResources_WildcardScopeSeesEverything(t *testing.T) {
	deps, _ := scopedDeps()
	ctx := envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{envscope.WildcardEnv}})

	text, err := readResource(t, serviceStatusHandler(deps), ctx, "opentrace://services/checkout/status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "fp-prod-1") || !strings.Contains(text, "fp-staging") {
		t.Errorf("wildcard token should see all envs: %s", text)
	}
}

// TestServiceStatus_ActiveErrorsIsNotCappedAtFive covers the min(count, 5) bug:
// the reported count must be the real number, with only the inline list
// trimmed.
func TestServiceStatus_ActiveErrorsIsNotCappedAtFive(t *testing.T) {
	groups := make([]store.ErrorGroup, 0, 12)
	for i := 0; i < 12; i++ {
		groups = append(groups, store.ErrorGroup{
			Fingerprint:     string(rune('a' + i)),
			Environment:     "production",
			Service:         "api",
			OccurrenceCount: i,
		})
	}
	deps := Deps{Stores: store.Stores{ErrorGroupStore: &envErrorGroupStore{
		ErrorGroupStore: mocks.NewErrorGroupStore(),
		byEnv:           map[string][]store.ErrorGroup{"production": groups},
	}}}

	text, err := readResource(t, serviceStatusHandler(deps), context.Background(), "opentrace://services/api/status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if data["active_errors"] != 12.0 {
		t.Errorf("active_errors = %v, want 12", data["active_errors"])
	}
	top, _ := data["top_errors"].([]any)
	if len(top) != topErrorsLimit {
		t.Errorf("top_errors length = %d, want %d", len(top), topErrorsLimit)
	}
}

// TestHealthchecksSummary_UsesUTCWindow — the store compares RFC3339 TEXT
// lexicographically against 'Z' timestamps, so the since value must be UTC.
func TestHealthchecksSummary_UsesUTCWindow(t *testing.T) {
	_, hcs := scopedDeps()
	if _, err := readResource(t, healthchecksSummaryHandler(hcs), context.Background(), "opentrace://healthchecks/summary"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hcs.since.Location() != time.UTC {
		t.Errorf("since location = %v, want UTC", hcs.since.Location())
	}
	if formatted := hcs.since.Format(time.RFC3339); !strings.HasSuffix(formatted, "Z") {
		t.Errorf("since = %s, want a Z-suffixed UTC timestamp", formatted)
	}
}
