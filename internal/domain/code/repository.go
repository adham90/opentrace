// Package code provides the domain service for code entity risk analysis
// and test correlation. The repository interfaces define what the service
// needs from the data layer.
package code

import (
	"context"

	"github.com/adham90/opentrace/pkg/store"
)

// CodeEntityRepository defines read access for code entity risk data.
// Implemented by internal/db.CodeEntityStore (SQLite adapter).
type CodeEntityRepository interface {
	TopByRisk(ctx context.Context, service string, limit int) ([]store.CodeEntity, error)
	GetByName(ctx context.Context, entityType store.CodeEntityType, entityName, service string) (*store.CodeEntity, error)
	BatchGetRisk(ctx context.Context, entityType store.CodeEntityType, names []string, service string) ([]store.CodeEntity, error)
}

// TestCorrelationRepository defines access to uncovered error path analysis.
// Implemented by internal/db.TestCorrelationStore (SQLite adapter).
type TestCorrelationRepository interface {
	TopByPriority(ctx context.Context, service string, limit int) ([]store.UncoveredErrorPath, error)
	GetByFingerprint(ctx context.Context, fingerprint string) (*store.UncoveredErrorPath, error)
}

// NoteRepository provides access to agent annotations on code entities.
// Implemented by internal/db.AgentNoteStore (SQLite adapter).
type NoteRepository interface {
	Upsert(ctx context.Context, entityType, entityID, note string) (*store.AgentNote, error)
	Get(ctx context.Context, entityType, entityID string) (*store.AgentNote, error)
	List(ctx context.Context, entityType string) ([]store.AgentNote, error)
}
