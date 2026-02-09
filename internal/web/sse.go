package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type planStepDTO struct {
	ID          int    `json:"id"`
	Description string `json:"description"`
}

type sseEvent struct {
	StepType string         `json:"step_type"`
	Content  string         `json:"content"`
	ToolName string         `json:"tool_name,omitempty"`
	Args     map[string]any `json:"args,omitempty"`
	Steps    []planStepDTO  `json:"steps,omitempty"`
	StepID   int            `json:"step_id,omitempty"`
	Status   string         `json:"status,omitempty"`
}

var demoEvents = []sseEvent{
	{StepType: "thinking", Content: "Analyzing the error pattern in the logs..."},
	{StepType: "tool_call", Content: "Searching for related log entries", ToolName: "log_search", Args: map[string]any{"query": "connection timeout error", "level": "ERROR", "limit": 10}},
	{StepType: "observation", Content: "Found 3 error entries matching the pattern in service 'api-gateway'"},
	{StepType: "final", Content: "The error is caused by a connection timeout between api-gateway and the database."},
}

func (s *Server) handleSSEDemo(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for _, event := range demoEvents {
		select {
		case <-r.Context().Done():
			return
		default:
		}

		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
	}
}
