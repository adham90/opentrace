package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// ContextInjector enriches tool responses with investigation memory.
type ContextInjector struct {
	sessionStore    store.InvestigationSessionStore
	transitionStore store.ToolTransitionStore
}

// NewContextInjector creates a new ContextInjector.
func NewContextInjector(ss store.InvestigationSessionStore, ts store.ToolTransitionStore) *ContextInjector {
	return &ContextInjector{sessionStore: ss, transitionStore: ts}
}

// InvestigationContext holds contextual investigation memory.
type InvestigationContext struct {
	SimilarPastSessions    []SessionSummary  `json:"similar_past_sessions,omitempty"`
	DeadEnds               []DeadEndWarning  `json:"dead_ends,omitempty"`
	ParallelInvestigations []ParallelInfo    `json:"parallel_investigations,omitempty"`
}

// SessionSummary is a compact representation of a past session.
type SessionSummary struct {
	SessionID   string `json:"session_id"`
	Intent      string `json:"intent"`
	Status      string `json:"status"`
	Summary     string `json:"summary,omitempty"`
	RootCause   string `json:"root_cause,omitempty"`
	TotalSteps  int    `json:"total_steps"`
	Service     string `json:"service,omitempty"`
}

// ParallelInfo describes another active investigation on the same service.
type ParallelInfo struct {
	SessionID string `json:"session_id"`
	UserEmail string `json:"user_email,omitempty"`
	Intent    string `json:"intent"`
	Steps     int    `json:"steps"`
}

// BuildContext constructs investigation context for the current session.
// Returns nil for non-investigation intents or when no context is available.
func (ci *ContextInjector) BuildContext(ctx context.Context, sess *store.InvestigationSession, toolName string) *InvestigationContext {
	if sess == nil || sess.Intent != IntentInvestigation {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ic := &InvestigationContext{}

	// 1. Similar past sessions
	similar, err := ci.sessionStore.FindSimilar(ctx, store.FindSimilarParams{
		Service:          sess.PrimaryService,
		Intent:           sess.Intent,
		ToolFingerprint:  sess.ToolFingerprint,
		ExcludeSessionID: sess.ID,
		MaxResults:       3,
		MinSteps:         3,
		OnlyResolved:     true,
	})
	if err != nil {
		slog.Debug("context_injector: FindSimilar failed", "error", err)
	} else {
		for _, s := range similar {
			summary := SessionSummary{
				SessionID:  s.ID,
				Intent:     s.Intent,
				Status:     string(s.Status),
				Summary:    truncate(s.Summary, 200),
				RootCause:  truncate(s.RootCause, 200),
				TotalSteps: s.TotalSteps,
				Service:    s.PrimaryService,
			}
			ic.SimilarPastSessions = append(ic.SimilarPastSessions, summary)
		}
	}

	// 2. Dead-ends from transition data
	if ci.transitionStore != nil {
		transitions, err := ci.transitionStore.GetDeadEnds(ctx, sess.Intent)
		if err != nil {
			slog.Debug("context_injector: dead-end query failed", "error", err)
		} else if len(transitions) > 0 {
			if len(transitions) > 3 {
				transitions = transitions[:3]
			}
			for _, t := range transitions {
				ic.DeadEnds = append(ic.DeadEnds, DeadEndWarning{
					FromTool: t.FromTool,
					ToTool:   t.ToTool,
					Reason:   fmt.Sprintf("This transition was abandoned %d/%d times", t.AbandonedCount, t.TotalCount),
				})
			}
		}
	}

	// 3. Parallel investigations (same service, currently open, different session)
	if sess.PrimaryService != "" {
		recent, err := ci.sessionStore.List(ctx, store.ListInvestigationSessionParams{
			Status:  store.InvestigationStatusOpen,
			Service: sess.PrimaryService,
			Limit:   5,
		})
		if err != nil {
			slog.Debug("context_injector: parallel investigation query failed", "error", err)
		} else {
			for _, s := range recent {
				if s.ID == sess.ID {
					continue
				}
				ic.ParallelInvestigations = append(ic.ParallelInvestigations, ParallelInfo{
					SessionID: s.ID,
					UserEmail: s.UserEmail,
					Intent:    s.Intent,
					Steps:     s.TotalSteps,
				})
			}
		}
	}

	// Return nil if nothing useful was found
	if len(ic.SimilarPastSessions) == 0 && len(ic.DeadEnds) == 0 && len(ic.ParallelInvestigations) == 0 {
		return nil
	}

	// Cap total JSON size to ~2KB
	if data, err := json.Marshal(ic); err == nil && len(data) > 2048 {
		// Truncate similar sessions until we fit
		for len(ic.SimilarPastSessions) > 0 {
			ic.SimilarPastSessions = ic.SimilarPastSessions[:len(ic.SimilarPastSessions)-1]
			if data, err = json.Marshal(ic); err == nil && len(data) <= 2048 {
				break
			}
		}
	}

	return ic
}

// InjectContext adds investigation_context to a tool response map.
func (ci *ContextInjector) InjectContext(resp map[string]any, ic *InvestigationContext) {
	if ic != nil {
		resp["investigation_context"] = ic
	}
}

// InjectContextIntoResult parses, enriches, and re-serializes the tool result
// for investigation-intent sessions.
func InjectContextIntoResult(ci *ContextInjector, sess *store.InvestigationSession, toolName string, resultText string) string {
	if ci == nil || sess == nil || sess.Intent != IntentInvestigation {
		return resultText
	}

	var respMap map[string]any
	if err := json.Unmarshal([]byte(resultText), &respMap); err != nil {
		return resultText
	}

	ic := ci.BuildContext(context.Background(), sess, toolName)
	ci.InjectContext(respMap, ic)

	newData, err := json.Marshal(respMap)
	if err != nil {
		return resultText
	}
	return string(newData)
}

// FormatDeadEndWarning returns a human-readable warning string.
func FormatDeadEndWarning(w DeadEndWarning) string {
	return fmt.Sprintf("Warning: %s → %s — %s", w.FromTool, w.ToTool, w.Reason)
}
