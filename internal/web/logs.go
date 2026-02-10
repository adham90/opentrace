package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

type ingestLogEntry struct {
	Timestamp   time.Time      `json:"timestamp"`
	Level       string         `json:"level"`
	Service     string         `json:"service"`
	TraceID     string         `json:"trace_id"`
	Message     string         `json:"message"`
	Environment string         `json:"environment"`
	Metadata    map[string]any `json:"metadata"`
}

func (s *Server) handleIngestLogs(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var entries []ingestLogEntry
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var single ingestLogEntry
		if err := json.Unmarshal(trimmed, &single); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		entries = []ingestLogEntry{single}
	} else {
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
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
			Timestamp:   e.Timestamp,
			Level:       e.Level,
			Service:     e.Service,
			TraceID:     e.TraceID,
			Message:     e.Message,
			Environment: e.Environment,
			Metadata:    e.Metadata,
		}
	}

	count, err := s.logStore.BatchInsert(r.Context(), logEntries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to insert logs")
		return
	}

	if count > 0 {
		s.ensureLogsConnector(r.Context())
	}

	status := http.StatusCreated
	if count == 0 {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]int{"count": count})
}

// ensureLogsConnector auto-creates and registers a logs connector if one
// doesn't already exist. Called after each successful log ingestion so the
// AI agent can use the log_search tool. Uses a mutex instead of sync.Once
// so it can retry after transient failures or re-register after deletion.
func (s *Server) ensureLogsConnector(ctx context.Context) {
	// Fast path: already registered in memory (no lock needed)
	if s.registry.Get(connector.ConnectorLogs) != nil {
		return
	}

	s.logsConnMu.Lock()
	defer s.logsConnMu.Unlock()

	// Double-check after acquiring lock
	if s.registry.Get(connector.ConnectorLogs) != nil {
		return
	}

	// Check if a logs data source row already exists in the DB
	sources, err := s.dsStore.List(ctx, store.ListDataSourceParams{})
	if err != nil {
		log.Printf("WARN: ensureLogsConnector: failed to list data sources: %v", err)
		return
	}
	var dsID *store.DataSource
	for i := range sources {
		if sources[i].Type == store.ConnectorLogs {
			dsID = &sources[i]
			break
		}
	}

	// Create DB row if it doesn't exist
	if dsID == nil {
		created, err := s.dsStore.Create(ctx, store.CreateDataSourceParams{
			Type:   store.ConnectorLogs,
			Name:   "Log Ingestion",
			Config: map[string]any{},
		})
		if err != nil {
			log.Printf("WARN: ensureLogsConnector: failed to create data source: %v", err)
			return
		}
		dsID = created
	}

	// Create and register the runtime connector
	lc := connector.NewLogsConnector(s.logStore)
	s.registry.Register(lc)

	// Update DB status to connected
	connected := store.StatusConnected
	if _, err := s.dsStore.Update(ctx, dsID.ID, store.UpdateDataSourceParams{
		Status: &connected,
	}); err != nil {
		log.Printf("WARN: ensureLogsConnector: failed to update status: %v", err)
	}

	log.Printf("INFO: auto-registered logs connector (data source %s)", dsID.ID)
}
