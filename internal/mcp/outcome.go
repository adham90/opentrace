package mcp

import "github.com/adham90/opentrace/internal/store"

// OutcomeSignal represents an individual signal contributing to outcome inference.
type OutcomeSignal struct {
	Signal     string  // what happened
	Outcome    string  // inferred outcome: "resolved", "unresolved", "abandoned"
	Confidence float64 // 0.0–1.0
}

// InferOutcome determines the session outcome from subsystem signals.
// Returns the inferred status string.
func InferOutcome(sess *store.InvestigationSession) store.InvestigationSessionStatus {
	if sess == nil {
		return store.InvestigationStatusAbandoned
	}

	// Quick queries are always resolved.
	if sess.TotalSteps <= 2 && sess.Intent == IntentQuery {
		return store.InvestigationStatusResolved
	}

	// Configuration sessions are resolved if something was created.
	if sess.Intent == IntentConfiguration {
		return store.InvestigationStatusResolved
	}

	signals := collectSignals(sess)
	return weightedOutcome(signals)
}

// collectSignals gathers outcome signals from the session's subsystem links.
func collectSignals(sess *store.InvestigationSession) []OutcomeSignal {
	var signals []OutcomeSignal

	// Strong positive signals — something was created/resolved.
	if sess.Summary != "" {
		signals = append(signals, OutcomeSignal{"summary_provided", "resolved", 0.9})
	}

	if sess.TotalSteps == 0 {
		signals = append(signals, OutcomeSignal{"no_steps", "abandoned", 0.9})
		return signals
	}

	// Check tool sequence for resolution patterns.
	seq := sess.ToolSequence

	// If last tool was a resolution action, high confidence.
	if len(seq) > 0 {
		lastTool := seq[len(seq)-1]
		switch lastTool {
		case "resolve_error", "ignore_error":
			signals = append(signals, OutcomeSignal{"error_resolved_action", "resolved", 0.9})
		case "watch", "create_healthcheck":
			signals = append(signals, OutcomeSignal{"monitoring_created", "resolved", 0.8})
		case "set_note":
			signals = append(signals, OutcomeSignal{"note_set", "resolved", 0.7})
		case "set_session_summary":
			signals = append(signals, OutcomeSignal{"summary_set", "resolved", 0.9})
		case "kill_query":
			signals = append(signals, OutcomeSignal{"query_killed", "resolved", 0.7})
		}
	}

	// Successful completion with enough steps.
	if sess.TotalErrors == 0 && sess.TotalSteps >= 3 {
		signals = append(signals, OutcomeSignal{"successful_steps", "resolved", 0.6})
	}

	// Negative signals.
	if sess.TotalSteps > 0 {
		errorRate := float64(sess.TotalErrors) / float64(sess.TotalSteps)
		if errorRate > 0.5 && sess.TotalSteps >= 4 {
			signals = append(signals, OutcomeSignal{"high_error_rate", "unresolved", 0.7})
		}
	}

	// Check for consecutive errors at the end.
	if consecutiveTrailingErrors(seq, sess.TotalErrors, sess.TotalSteps) >= 3 {
		signals = append(signals, OutcomeSignal{"consecutive_failures", "unresolved", 0.8})
	}

	// Short investigation with no resolution signals.
	if sess.TotalSteps <= 2 && sess.Intent == IntentInvestigation && len(signals) == 0 {
		signals = append(signals, OutcomeSignal{"short_investigation", "abandoned", 0.6})
	}

	return signals
}

// weightedOutcome selects the outcome with the highest weighted confidence.
func weightedOutcome(signals []OutcomeSignal) store.InvestigationSessionStatus {
	if len(signals) == 0 {
		return store.InvestigationStatusUnresolved
	}

	scores := map[string]float64{
		"resolved":   0,
		"unresolved": 0,
		"abandoned":  0,
	}

	for _, s := range signals {
		scores[s.Outcome] += s.Confidence
	}

	best := "unresolved"
	bestScore := scores["unresolved"]
	for outcome, score := range scores {
		if score > bestScore {
			best = outcome
			bestScore = score
		}
	}

	switch best {
	case "resolved":
		return store.InvestigationStatusResolved
	case "abandoned":
		return store.InvestigationStatusAbandoned
	default:
		return store.InvestigationStatusUnresolved
	}
}

// consecutiveTrailingErrors estimates consecutive errors at the end of a session.
// Since we don't track per-step error status in the sequence, we use a heuristic:
// if error rate is very high and total errors >= n, assume trailing errors.
func consecutiveTrailingErrors(_ []string, totalErrors, totalSteps int) int {
	if totalSteps == 0 {
		return 0
	}
	// If more than 75% of steps are errors and we have at least 3, treat as consecutive.
	errorRate := float64(totalErrors) / float64(totalSteps)
	if errorRate >= 0.75 && totalErrors >= 3 {
		return totalErrors
	}
	return 0
}
