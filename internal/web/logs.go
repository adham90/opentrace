package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/opentrace/opentrace/internal/store"
)

type ingestLogEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Service   string         `json:"service"`
	TraceID   string         `json:"trace_id"`
	Message   string         `json:"message"`
	Metadata  map[string]any `json:"metadata"`
}

func (s *Server) handleIngestLogs(w http.ResponseWriter, r *http.Request) {
	var entries []ingestLogEntry
	if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Validate required fields
	for i, e := range entries {
		if e.Timestamp.IsZero() || e.Level == "" || e.Message == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: timestamp, level, and message are required", i))
			return
		}
	}

	logEntries := make([]store.LogEntry, len(entries))
	for i, e := range entries {
		logEntries[i] = store.LogEntry{
			Timestamp: e.Timestamp,
			Level:     e.Level,
			Service:   e.Service,
			TraceID:   e.TraceID,
			Message:   e.Message,
			Metadata:  e.Metadata,
		}
	}

	count, err := s.logStore.BatchInsert(r.Context(), logEntries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to insert logs")
		return
	}

	status := http.StatusCreated
	if count == 0 {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]int{"count": count})
}
