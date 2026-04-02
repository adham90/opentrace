package tools

import (
	"context"
	"fmt"


	"github.com/adham90/opentrace/pkg/store"
)

// TestGenDeps holds stores needed by the test generation tool.
type TestGenDeps struct {
	ErrorGroupStore  store.ErrorGroupStore
	ErrorImpactStore store.ErrorImpactStore
	CodeEntityStore  store.CodeEntityStore
	LogStore         store.LogStore
}

// TestGenHandler returns a handler for the test generation tool.
func TestGenHandler(d TestGenDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action := ArgString(args, "action")

		switch action {
		case "context":
			return HandleTestGenContext(ctx, d, args)
		case "suggest":
			return HandleTestGenSuggest(ctx, d, args)
		default:
			return NewToolResultError("unknown action: " + action + ". Use: context, suggest"), nil
		}
	}
}

func HandleTestGenContext(ctx context.Context, d TestGenDeps, args map[string]any) (*CallToolResult, error) {
	fingerprint := ArgString(args, "fingerprint")
	if fingerprint == "" {
		return NewToolResultError("fingerprint is required for context action"), nil
	}

	if d.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	// Get the error group
	eg, err := d.ErrorGroupStore.Get(ctx, fingerprint)
	if err != nil {
		return NewToolResultError("error group not found: " + fingerprint), nil
	}

	result := map[string]any{
		"error_group": map[string]any{
			"fingerprint":      eg.Fingerprint,
			"exception_class":  eg.ExceptionClass,
			"message":          eg.Message,
			"occurrences":      eg.OccurrenceCount,
			"first_seen":       eg.FirstSeenAt.Format("2006-01-02T15:04:05Z"),
			"last_seen":        eg.LastSeenAt.Format("2006-01-02T15:04:05Z"),
			"service":          eg.Service,
			"status":           string(eg.Status),
		},
	}

	// Source location
	if eg.SourceFile != "" {
		result["source_location"] = map[string]any{
			"file": eg.SourceFile,
			"line": eg.SourceLine,
		}
	}

	// User impact
	if d.ErrorImpactStore != nil {
		impact, err := d.ErrorImpactStore.GetImpact(ctx, fingerprint)
		if err == nil && impact != nil {
			result["impact"] = map[string]any{
				"unique_users":   impact.UniqueUsers,
				"impact_score":   impact.ImpactScore,
				"common_traits":  impact.CommonTraits,
			}
		}
	}

	// Edge case analysis
	funcName := eg.SourceFile
	if idx := len(funcName) - 1; idx > 0 {
		// Use filename as fallback function name
	}
	result["edge_case"] = map[string]any{
		"description":         fmt.Sprintf("%s in %s", eg.ExceptionClass, eg.SourceFile),
		"trigger":             eg.Message,
		"suggested_test_name": fmt.Sprintf("Test_%s", sanitizeTestName(eg.ExceptionClass)),
		"suggested_assertion": fmt.Sprintf("Should handle %s gracefully without raising %s", describeEdgeCase(eg.Message), eg.ExceptionClass),
	}
	_ = funcName

	// Suggested test comment
	result["test_comment"] = fmt.Sprintf(
		"Regression test from production error group %s\n// First seen: %s\n// Occurrences: %d\n// Service: %s",
		eg.Fingerprint,
		eg.FirstSeenAt.Format("2006-01-02"),
		eg.OccurrenceCount,
		eg.Service,
	)

	return JSONResult(result)
}

func HandleTestGenSuggest(ctx context.Context, d TestGenDeps, args map[string]any) (*CallToolResult, error) {
	if d.ErrorGroupStore == nil {
		return NewToolResultError("ErrorGroupStore not configured"), nil
	}

	limit := ArgInt(args, "limit", 10, 30)
	service := ArgString(args, "service")

	// Get unresolved errors sorted by occurrence (most impactful first)
	groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
		Service: service,
		Status:  store.ErrorGroupUnresolved,
		Limit:   limit,
		SortBy:  "occurrence_count",
	})
	if err != nil {
		return NewToolResultError("failed to list errors: " + err.Error()), nil
	}

	var suggestions []map[string]any
	for i, g := range groups {
		priority := "medium"
		if g.OccurrenceCount > 100 {
			priority = "critical"
		} else if g.OccurrenceCount > 20 {
			priority = "high"
		}

		suggestion := map[string]any{
			"rank":            i + 1,
			"fingerprint":     g.Fingerprint,
			"exception_class": g.ExceptionClass,
			"message":         Truncate(g.Message, 80),
			"service":         g.Service,
			"occurrences":     g.OccurrenceCount,
			"priority":        priority,
			"source_file":     g.SourceFile,
		}

		suggestions = append(suggestions, suggestion)
	}

	return JSONResult(map[string]any{
		"suggestions": suggestions,
		"count":       len(suggestions),
		"tip":         "Use test_gen(action: \"context\", fingerprint: \"...\") to get test-ready data for any error.",
	})
}


func sanitizeTestName(s string) string {
	// Simple: replace non-alphanumeric with underscore
	var result []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			result = append(result, c)
		} else {
			result = append(result, '_')
		}
	}
	return string(result)
}

func describeEdgeCase(message string) string {
	msg := Truncate(message, 60)
	if msg == "" {
		return "this edge case"
	}
	return "the case where: " + msg
}
