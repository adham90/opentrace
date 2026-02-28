package connector

import "context"

// ConnectorType identifies a connector category.
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

// ToolParam describes a parameter for a Tool.
type ToolParam struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "string", "int", "bool"
	Required bool   `json:"required"`
}

// Tool represents a tool exposed by a connector.
type Tool struct {
	Name        string
	Description string
	Params      []ToolParam
	Handler     func(ctx context.Context, args map[string]any) (string, error)
}

// DataSource is the interface that all connectors must implement.
type DataSource interface {
	Type() ConnectorType
	TestConnection(ctx context.Context) error
	Tools() []Tool
	Close() error
}
