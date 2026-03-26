package store

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type ConnectorType string

const (
	ConnectorLogs          ConnectorType = "logs"
	ConnectorDatabase      ConnectorType = "database"
	ConnectorMySQL         ConnectorType = "mysql"
	ConnectorRedis         ConnectorType = "redis"
	ConnectorTurso         ConnectorType = "turso"
	ConnectorMonitoring    ConnectorType = "monitoring"
	ConnectorServerMetrics ConnectorType = "server_metrics"
)

type ConnectorStatus string

const (
	StatusConnected    ConnectorStatus = "connected"
	StatusDisconnected ConnectorStatus = "disconnected"
	StatusError        ConnectorStatus = "error"
)

type DataSource struct {
	bun.BaseModel `bun:"table:data_sources" json:"-"`
	ID            uuid.UUID       `bun:"id,pk" json:"id"`
	Type          ConnectorType   `bun:"type" json:"type"`
	Name          string          `bun:"name" json:"name"`
	Config        map[string]any  `bun:"config" json:"config"`
	Status        ConnectorStatus `bun:"status" json:"status"`
	StatusMessage *string         `bun:"status_message" json:"status_message,omitempty"`
	LastTestedAt  *time.Time      `bun:"last_tested_at" json:"last_tested_at,omitempty"`
	CreatedAt     time.Time       `bun:"created_at" json:"created_at"`
	UpdatedAt     time.Time       `bun:"updated_at" json:"updated_at"`
}

type CreateDataSourceParams struct {
	Type   ConnectorType  `json:"type"`
	Name   string         `json:"name"`
	Config map[string]any `json:"config"`
}

type UpdateDataSourceParams struct {
	Name          *string          `json:"name,omitempty"`
	Config        map[string]any   `json:"config,omitempty"`
	Status        *ConnectorStatus `json:"status,omitempty"`
	StatusMessage *string          `json:"status_message,omitempty"`
	LastTestedAt  *time.Time       `json:"last_tested_at,omitempty"`
}

// ListDataSourceParams defines filters for listing data sources.
type ListDataSourceParams struct {
	Type ConnectorType `json:"type,omitempty"`
}
