package mcp

import (
	"fmt"

	"github.com/adham90/opentrace/pkg/store"
)

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
