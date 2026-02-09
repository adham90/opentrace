package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/opentrace/opentrace/internal/agent"
	"github.com/opentrace/opentrace/internal/llm"
	"github.com/opentrace/opentrace/internal/store"
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

	if s.llmProvider == nil {
		sendEvent("error", "No LLM provider configured. Set OPENTRACE_OLLAMA_URL and ensure Ollama is running.", "")
		return
	}

	// Get tools from registry — verify at least one data connector exists
	if !s.registry.HasDataConnectors() {
		sendEvent("error", "No data connectors configured. Add a logs, database, or codebase connector first.", "")
		return
	}
	tools := s.registry.AllTools()

	// Resolve or create chat
	var chatID uuid.UUID
	var history []llm.ChatMessage

	chatIDStr := r.URL.Query().Get("chat_id")
	if chatIDStr != "" && s.chatStore != nil {
		parsed, err := uuid.Parse(chatIDStr)
		if err != nil {
			sendEvent("error", "Invalid chat_id format", "")
			return
		}
		chatID = parsed

		// Load existing messages as history
		msgs, err := s.chatStore.GetMessages(r.Context(), chatID)
		if err != nil {
			sendEvent("error", fmt.Sprintf("Failed to load chat history: %s", err), "")
			return
		}
		history = messagesToChatHistory(msgs)
	} else if s.chatStore != nil {
		// Create new chat
		title := query
		if len(title) > 80 {
			title = title[:80]
		}
		chat, err := s.chatStore.CreateChat(r.Context(), title)
		if err != nil {
			sendEvent("error", fmt.Sprintf("Failed to create chat: %s", err), "")
			return
		}
		chatID = chat.ID
	}

	// Send chat_id event so frontend knows which chat this belongs to
	if chatID != uuid.Nil {
		sendEvent("chat_id", chatID.String(), "")
	}

	// Persist user message
	if s.chatStore != nil && chatID != uuid.Nil {
		_ = s.chatStore.AddMessage(r.Context(), store.Message{
			ID:      uuid.New(),
			ChatID:  chatID,
			Role:    "user",
			Content: query,
		})
	}

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

	ag := agent.New(s.llmProvider, cfg)

	// Run with callback for SSE events, persisting messages as they happen
	ctx := r.Context()
	_, err := ag.RunWithCallback(ctx, query, tools, func(evt agent.Event) {
		sendEvent(evt.Type, evt.Content, evt.ToolName)

		// Persist agent messages to chat
		if s.chatStore != nil && chatID != uuid.Nil {
			switch evt.Type {
			case "tool_call":
				_ = s.chatStore.AddMessage(ctx, store.Message{
					ID:       uuid.New(),
					ChatID:   chatID,
					Role:     "tool_call",
					Content:  evt.Content,
					ToolName: evt.ToolName,
				})
			case "observation":
				_ = s.chatStore.AddMessage(ctx, store.Message{
					ID:       uuid.New(),
					ChatID:   chatID,
					Role:     "observation",
					Content:  evt.Content,
					ToolName: evt.ToolName,
				})
			case "final":
				_ = s.chatStore.AddMessage(ctx, store.Message{
					ID:      uuid.New(),
					ChatID:  chatID,
					Role:    "assistant",
					Content: evt.Content,
				})
			}
		}
	}, history)

	if err != nil {
		sendEvent("error", err.Error(), "")
	}
}

// messagesToChatHistory converts stored messages into LLM chat messages
// for the agent to use as conversation context.
func messagesToChatHistory(msgs []store.Message) []llm.ChatMessage {
	if len(msgs) == 0 {
		return nil
	}
	var history []llm.ChatMessage
	for _, m := range msgs {
		switch m.Role {
		case "user":
			history = append(history, llm.ChatMessage{Role: "user", Content: m.Content})
		case "assistant":
			history = append(history, llm.ChatMessage{Role: "assistant", Content: m.Content})
		case "tool_call":
			// Tool calls were assistant responses (JSON)
			history = append(history, llm.ChatMessage{Role: "assistant", Content: m.Content})
		case "observation":
			// Tool results were fed back as user messages
			history = append(history, llm.ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("Tool %q returned:\n%s", m.ToolName, m.Content),
			})
		}
	}
	return history
}
