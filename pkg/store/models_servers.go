package store

import (
	"time"

	"github.com/google/uuid"
)

// ServerStatus represents the health status of a monitored server.
type ServerStatus string

const (
	ServerOnline  ServerStatus = "online"
	ServerOffline ServerStatus = "offline"
	ServerUnknown ServerStatus = "unknown"
)

// Server represents a monitored VM or host.
type Server struct {
	ID           uuid.UUID         `json:"id"`
	Hostname     string            `json:"hostname"`
	DisplayName  string            `json:"display_name,omitempty"`
	IPAddress    string            `json:"ip_address,omitempty"`
	OS           string            `json:"os,omitempty"`
	Arch         string            `json:"arch,omitempty"`
	AgentVersion string            `json:"agent_version,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Status       ServerStatus      `json:"status"`
	LastSeenAt   *time.Time        `json:"last_seen_at,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// UpdateServerParams defines the input for updating a server's user-facing fields.
type UpdateServerParams struct {
	DisplayName *string `json:"display_name,omitempty"`
}

// RegisterServerParams defines the input for registering/updating a server.
type RegisterServerParams struct {
	Hostname     string            `json:"hostname"`
	IPAddress    string            `json:"ip_address,omitempty"`
	OS           string            `json:"os,omitempty"`
	Arch         string            `json:"arch,omitempty"`
	AgentVersion string            `json:"agent_version,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

// MetricPoint represents a single metric data point returned by queries.
type MetricPoint struct {
	ID          int64             `json:"id"`
	ServerID    uuid.UUID         `json:"server_id"`
	Timestamp   time.Time         `json:"timestamp"`
	MetricName  string            `json:"metric_name"`
	MetricValue float64           `json:"metric_value"`
	Unit        string            `json:"unit,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// MetricSample is a single metric in an ingestion batch (no server_id or timestamp — those come from the batch context).
type MetricSample struct {
	Name   string            `json:"name"`
	Value  float64           `json:"value"`
	Unit   string            `json:"unit,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// MetricQuery defines filters for querying metrics.
type MetricQuery struct {
	ServerID   uuid.UUID  `json:"server_id"`
	MetricName string     `json:"metric_name,omitempty"`
	Start      *time.Time `json:"start,omitempty"`
	End        *time.Time `json:"end,omitempty"`
	Limit      int        `json:"limit,omitempty"`
}

// ListServerParams controls pagination for server listing.
type ListServerParams struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}
