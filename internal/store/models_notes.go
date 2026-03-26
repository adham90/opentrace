package store

import "time"

// AgentNote represents a persistent note attached to an entity by the AI agent.
type AgentNote struct {
	ID         int64     `json:"id"`
	EntityType string    `json:"entity_type"` // "query", "endpoint", "service", "healthcheck", "error"
	EntityID   string    `json:"entity_id"`   // fingerprint, URL, query hash, etc.
	Note       string    `json:"note"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
