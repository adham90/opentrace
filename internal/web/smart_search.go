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
	if vals, err := logStore.DistinctValues(ctx, "event_type", params); err == nil {
		sc.EventTypes = vals
	}
	if keys, err := logStore.MetadataKeys(ctx, params); err == nil {
		sc.MetadataKeys = keys
	}
	return sc
}

func buildAISearchPrompt(ctx searchContext, query string) string {
	now := time.Now().UTC()
	var b strings.Builder

	b.WriteString("Convert the user's log search query into JSON filters. Respond with ONLY valid JSON.\n\n")

	// Context
	b.WriteString("Services: ")
	if len(ctx.Services) > 0 {
		b.WriteString(strings.Join(ctx.Services, ", "))
	} else {
		b.WriteString("(none)")
	}
	b.WriteString("\nEvent types: ")
	if len(ctx.EventTypes) > 0 {
		b.WriteString(strings.Join(ctx.EventTypes, ", "))
	} else {
		b.WriteString("(none)")
	}
	if len(ctx.MetadataKeys) > 0 {
		b.WriteString("\nMetadata keys: ")
		b.WriteString(strings.Join(ctx.MetadataKeys, ", "))
	}
	b.WriteString("\nNow: ")
	b.WriteString(now.Format(time.RFC3339))
	b.WriteString("\n")

	b.WriteString(`
JSON fields:
- "level": "ERROR", "WARN", "INFO", or "DEBUG". Words like "errors"/"failures"/"wrong" mean ERROR. "warnings" means WARN.
- "service": Must be from the services list above. Match typos (e.g. "gatway"→"gateway").
- "event_type": Must be from the event types list above.
- "time_range": Only use these exact values: "15m", "1h", "6h", "24h", "7d".
- "start_time": RFC3339 timestamp for custom time ranges (e.g. "45 hours ago", "yesterday", "last 3 days"). Calculate from current time.
- "end_time": RFC3339 timestamp. Use with start_time for date ranges.
- "query": Specific keywords to search in log message text (e.g. "timeout", "TLS handshake", "rate limit exceeded", "cache miss", "card declined"). Leave empty "" if the user only wants to filter by level/service/time — do NOT repeat filter values here.
- "metadata": Key-value pairs using only the metadata keys listed above.
- "reasoning": One sentence summary.

Leave fields as "" if not applicable. Only set service if user mentioned it.

Examples:
`)

	// Dynamic examples using actual services from context
	svc := "my-service"
	if len(ctx.Services) > 0 {
		svc = ctx.Services[0]
	}

	yesterday := now.AddDate(0, 0, -1).Truncate(24 * time.Hour)
	ago45h := now.Add(-45 * time.Hour)

	fmt.Fprintf(&b, `"errors from %s last hour" → {"level":"ERROR","service":"%s","time_range":"1h","query":"","reasoning":"Errors from %s in the last hour"}
"TLS handshake failures" → {"level":"ERROR","query":"TLS handshake","reasoning":"Error logs mentioning TLS handshake"}
"rate limit exceeded errors" → {"level":"ERROR","query":"rate limit exceeded","reasoning":"Error logs mentioning rate limit exceeded"}
"cache miss logs from the last 15 minutes" → {"query":"cache miss","time_range":"15m","reasoning":"Logs mentioning cache miss in last 15 minutes"}
"errors from 45 hours ago until now" → {"level":"ERROR","start_time":"%s","query":"","reasoning":"Errors from the last 45 hours"}
"errors from yesterday" → {"level":"ERROR","start_time":"%s","end_time":"%s","query":"","reasoning":"Errors from yesterday"}
"connection pool nearing capacity" → {"query":"connection pool nearing capacity","reasoning":"Logs mentioning connection pool capacity"}
"memory usage warnings last 3 hours" → {"level":"WARN","start_time":"%s","query":"memory usage","reasoning":"Warning logs about memory usage in last 3 hours"}
`,
		svc, svc, svc,
		ago45h.Format(time.RFC3339),
		yesterday.Format(time.RFC3339), now.Truncate(24*time.Hour).Format(time.RFC3339),
		now.Add(-3*time.Hour).Format(time.RFC3339),
	)

	return b.String()
}

// llmSearchResult is the parsed JSON response from the LLM.
type llmSearchResult struct {
	Level     string       `json:"level"`
	Service   string       `json:"service"`
	EventType string       `json:"event_type"`
	TimeRange string       `json:"time_range"`
	StartTime string       `json:"start_time"`
	EndTime   string       `json:"end_time"`
	Query     string       `json:"query"`
	Metadata  flexibleMeta `json:"metadata"`
	Reasoning string       `json:"reasoning"`
}

// flexibleMeta handles LLM returning either {} or "" for metadata.
type flexibleMeta map[string]string

func (m *flexibleMeta) UnmarshalJSON(data []byte) error {
	// Handle empty string, null, or missing
	s := strings.TrimSpace(string(data))
	if s == `""` || s == "null" || s == "" {
		*m = nil
		return nil
	}
	// Try parsing as map
	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		// Silently ignore — metadata is optional
		*m = nil
		return nil
	}
	*m = result
	return nil
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

	// Event type: fuzzy match against DB values
	if raw.EventType != "" {
		if m := bestMatch(raw.EventType, ctx.EventTypes); m != "" {
			filters["event_type"] = m
		}
	}

	// Query: pass through only if it contains meaningful search terms
	// beyond what's already captured by structured filters.
	if q := strings.TrimSpace(raw.Query); q != "" {
		cleaned := stripRedundantTerms(q, filters, ctx)
		if cleaned != "" {
			filters["query"] = cleaned
		}
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

// stripRedundantTerms removes words from the query string that merely restate
// structured filters (level names, service names, time words, etc.).
func stripRedundantTerms(query string, filters map[string]string, ctx searchContext) string {
	// Words that are just noise — they describe the intent, not a search term
	noise := map[string]bool{
		"show": true, "me": true, "the": true, "from": true, "in": true,
		"on": true, "all": true, "logs": true, "log": true, "find": true,
		"get": true, "with": true, "for": true, "and": true, "or": true,
		"a": true, "an": true, "of": true, "to": true, "that": true,
		"last": true, "ago": true, "until": true, "now": true, "since": true,
		"recent": true, "latest": true, "today": true, "yesterday": true,
		"hours": true, "hour": true, "minutes": true, "minute": true,
		"days": true, "day": true, "week": true,
	}

	// Filter value words (level, service, environment names)
	filterWords := make(map[string]bool)
	for _, v := range filters {
		for _, w := range strings.Fields(strings.ToLower(v)) {
			filterWords[w] = true
		}
	}
	// Also add level synonyms
	filterWords["errors"] = true
	filterWords["error"] = true
	filterWords["warnings"] = true
	filterWords["warning"] = true
	filterWords["debug"] = true
	filterWords["info"] = true
	// Service names from context
	for _, s := range ctx.Services {
		for _, w := range strings.Fields(strings.ToLower(s)) {
			filterWords[w] = true
		}
	}

	words := strings.Fields(query)
	var kept []string
	for _, w := range words {
		lower := strings.ToLower(w)
		if noise[lower] || filterWords[lower] {
			continue
		}
		// Skip numeric-looking tokens (likely time values like "45h", "30m")
		trimmed := strings.TrimRight(lower, "hms")
		if trimmed != "" && trimmed != lower {
			if _, err := fmt.Sscanf(trimmed, "%f", new(float64)); err == nil {
				continue
			}
		}
		kept = append(kept, w)
	}
	return strings.Join(kept, " ")
}

