package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// setSessionSummaryHandler returns the handler for the set_session_summary tool.
func setSessionSummaryHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		args := request.GetArguments()

		summary, _ := args["summary"].(string)
		rootCause, _ := args["root_cause"].(string)
		fixApplied, _ := args["fix_applied"].(string)
		outcome, _ := args["outcome"].(string)
		primaryService, _ := args["primary_service"].(string)

		if summary == "" {
			return mcplib.NewToolResultError("summary is required"), nil
		}

		if sessionTracker == nil {
			return mcplib.NewToolResultError("session tracking is not enabled"), nil
		}

		// Map outcome string to status.
		var status *store.InvestigationSessionStatus
		if outcome != "" {
			switch outcome {
			case "resolved":
				s := store.InvestigationStatusResolved
				status = &s
			case "unresolved":
				s := store.InvestigationStatusUnresolved
				status = &s
			case "partial":
				s := store.InvestigationStatusUnresolved
				status = &s
			default:
				s := store.InvestigationSessionStatus(outcome)
				status = &s
			}
		}

		params := store.UpdateInvestigationSessionParams{
			Summary: &summary,
		}
		if rootCause != "" {
			params.RootCause = &rootCause
		}
		if fixApplied != "" {
			params.FixDescription = &fixApplied
		}
		if status != nil {
			params.Status = status
		}
		if primaryService != "" {
			params.PrimaryService = &primaryService
		}

		sessionTracker.UpdateSession(params)

		resp := map[string]any{
			"status":  "saved",
			"message": "Session summary recorded. This will help future investigations of similar issues.",
		}
		if sessID := sessionTracker.CurrentSessionID(); sessID != "" {
			resp["session_id"] = sessID
		}

		data, _ := json.Marshal(resp)
		return mcplib.NewToolResultText(string(data)), nil
	}
}

// finalizeSessionOutcome is called when a session closes. It infers the
// outcome if no summary was provided, and updates the session status.
func finalizeSessionOutcome(st *SessionTracker) {
	sess := st.CurrentSession()
	if sess == nil {
		return
	}

	// If summary was already provided via set_session_summary, don't override.
	if sess.Summary != "" {
		return
	}

	// Infer outcome from signals.
	inferredStatus := InferOutcome(sess)
	params := store.UpdateInvestigationSessionParams{}

	statusVal := inferredStatus
	params.Status = &statusVal

	// Set a basic auto-generated summary based on tool sequence.
	if len(sess.ToolSequence) > 0 && sess.Intent == IntentInvestigation {
		autoSummary := fmt.Sprintf("Investigation using %d tools (%s). Outcome: %s.",
			sess.TotalSteps, sess.ToolFingerprint, string(inferredStatus))
		params.Summary = &autoSummary
	}

	st.UpdateSession(params)
}
