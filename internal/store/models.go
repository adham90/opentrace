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
