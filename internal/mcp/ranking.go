package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// RankingService replaces static tool suggestions with data-driven rankings.
type RankingService struct {
	transitionStore store.ToolTransitionStore
	templateStore   store.WorkflowTemplateStore
}

// NewRankingService creates a new RankingService.
func NewRankingService(ts store.ToolTransitionStore, ws store.WorkflowTemplateStore) *RankingService {
	return &RankingService{transitionStore: ts, templateStore: ws}
}

// RankingRequest describes the current session state for ranking.
type RankingRequest struct {
	CurrentTool         string
	Intent              string
	StepIndex           int
	SessionTools        []string
	FallbackSuggestions []ToolSuggestion
}

// DeadEndWarning describes a tool transition that frequently leads to abandonment.
type DeadEndWarning struct {
	FromTool string `json:"from_tool"`
	ToTool   string `json:"to_tool"`
	Reason   string `json:"reason"`
}

// RankSuggestions returns ranked suggestions using learned transitions,
// falling back to workflow templates, then static suggestions.
func (r *RankingService) RankSuggestions(ctx context.Context, req RankingRequest) []ToolSuggestion {
	if r.transitionStore == nil {
		return req.FallbackSuggestions
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// 1. Try learned transitions
	if req.CurrentTool != "" {
		transitions, err := r.transitionStore.GetTransitions(ctx, store.GetTransitionsParams{
			FromTool:   req.CurrentTool,
			Intent:     req.Intent,
			MinSupport: 3,
			MaxAgeDays: 90,
		})
		if err != nil {
			slog.Debug("ranking: transition query failed", "error", err)
		} else if len(transitions) > 0 {
			return r.transitionsToSuggestions(transitions, req)
		}
	}

	// 2. Try workflow templates for cold start (step 0 or 1)
	if r.templateStore != nil && req.StepIndex <= 1 && req.Intent != "" {
		templates, err := r.templateStore.GetNextStep(ctx, req.Intent, req.StepIndex)
		if err != nil {
			slog.Debug("ranking: template query failed", "error", err)
		} else if len(templates) > 0 {
			return r.templatesToSuggestions(templates)
		}
	}

	// 3. Final fallback: static suggestions
	return req.FallbackSuggestions
}

// GetDeadEnds returns tools that correlate with session abandonment.
func (r *RankingService) GetDeadEnds(ctx context.Context, intent string) []DeadEndWarning {
	if r.transitionStore == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	transitions, err := r.transitionStore.GetDeadEnds(ctx, intent)
	if err != nil {
		slog.Debug("ranking: dead-end query failed", "error", err)
		return nil
	}

	var warnings []DeadEndWarning
	for _, t := range transitions {
		warnings = append(warnings, DeadEndWarning{
			FromTool: t.FromTool,
			ToTool:   t.ToTool,
			Reason:   fmt.Sprintf("This transition was abandoned %d/%d times", t.AbandonedCount, t.TotalCount),
		})
	}
	return warnings
}

// computeScore calculates a ranking score for a tool transition.
func computeScore(t store.ToolTransition) float64 {
	if t.TotalCount == 0 {
		return 0
	}

	resolvedRate := float64(t.ResolvedCount) / float64(t.TotalCount)
	abandonPenalty := float64(t.AbandonedCount) / float64(t.TotalCount) * 0.5

	// Time decay: half-life of 30 days
	daysSince := time.Since(t.LastSeenAt).Hours() / 24
	timeDecay := math.Pow(0.5, daysSince/30)

	score := (resolvedRate - abandonPenalty) * timeDecay
	if score < 0 {
		score = 0
	}
	return score
}

func (r *RankingService) transitionsToSuggestions(transitions []store.ToolTransition, req RankingRequest) []ToolSuggestion {
	// Score and sort
	type scored struct {
		transition store.ToolTransition
		score      float64
	}

	var items []scored
	seen := make(map[string]bool)
	for _, t := range transitions {
		if seen[t.ToTool] {
			continue
		}
		seen[t.ToTool] = true
		items = append(items, scored{transition: t, score: computeScore(t)})
	}

	// Sort by score descending (simple insertion sort for small N)
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].score > items[j-1].score; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}

	// Merge with fallback suggestions to preserve pre-filled args
	fallbackMap := make(map[string]ToolSuggestion)
	for _, fb := range req.FallbackSuggestions {
		fallbackMap[fb.Tool] = fb
	}

	var result []ToolSuggestion
	for _, item := range items {
		if len(result) >= 5 {
			break
		}
		s := ToolSuggestion{
			Tool:       item.transition.ToTool,
			Confidence: item.score,
			Source:     "learned",
			Evidence:   fmt.Sprintf("Used in %d sessions (%d resolved)", item.transition.TotalCount, item.transition.ResolvedCount),
		}
		// Merge args/why from fallback if available
		if fb, ok := fallbackMap[item.transition.ToTool]; ok {
			s.Args = fb.Args
			s.Why = fb.Why
		}
		result = append(result, s)
	}
	return result
}

func (r *RankingService) templatesToSuggestions(templates []store.WorkflowTemplate) []ToolSuggestion {
	var result []ToolSuggestion
	seen := make(map[string]bool)
	for _, t := range templates {
		if seen[t.ToolName] || len(result) >= 5 {
			continue
		}
		seen[t.ToolName] = true
		result = append(result, ToolSuggestion{
			Tool:       t.ToolName,
			Why:        fmt.Sprintf("Step %d of %s workflow", t.StepOrder+1, t.Name),
			Confidence: 0.5, // moderate confidence for templates
			Source:     "template",
			Evidence:   fmt.Sprintf("From %q workflow template", t.Name),
		})
	}
	return result
}
