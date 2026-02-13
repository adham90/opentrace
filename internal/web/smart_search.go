package web

import (
	"context"
	"encoding/json"
	"fmt"
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
	MetadataKeys []string
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
	var llmResult llmSearchResult
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
	if keys, err := logStore.MetadataKeys(ctx, params); err == nil {
		sc.MetadataKeys = keys
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

	b.WriteString("Metadata keys: ")
	if len(ctx.MetadataKeys) > 0 {
		b.WriteString(strings.Join(ctx.MetadataKeys, ", "))
	} else {
		b.WriteString("(none found)")
	}
	b.WriteString("\n")

	b.WriteString("\nCurrent time: ")
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString("\n")

	b.WriteString(`
Valid log levels: DEBUG, INFO, WARN, ERROR
Shortcut time ranges: 15m, 1h, 6h, 24h, 7d

Respond with a JSON object containing ONLY these fields:
{
  "level": "",
  "service": "",
  "environment": "",
  "event_type": "",
  "time_range": "",
  "start_time": "",
  "end_time": "",
  "query": "",
  "metadata": {},
  "reasoning": ""
}

Rules:
- Only use values from the lists above for service, environment, and event_type
- Leave a field as empty string "" if the user's query doesn't specify it or you're uncertain
- "query" is for free-text search terms that don't map to structured filters — use it only for actual keywords the user wants to find in log messages, NOT for restating structured filters
- "metadata" is for key-value filters on log metadata fields — only use keys from the metadata keys list above
- "reasoning" should be a brief one-sentence explanation of your interpretation
- For relative time like "last hour" use time_range "1h", "last 15 minutes" use "15m", "last day" use "24h", "last week" use "7d"
- For specific dates/times (e.g. "yesterday", "last Tuesday", "January 15", "between 2pm and 5pm"), use start_time and/or end_time in RFC3339 format (e.g. "2006-01-02T15:04:05Z"). Calculate from the current time provided above.
- If using time_range, leave start_time and end_time empty. If using start_time/end_time, leave time_range empty.
- Match service/environment names even if the user uses abbreviations or partial names
`)

	return b.String()
}

// llmSearchResult is the parsed JSON response from the LLM.
type llmSearchResult struct {
	Level       string            `json:"level"`
	Service     string            `json:"service"`
	Environment string            `json:"environment"`
	EventType   string            `json:"event_type"`
	TimeRange   string            `json:"time_range"`
	StartTime   string            `json:"start_time"`
	EndTime     string            `json:"end_time"`
	Query       string            `json:"query"`
	Metadata    map[string]string `json:"metadata"`
	Reasoning   string            `json:"reasoning"`
}

func normalizeFilters(raw llmSearchResult, ctx searchContext) map[string]string {
	filters := make(map[string]string)

	// Level: case-insensitive match against valid levels
	if raw.Level != "" {
		level := strings.ToUpper(strings.TrimSpace(raw.Level))
		switch level {
		case "DEBUG", "INFO", "WARN", "ERROR":
			filters["level"] = level
		}
	}

	// Time: prefer preset ranges, fall back to absolute start/end
	presetUsed := false
	if raw.TimeRange != "" {
		tr := strings.TrimSpace(raw.TimeRange)
		switch tr {
		case "15m", "1h", "6h", "24h", "7d":
			filters["time_range"] = tr
			presetUsed = true
		}
	}
	if !presetUsed {
		if raw.StartTime != "" {
			if t, err := parseFlexibleTime(raw.StartTime); err == nil {
				filters["start"] = t.UTC().Format(time.RFC3339)
			}
		}
		if raw.EndTime != "" {
			if t, err := parseFlexibleTime(raw.EndTime); err == nil {
				filters["end"] = t.UTC().Format(time.RFC3339)
			}
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

	// Metadata: validate keys against known metadata keys, pass through values
	if len(raw.Metadata) > 0 {
		knownKeys := make(map[string]bool, len(ctx.MetadataKeys))
		for _, k := range ctx.MetadataKeys {
			knownKeys[strings.ToLower(k)] = true
		}
		for k, v := range raw.Metadata {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			// Find the canonical key name (case-insensitive)
			canonicalKey := ""
			if knownKeys[strings.ToLower(k)] {
				for _, mk := range ctx.MetadataKeys {
					if strings.EqualFold(mk, k) {
						canonicalKey = mk
						break
					}
				}
			}
			if canonicalKey != "" {
				filters["meta."+canonicalKey] = v
			}
		}
	}

	return filters
}

// parseFlexibleTime attempts to parse a time string in various formats.
func parseFlexibleTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time format: %s", s)
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

