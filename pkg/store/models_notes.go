package store

import (
	"time"

	"github.com/uptrace/bun"
)

// AgentNote represents a persistent note attached to an entity by the AI agent.
type AgentNote struct {
	bun.BaseModel `bun:"table:agent_notes" json:"-"`
	ID            int64     `bun:"id,pk,autoincrement" json:"id"`
	EntityType    string    `bun:"entity_type" json:"entity_type"` // "query", "endpoint", "service", "healthcheck", "error"
	EntityID      string    `bun:"entity_id" json:"entity_id"`    // fingerprint, URL, query hash, etc.
	Note          string    `bun:"note" json:"note"`
	CreatedAt     time.Time `bun:"created_at" json:"created_at"`
	UpdatedAt     time.Time `bun:"updated_at" json:"updated_at"`
}
