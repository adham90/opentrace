package store

import (
	"time"

	"github.com/google/uuid"
)

type ConnectorType string

const (
	ConnectorLogs       ConnectorType = "logs"
	ConnectorDatabase   ConnectorType = "database"
	ConnectorCodebase   ConnectorType = "codebase"
	ConnectorMonitoring ConnectorType = "monitoring"
)

type ConnectorStatus string

const (
	StatusConnected    ConnectorStatus = "connected"
	StatusDisconnected ConnectorStatus = "disconnected"
	StatusError        ConnectorStatus = "error"
)

type DataSource struct {
	ID            uuid.UUID       `json:"id"`
	Type          ConnectorType   `json:"type"`
	Name          string          `json:"name"`
	Config        map[string]any  `json:"config"`
	Status        ConnectorStatus `json:"status"`
	StatusMessage *string         `json:"status_message,omitempty"`
	LastTestedAt  *time.Time      `json:"last_tested_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type CreateDataSourceParams struct {
	Type   ConnectorType  `json:"type"`
	Name   string         `json:"name"`
	Config map[string]any `json:"config"`
}

type UpdateDataSourceParams struct {
	Status        *ConnectorStatus `json:"status,omitempty"`
	StatusMessage *string          `json:"status_message,omitempty"`
	LastTestedAt  *time.Time       `json:"last_tested_at,omitempty"`
}

// LogEntry represents an ingested log line.
type LogEntry struct {
	ID          int64          `json:"id"`
	Timestamp   time.Time      `json:"timestamp"`
	Level       string         `json:"level"`
	Service     string         `json:"service,omitempty"`
	TraceID     string         `json:"trace_id,omitempty"`
	Message     string         `json:"message"`
	Environment string         `json:"environment,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

// LogSearchParams defines filters for log search.
type LogSearchParams struct {
	Query       string     `json:"query,omitempty"`
	Service     string     `json:"service,omitempty"`
	Level       string     `json:"level,omitempty"`
	TraceID     string     `json:"trace_id,omitempty"`
	Environment string     `json:"environment,omitempty"`
	Start       *time.Time `json:"start,omitempty"`
	End         *time.Time `json:"end,omitempty"`
	Limit       int        `json:"limit,omitempty"`
}

// CodeChunk represents a chunk of source code with its embedding.
type CodeChunk struct {
	FilePath   string    `json:"file_path"`
	ChunkIndex int       `json:"chunk_index"`
	Content    string    `json:"content"`
	Embedding  []float64 `json:"embedding,omitempty"`
}

// CodeSearchResult represents a code chunk returned by similarity search.
type CodeSearchResult struct {
	FilePath   string  `json:"file_path"`
	ChunkIndex int     `json:"chunk_index"`
	Content    string  `json:"content"`
	Similarity float64 `json:"similarity"`
}
