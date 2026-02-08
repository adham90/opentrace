package connector

import (
	"context"

	"github.com/opentrace/opentrace/internal/agent"
)

// ConnectorType identifies a connector category.
type ConnectorType string

const (
	ConnectorLogs       ConnectorType = "logs"
	ConnectorDatabase   ConnectorType = "database"
	ConnectorCodebase   ConnectorType = "codebase"
	ConnectorMonitoring ConnectorType = "monitoring"
	ConnectorSystem     ConnectorType = "system"
)

// DataSource is the interface that all connectors must implement.
type DataSource interface {
	Type() ConnectorType
	TestConnection(ctx context.Context) error
	Tools() []agent.Tool
	Close() error
}
