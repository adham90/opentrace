package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/llm"
	"github.com/adham90/opentrace/internal/store"
)

type smartSearchRequest struct {
	Query string `json:"query"`
}

type smartSearchResponse struct {
	Filters   map[string]string `json:"filters"`
	Reasoning string            `json:"reasoning"`
	Error     string            `json:"error,omitempty"`
}

type searchContext struct {
	Services     []string
	Environments []string
	EventTypes   []string
}

func (s *Server) handleSmartSearch(w http.ResponseWriter, r *http.Request) {
	var req smartSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}

	// Get an LLM provider
	provider, err := llm.NewProviderByName(s.cfg.LLMProvider, s.cfg)
	if err != nil {
		slog.Warn("smart search: no LLM provider", "error", err)
		writeJSON(w, http.StatusOK, smartSearchResponse{
			Error: "Smart search requires a configured LLM provider",
		})
		return
	}

	// Fetch available filter values from the log store
	ctx := r.Context()
	sCtx := fetchSearchContext(ctx, s.logStore)

	// Build prompt and call LLM
	prompt := buildAISearchPrompt(sCtx, req.Query)
	resp, err := provider.ChatCompletion(ctx, llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: prompt},
			{Role: "user", Content: req.Query},
		},
		JSONMode:  true,
		MaxTokens: 512,
	})
	if err != nil {
		slog.Error("smart search: LLM call failed", "error", err)
		writeJSON(w, http.StatusOK, smartSearchResponse{
			Error: "AI search failed: " + err.Error(),
		})
		return
	}

	// Parse LLM response
	var llmResult struct {
		Level       string `json:"level"`
		Service     string `json:"service"`
		Environment string `json:"environment"`
		EventType   string `json:"event_type"`
		TimeRange   string `json:"time_range"`
		Query       string `json:"query"`
		Reasoning   string `json:"reasoning"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &llmResult); err != nil {
		slog.Warn("smart search: failed to parse LLM response", "error", err, "content", resp.Content)
		writeJSON(w, http.StatusOK, smartSearchResponse{
			Error: "Failed to parse AI response",
		})
		return
	}

	// Normalize filters against real DB values
	filters := normalizeFilters(llmResult, sCtx)

	writeJSON(w, http.StatusOK, smartSearchResponse{
		Filters:   filters,
		Reasoning: llmResult.Reasoning,
	})
}

func fetchSearchContext(ctx context.Context, logStore store.LogStore) searchContext {
	if logStore == nil {
		return searchContext{}
	}

	// Use a wide time range to get all available values
	params := store.LogCountParams{
		Since: time.Now().Add(-30 * 24 * time.Hour),
		Until: time.Now(),
	}

	var sc searchContext
	if vals, err := logStore.DistinctValues(ctx, "service", params); err == nil {
		sc.Services = vals
	}
	if vals, err := logStore.DistinctValues(ctx, "environment", params); err == nil {
		sc.Environments = vals
	}
	if vals, err := logStore.DistinctValues(ctx, "event_type", params); err == nil {
		sc.EventTypes = vals
	}
	return sc
}

func buildAISearchPrompt(ctx searchContext, query string) string {
	var b strings.Builder
	b.WriteString(`You are a log search assistant. Convert the user's natural language query into structured log filters.

Available filter values:

`)
	b.WriteString("Services: ")
	if len(ctx.Services) > 0 {
		b.WriteString(strings.Join(ctx.Services, ", "))
	} else {
		b.WriteString("(none found)")
	}
	b.WriteString("\n")

	b.WriteString("Environments: ")
	if len(ctx.Environments) > 0 {
		b.WriteString(strings.Join(ctx.Environments, ", "))
	} else {
		b.WriteString("(none found)")
	}
	b.WriteString("\n")

	b.WriteString("Event types: ")
	if len(ctx.EventTypes) > 0 {
		b.WriteString(strings.Join(ctx.EventTypes, ", "))
	} else {
		b.WriteString("(none found)")
	}
	b.WriteString("\n")

	b.WriteString(`
Valid log levels: DEBUG, INFO, WARN, ERROR
Valid time ranges: 15m, 1h, 6h, 24h, 7d

Respond with a JSON object containing ONLY these fields:
{
  "level": "",
  "service": "",
  "environment": "",
  "event_type": "",
  "time_range": "",
  "query": "",
  "reasoning": ""
}

Rules:
- Only use values from the lists above for service, environment, and event_type
- Leave a field as empty string "" if the user's query doesn't specify it or you're uncertain
- "query" is for free-text search terms that don't map to structured filters
- "reasoning" should be a brief one-sentence explanation of your interpretation
- For time references like "last hour" use "1h", "last 15 minutes" use "15m", "last day" use "24h", "last week" use "7d"
- Match service/environment names even if the user uses abbreviations or partial names
`)

	return b.String()
}

func normalizeFilters(raw struct {
	Level       string `json:"level"`
	Service     string `json:"service"`
	Environment string `json:"environment"`
	EventType   string `json:"event_type"`
	TimeRange   string `json:"time_range"`
	Query       string `json:"query"`
	Reasoning   string `json:"reasoning"`
}, ctx searchContext) map[string]string {
	filters := make(map[string]string)

	// Level: case-insensitive match against valid levels
	if raw.Level != "" {
		level := strings.ToUpper(strings.TrimSpace(raw.Level))
		switch level {
		case "DEBUG", "INFO", "WARN", "ERROR":
			filters["level"] = level
		}
	}

	// Time range: exact match against valid ranges
	if raw.TimeRange != "" {
		tr := strings.TrimSpace(raw.TimeRange)
		switch tr {
		case "15m", "1h", "6h", "24h", "7d":
			filters["time_range"] = tr
		}
	}

	// Service: fuzzy match against DB values
	if raw.Service != "" {
		if m := bestMatch(raw.Service, ctx.Services); m != "" {
			filters["service"] = m
		}
	}

	// Environment: fuzzy match against DB values
	if raw.Environment != "" {
		if m := bestMatch(raw.Environment, ctx.Environments); m != "" {
			filters["environment"] = m
		}
	}

	// Event type: fuzzy match against DB values
	if raw.EventType != "" {
		if m := bestMatch(raw.EventType, ctx.EventTypes); m != "" {
			filters["event_type"] = m
		}
	}

	// Query: pass through as-is for FTS search
	if q := strings.TrimSpace(raw.Query); q != "" {
		filters["query"] = q
	}

	return filters
}

// bestMatch finds the best match for input among candidates.
// Priority: exact (case-insensitive) > prefix > substring > no match.
func bestMatch(input string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return ""
	}

	// Exact match (case-insensitive)
	for _, c := range candidates {
		if strings.ToLower(c) == lower {
			return c
		}
	}

	// Prefix match
	for _, c := range candidates {
		if strings.HasPrefix(strings.ToLower(c), lower) {
			return c
		}
	}

	// Substring match
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(c), lower) {
			return c
		}
	}

	return ""
}

