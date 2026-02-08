package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/opentrace/opentrace/internal/agent"
	"github.com/opentrace/opentrace/internal/llm"
)

func (s *Server) handleInvestigateSSE(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter is required")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	sendEvent := func(stepType, content, toolName string) {
		evt := sseEvent{
			StepType: stepType,
			Content:  content,
			ToolName: toolName,
		}
		data, _ := json.Marshal(evt)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// Check if LLM provider is available
	var llmProvider llm.LLMProvider
	if s.embedder != nil {
		// Try to get the LLM provider from the same backend
		if op, ok := s.embedder.(llm.LLMProvider); ok {
			llmProvider = op
		}
	}

	if llmProvider == nil {
		sendEvent("error", "No LLM provider configured. Set OPENTRACE_OLLAMA_URL and ensure Ollama is running.", "")
		return
	}

	// Get tools from registry
	tools := s.registry.AllTools()

	// Build agent config
	cfg := agent.RunConfig{
		MaxSteps:            12,
		MaxToolCalls:        8,
		MaxObservationBytes: 8192,
	}
	if s.cfg != nil {
		cfg.MaxSteps = s.cfg.MaxAgentSteps
		cfg.MaxToolCalls = s.cfg.MaxToolCalls
		cfg.MaxObservationBytes = s.cfg.MaxObservationBytes
	}

	ag := agent.New(llmProvider, cfg)

	// Run with callback for SSE events
	_, err := ag.RunWithCallback(r.Context(), query, tools, func(evt agent.Event) {
		sendEvent(evt.Type, evt.Content, evt.ToolName)
	})

	if err != nil {
		sendEvent("error", err.Error(), "")
	}
}
