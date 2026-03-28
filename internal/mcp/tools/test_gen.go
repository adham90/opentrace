package tools

import (
	"context"
	"fmt"


	"github.com/adham90/opentrace/pkg/store"
)

// TestGenDeps holds stores needed by the test generation tool.
type TestGenDeps struct {
	ErrorGroupStore      store.ErrorGroupStore
	ErrorImpactStore     store.ErrorImpactStore
	CodeEntityStore      store.CodeEntityStore
	TestCorrelationStore store.TestCorrelationStore
	LogStore             store.LogStore
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
		case "coverage":
			return HandleTestGenCoverage(ctx, d, args)
		default:
			return NewToolResultError("unknown action: " + action + ". Use: context, suggest, coverage"), nil
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
			"rank":             i + 1,
			"fingerprint":      g.Fingerprint,
			"exception_class":  g.ExceptionClass,
			"message":          Truncate(g.Message, 80),
			"service":          g.Service,
			"occurrences":      g.OccurrenceCount,
			"priority":         priority,
			"source_file":      g.SourceFile,
		}

		// Check if test coverage exists
		if d.TestCorrelationStore != nil {
			corr, err := d.TestCorrelationStore.GetByFingerprint(ctx, g.Fingerprint)
			if err == nil && corr != nil {
				suggestion["has_coverage"] = true
				suggestion["coverage_source"] = corr.SourceFile
			} else {
				suggestion["has_coverage"] = false
			}
		}

		suggestions = append(suggestions, suggestion)
	}

	return JSONResult(map[string]any{
		"suggestions": suggestions,
		"count":       len(suggestions),
		"tip":         "Use test_gen(action: \"context\", fingerprint: \"...\") to get test-ready data for any error.",
	})
}

func HandleTestGenCoverage(ctx context.Context, d TestGenDeps, args map[string]any) (*CallToolResult, error) {
	limit := ArgInt(args, "limit", 10, 30)
	service := ArgString(args, "service")

	var gaps []map[string]any

	// Get uncovered error paths from TestCorrelationStore
	if d.TestCorrelationStore != nil {
		paths, err := d.TestCorrelationStore.TopByPriority(ctx, service, limit)
		if err == nil {
			for _, p := range paths {
				gaps = append(gaps, map[string]any{
					"fingerprint":     p.ErrorFingerprint,
					"exception_class": p.ErrorClass,
					"service":         p.Service,
					"error_count":     p.ErrorCount,
					"source_file":     p.SourceFile,
					"priority_score":  p.PriorityScore,
				})
			}
		}
	}

	if len(gaps) == 0 {
		// Fallback: list unresolved errors that likely lack tests
		if d.ErrorGroupStore != nil {
			groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
				Service: service,
				Status:  store.ErrorGroupUnresolved,
				Limit:   limit,
				SortBy:  "occurrence_count",
			})
			if err == nil {
				for _, g := range groups {
					gaps = append(gaps, map[string]any{
						"fingerprint":     g.Fingerprint,
						"exception_class": g.ExceptionClass,
						"service":         g.Service,
						"occurrences":     g.OccurrenceCount,
						"source_file":     g.SourceFile,
						"note":            "No test correlation data available — listed by occurrence count",
					})
				}
			}
		}
	}

	return JSONResult(map[string]any{
		"uncovered_errors": gaps,
		"count":            len(gaps),
		"tip":              "Use test_gen(action: \"context\", fingerprint: \"...\") to get test-ready data for any error.",
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
