package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/pkg/store"
)

const (
	// errorGroupScanLimit bounds how many error groups a service-status read
	// pulls per environment. active_errors is the number actually found, so
	// the cap is reported alongside it rather than silently distorting it.
	errorGroupScanLimit = 200
	// topErrorsLimit is how many error groups the resource inlines.
	topErrorsLimit = 5
	// serviceListWindow is how far back the service list looks.
	serviceListWindow = 30 * 24 * time.Hour
	// healthcheckWindow is the uptime window of the health-check summary.
	healthcheckWindow = 24 * time.Hour
)

// errNoEnvScope is returned to a caller whose token grants no environment.
// Resources fail closed: no scope means no data, never "all data".
var errNoEnvScope = errors.New("this token has no environment scope — ask an admin to grant access")

// resourceEnvs returns the environments a resource read must be restricted to.
//
// A nil slice means "no filter": either no auth middleware ran (unit tests and
// non-MCP callers, mirroring tools.ResolveEnv's fallback) or the token holds
// the legacy wildcard scope. A non-empty slice means the query must be run
// once per environment and the results merged. An empty scope is denied.
//
// Resources take no arguments, so unlike tools.ResolveEnv there is nothing to
// validate against the scope — the scope itself is the filter.
func resourceEnvs(ctx context.Context) ([]string, error) {
	scope, attached := envscope.FromOK(ctx)
	if !attached || scope.AllowsAll() {
		return nil, nil
	}
	if scope.IsEmpty() {
		return nil, errNoEnvScope
	}
	return append([]string(nil), scope.Allowed...), nil
}

// queryEnvs turns the scope list into the values to pass as the Environment
// filter of a store query. A nil scope becomes a single "" (unfiltered) pass.
func queryEnvs(envs []string) []string {
	if len(envs) == 0 {
		return []string{""}
	}
	return envs
}

// addResources registers MCP resources on the server.
func addResources(s *mcp.Server, deps Deps) {
	// 1. Service status template: opentrace://services/{service}/status
	if deps.ErrorGroupStore != nil || deps.CodeEntityStore != nil {
		s.AddResourceTemplate(
			&mcp.ResourceTemplate{
				URITemplate: "opentrace://services/{service}/status",
				Name:        "Service Status",
				Description: "Live status for a service: error counts, top risks",
				MIMEType:    "application/json",
			},
			serviceStatusHandler(deps),
		)
	}

	// 2. Code risk summary: opentrace://code/risk-summary
	if deps.CodeEntityStore != nil {
		s.AddResource(
			&mcp.Resource{
				URI:         "opentrace://code/risk-summary",
				Name:        "Code Risk Summary",
				Description: "Top 10 risky code entities across all services",
				MIMEType:    "application/json",
			},
			codeRiskSummaryHandler(deps.CodeEntityStore),
		)
	}

	// 3. Current configuration: opentrace://config/current
	if deps.SettingsStore != nil {
		s.AddResource(
			&mcp.Resource{
				URI:         "opentrace://config/current",
				Name:        "Current Configuration",
				Description: "Current configuration (retention settings, MCP name, limits)",
				MIMEType:    "application/json",
			},
			configCurrentHandler(deps.SettingsStore),
		)
	}

	// 5. Service list: opentrace://services/list
	if deps.LogStore != nil {
		s.AddResource(
			&mcp.Resource{
				URI:         "opentrace://services/list",
				Name:        "Service List",
				Description: "List all known service names from logs",
				MIMEType:    "application/json",
			},
			servicesListHandler(deps.LogStore),
		)
	}

	// 6. Connector status: opentrace://connectors/status
	if deps.DSStore != nil {
		s.AddResource(
			&mcp.Resource{
				URI:         "opentrace://connectors/status",
				Name:        "Connector Status",
				Description: "Active connector status",
				MIMEType:    "application/json",
			},
			connectorsStatusHandler(deps.DSStore),
		)
	}

	// 7. Health check summary: opentrace://healthchecks/summary
	if deps.HealthCheckStore != nil {
		s.AddResource(
			&mcp.Resource{
				URI:         "opentrace://healthchecks/summary",
				Name:        "Health Check Summary",
				Description: "Current health check status",
				MIMEType:    "application/json",
			},
			healthchecksSummaryHandler(deps.HealthCheckStore),
		)
	}
}

func serviceStatusHandler(deps Deps) func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := request.Params.URI
		// Extract service from URI: opentrace://services/{service}/status
		service := ""
		if parts := strings.Split(uri, "/"); len(parts) >= 4 {
			service = parts[3]
		}
		if service == "" {
			return nil, fmt.Errorf("service name required in URI")
		}

		envs, err := resourceEnvs(ctx)
		if err != nil {
			return nil, err
		}

		result := map[string]any{
			"service": service,
		}
		if len(envs) > 0 {
			result["environments"] = envs
		}

		// Error count — one query per authorized environment, merged. The
		// scan is bounded, so report whether the count hit the cap instead of
		// pretending the capped number is the total.
		if deps.ErrorGroupStore != nil {
			groups, capped := listServiceErrorGroups(ctx, deps.ErrorGroupStore, service, envs)
			result["active_errors"] = len(groups)
			if capped {
				result["active_errors_capped"] = true
			}
			if len(groups) > topErrorsLimit {
				groups = groups[:topErrorsLimit]
			}
			errorSummaries := make([]map[string]any, 0, len(groups))
			for _, g := range groups {
				errorSummaries = append(errorSummaries, map[string]any{
					"fingerprint":     g.Fingerprint,
					"environment":     g.Environment,
					"exception_class": g.ExceptionClass,
					"total_count":     g.OccurrenceCount,
					"last_seen_at":    g.LastSeenAt.Format(time.RFC3339),
				})
			}
			result["top_errors"] = errorSummaries
		}

		// Top risks. code_entities carries no environment column, so there is
		// nothing to scope here beyond the deny check performed above.
		if deps.CodeEntityStore != nil {
			entities, err := deps.CodeEntityStore.TopByRisk(ctx, service, 3)
			if err == nil {
				riskSummaries := make([]map[string]any, 0, len(entities))
				for _, e := range entities {
					riskSummaries = append(riskSummaries, map[string]any{
						"entity_name": e.EntityName,
						"entity_type": e.EntityType,
						"risk_score":  e.RiskScore,
						"error_count": e.ErrorCount,
					})
				}
				result["top_risks"] = riskSummaries
			}
		}

		data, _ := json.Marshal(result)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: uri, MIMEType: "application/json", Text: string(data)},
			},
		}, nil
	}
}

// listServiceErrorGroups lists unresolved error groups for a service across
// every environment the caller is authorized for, sorted by occurrence count.
// The second return reports whether any per-env scan hit errorGroupScanLimit.
func listServiceErrorGroups(ctx context.Context, egs store.ErrorGroupStore, service string, envs []string) ([]store.ErrorGroup, bool) {
	var all []store.ErrorGroup
	capped := false
	for _, env := range queryEnvs(envs) {
		groups, err := egs.List(ctx, store.ListErrorGroupParams{
			Service:     service,
			Environment: env,
			Status:      store.ErrorGroupUnresolved,
			Limit:       errorGroupScanLimit,
		})
		if err != nil {
			continue
		}
		if len(groups) >= errorGroupScanLimit {
			capped = true
		}
		all = append(all, groups...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].OccurrenceCount > all[j].OccurrenceCount
	})
	return all, capped
}

func codeRiskSummaryHandler(ces store.CodeEntityStore) func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		// code_entities has no environment column; the only scope decision
		// available is whether the token is allowed to read anything at all.
		if _, err := resourceEnvs(ctx); err != nil {
			return nil, err
		}
		entities, err := ces.TopByRisk(ctx, "", 10)
		if err != nil {
			return nil, fmt.Errorf("querying top risks: %w", err)
		}

		result := map[string]any{
			"count":    len(entities),
			"entities": entities,
		}

		data, _ := json.Marshal(result)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: "opentrace://code/risk-summary", MIMEType: "application/json", Text: string(data)},
			},
		}, nil
	}
}

func configCurrentHandler(ss store.SettingsStore) func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		// Instance-wide configuration is not env-partitioned, but a token with
		// no environment scope reads nothing.
		if _, err := resourceEnvs(ctx); err != nil {
			return nil, err
		}

		result := map[string]any{}

		retention, err := ss.GetRetention(ctx)
		if err == nil && retention != nil {
			result["retention_days"] = retention.RetentionDays
			result["metric_retention_days"] = retention.MetricRetentionDays
		}

		maxRows, err := ss.GetMaxQueryRows(ctx)
		if err == nil {
			result["max_query_rows"] = maxRows
		}

		timeout, err := ss.GetStatementTimeout(ctx)
		if err == nil {
			result["statement_timeout_ms"] = timeout
		}

		mcpName, err := ss.GetMCPName(ctx)
		if err == nil {
			result["mcp_name"] = mcpName
		}

		data, _ := json.Marshal(result)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: "opentrace://config/current", MIMEType: "application/json", Text: string(data)},
			},
		}, nil
	}
}

func servicesListHandler(ls store.LogStore) func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		envs, err := resourceEnvs(ctx)
		if err != nil {
			return nil, err
		}

		// CountByService honours the Environment filter (DistinctValues does
		// not), so it is the env-safe way to enumerate service names.
		now := time.Now().UTC()
		seen := make(map[string]bool)
		services := make([]string, 0)
		for _, env := range queryEnvs(envs) {
			counts, err := ls.CountByService(ctx, store.LogCountParams{
				Since:       now.Add(-serviceListWindow),
				Until:       now,
				Environment: env,
			})
			if err != nil {
				return nil, fmt.Errorf("listing services: %w", err)
			}
			for _, c := range counts {
				if c.Service == "" || seen[c.Service] {
					continue
				}
				seen[c.Service] = true
				services = append(services, c.Service)
			}
		}
		sort.Strings(services)

		result := map[string]any{
			"services": services,
			"count":    len(services),
		}
		if len(envs) > 0 {
			result["environments"] = envs
		}

		data, _ := json.Marshal(result)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: "opentrace://services/list", MIMEType: "application/json", Text: string(data)},
			},
		}, nil
	}
}

func connectorsStatusHandler(ds store.DataSourceStore) func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		envs, err := resourceEnvs(ctx)
		if err != nil {
			return nil, err
		}

		connectors := make([]map[string]any, 0)
		seen := make(map[string]bool)
		for _, env := range queryEnvs(envs) {
			sources, err := ds.List(ctx, store.ListDataSourceParams{Environment: env})
			if err != nil {
				return nil, fmt.Errorf("listing connectors: %w", err)
			}
			for _, src := range sources {
				id := src.ID.String()
				if seen[id] {
					continue
				}
				seen[id] = true
				entry := map[string]any{
					"id":     id,
					"name":   src.Name,
					"type":   string(src.Type),
					"status": string(src.Status),
				}
				if src.LastTestedAt != nil {
					entry["last_tested_at"] = src.LastTestedAt.Format(time.RFC3339)
				}
				connectors = append(connectors, entry)
			}
		}

		result := map[string]any{
			"connectors": connectors,
			"count":      len(connectors),
		}
		if len(envs) > 0 {
			result["environments"] = envs
		}

		data, _ := json.Marshal(result)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: "opentrace://connectors/status", MIMEType: "application/json", Text: string(data)},
			},
		}, nil
	}
}

// allowedHealthCheckIDs returns the set of health check IDs the caller may
// see. A nil map means "no restriction" (unscoped or wildcard token).
func allowedHealthCheckIDs(ctx context.Context, hcs store.HealthCheckStore, envs []string) (map[string]bool, error) {
	if len(envs) == 0 {
		return nil, nil
	}
	allowed := make(map[string]bool)
	for _, env := range envs {
		checks, err := hcs.List(ctx, store.ListHealthCheckParams{Environment: env})
		if err != nil {
			return nil, fmt.Errorf("listing health checks: %w", err)
		}
		for _, c := range checks {
			allowed[c.ID] = true
		}
	}
	return allowed, nil
}

func healthchecksSummaryHandler(hcs store.HealthCheckStore) func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		envs, err := resourceEnvs(ctx)
		if err != nil {
			return nil, err
		}

		// UTC: checked_at is stored as RFC3339 'Z' TEXT and compared
		// lexicographically, so a local-zone since string shifts the window.
		summaries, err := hcs.UptimeSummaries(ctx, time.Now().UTC().Add(-healthcheckWindow))
		if err != nil {
			return nil, fmt.Errorf("listing health checks: %w", err)
		}

		// UptimeSummaries has no environment filter, so drop any summary whose
		// health check is outside the caller's scope.
		allowed, err := allowedHealthCheckIDs(ctx, hcs, envs)
		if err != nil {
			return nil, err
		}

		checks := make([]map[string]any, 0, len(summaries))
		for _, s := range summaries {
			if allowed != nil && !allowed[s.HealthCheckID] {
				continue
			}
			checks = append(checks, map[string]any{
				"id":             s.HealthCheckID,
				"name":           s.Name,
				"url":            s.URL,
				"status":         s.CurrentStatus,
				"uptime_percent": s.UptimePct,
				"total_checks":   s.TotalChecks,
				"down_checks":    s.DownChecks,
			})
		}

		result := map[string]any{
			"healthchecks": checks,
			"count":        len(checks),
		}
		if len(envs) > 0 {
			result["environments"] = envs
		}

		data, _ := json.Marshal(result)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: "opentrace://healthchecks/summary", MIMEType: "application/json", Text: string(data)},
			},
		}, nil
	}
}
