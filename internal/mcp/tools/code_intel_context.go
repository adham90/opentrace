package tools

import (
	"context"

	"github.com/adham90/opentrace/pkg/store"
)

func buildDebuggingContext(ctx context.Context, d CodeIntelDeps, service string, result map[string]any) {
	if d.ErrorGroupStore != nil {
		params := store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  5,
		}
		if service != "" {
			params.Service = service
		}
		if groups, err := d.ErrorGroupStore.List(ctx, params); err == nil && len(groups) > 0 {
			result["error_groups"] = groups
		}
	}

	if d.DeployStore != nil {
		if deploys, err := d.DeployStore.GetRecent(ctx, service, 3); err == nil && len(deploys) > 0 {
			result["recent_deploys"] = deploys
		}
	}

	if d.CodeEntityStore != nil {
		if entities, err := d.CodeEntityStore.TopByRisk(ctx, service, 3); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}

	if d.InvestigationSessionStore != nil {
		params := store.ListInvestigationSessionParams{
			Status: store.InvestigationStatusOpen,
			Limit:  5,
		}
		if service != "" {
			params.Service = service
		}
		if sessions, err := d.InvestigationSessionStore.List(ctx, params); err == nil && len(sessions) > 0 {
			result["active_investigations"] = sessions
		}
	}

	if d.AgentNoteStore != nil && service != "" {
		if note, err := d.AgentNoteStore.Get(ctx, "service", service); err == nil && note != nil {
			result["agent_notes"] = note.Note
		}
	}
}

func buildDeployingContext(ctx context.Context, d CodeIntelDeps, service string, result map[string]any) {
	if d.CodeEntityStore != nil {
		if entities, err := d.CodeEntityStore.TopByRisk(ctx, service, 5); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}

	if d.DeployStore != nil {
		if deploys, err := d.DeployStore.GetRecent(ctx, service, 5); err == nil && len(deploys) > 0 {
			result["recent_deploys"] = deploys
		}
	}

	if d.TestCorrelationStore != nil {
		if paths, err := d.TestCorrelationStore.TopByPriority(ctx, service, 5); err == nil && len(paths) > 0 {
			result["uncovered_paths"] = paths
		}
	}
}

func buildTestingContext(ctx context.Context, d CodeIntelDeps, service string, result map[string]any) {
	if d.TestCorrelationStore != nil {
		if paths, err := d.TestCorrelationStore.TopByPriority(ctx, service, 10); err == nil && len(paths) > 0 {
			result["uncovered_paths"] = paths
		}
	}

	if d.ErrorGroupStore != nil {
		params := store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  5,
		}
		if service != "" {
			params.Service = service
		}
		if groups, err := d.ErrorGroupStore.List(ctx, params); err == nil && len(groups) > 0 {
			result["error_groups"] = groups
		}
	}

	if d.CodeEntityStore != nil {
		if entities, err := d.CodeEntityStore.TopByRisk(ctx, service, 5); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}
}

func buildReviewingContext(ctx context.Context, d CodeIntelDeps, service string, result map[string]any) {
	if d.CodeEntityStore != nil {
		if entities, err := d.CodeEntityStore.TopByRisk(ctx, service, 5); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}

	if d.ErrorGroupStore != nil {
		params := store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  5,
		}
		if service != "" {
			params.Service = service
		}
		if groups, err := d.ErrorGroupStore.List(ctx, params); err == nil && len(groups) > 0 {
			result["recent_errors"] = groups
		}
	}

	if d.DeployStore != nil {
		if deploys, err := d.DeployStore.GetRecent(ctx, service, 3); err == nil && len(deploys) > 0 {
			result["related_deploys"] = deploys
		}
	}
}

func buildEditingContext(ctx context.Context, d CodeIntelDeps, service string, result map[string]any) {
	if d.CodeEntityStore != nil {
		if entities, err := d.CodeEntityStore.TopByRisk(ctx, service, 3); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}

	if d.AgentNoteStore != nil && service != "" {
		if note, err := d.AgentNoteStore.Get(ctx, "service", service); err == nil && note != nil {
			result["agent_notes"] = note.Note
		}
	}

	if d.ErrorGroupStore != nil {
		params := store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  3,
		}
		if service != "" {
			params.Service = service
		}
		if groups, err := d.ErrorGroupStore.List(ctx, params); err == nil && len(groups) > 0 {
			result["recent_errors"] = groups
		}
	}
}
