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

	sendSSE := func(evt sseEvent) {
		data, _ := json.Marshal(evt)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	sendEvent := func(stepType, content, toolName string, args map[string]any) {
		sendSSE(sseEvent{
			StepType: stepType,
			Content:  content,
			ToolName: toolName,
			Args:     args,
		})
	}

	// Select LLM provider: query param > default
	selectedProvider := s.llmProvider
	if providerName := r.URL.Query().Get("provider"); providerName != "" && s.llmProviders != nil {
		if p, ok := s.llmProviders[providerName]; ok {
			selectedProvider = p
		} else {
			sendEvent("error", fmt.Sprintf("Unknown provider: %q. Check /api/providers for available options.", providerName), "", nil)
			return
		}
	}

	if selectedProvider == nil {
		sendEvent("error", "No LLM provider configured. Set OPENTRACE_OLLAMA_URL and ensure Ollama is running.", "", nil)
		return
	}

	// Get tools from registry — verify at least one data connector exists
	if !s.registry.HasDataConnectors() {
		sendEvent("error", "No data connectors configured. Add a logs, database, or codebase connector first.", "", nil)
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
			sendEvent("error", "Invalid chat_id format", "", nil)
			return
		}
		chatID = parsed

		// Load existing messages as history
		msgs, err := s.chatStore.GetMessages(r.Context(), chatID)
		if err != nil {
			sendEvent("error", fmt.Sprintf("Failed to load chat history: %s", err), "", nil)
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
			sendEvent("error", fmt.Sprintf("Failed to create chat: %s", err), "", nil)
			return
		}
		chatID = chat.ID
	}

	// Send chat_id event so frontend knows which chat this belongs to
	if chatID != uuid.Nil {
		sendEvent("chat_id", chatID.String(), "", nil)
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

	ag := agent.New(selectedProvider, cfg)

	// Run with callback for SSE events, persisting messages as they happen
	ctx := r.Context()
	_, err := ag.RunWithCallback(ctx, query, tools, func(evt agent.Event) {
		switch evt.Type {
		case "plan":
			steps := make([]planStepDTO, len(evt.Steps))
			for i, s := range evt.Steps {
				steps[i] = planStepDTO{ID: s.ID, Description: s.Description}
			}
			sendSSE(sseEvent{StepType: "plan", Steps: steps})
		case "plan_update":
			sendSSE(sseEvent{StepType: "plan_update", StepID: evt.StepID, Status: evt.Status})
		default:
			sendEvent(evt.Type, evt.Content, evt.ToolName, evt.Args)
		}

		// Persist agent messages to chat
		if s.chatStore != nil && chatID != uuid.Nil {
			switch evt.Type {
			case "plan":
				// Persist plan steps as JSON content with role "plan"
				stepsJSON, _ := json.Marshal(evt.Steps)
				_ = s.chatStore.AddMessage(ctx, store.Message{
					ID:      uuid.New(),
					ChatID:  chatID,
					Role:    "plan",
					Content: string(stepsJSON),
				})
			case "tool_call":
				// Persist args as JSON so chat history can render them
				toolContent := evt.Content
				if len(evt.Args) > 0 {
					if argsJSON, err := json.Marshal(evt.Args); err == nil {
						toolContent = string(argsJSON)
					}
				}
				_ = s.chatStore.AddMessage(ctx, store.Message{
					ID:       uuid.New(),
					ChatID:   chatID,
					Role:     "tool_call",
					Content:  toolContent,
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
			// plan_update is ephemeral — no persistence needed
			}
		}
	}, history)

	if err != nil {
		sendEvent("error", err.Error(), "", nil)
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
		case "plan":
			// Plans were assistant responses — include so LLM has context
			history = append(history, llm.ChatMessage{Role: "assistant", Content: m.Content})
		}
	}
	return history
}
