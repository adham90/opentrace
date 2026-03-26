package store

import (
	"context"
	"time"
)

// InvestigationSessionStore manages MCP investigation session lifecycle.
type InvestigationSessionStore interface {
	Create(ctx context.Context, params CreateInvestigationSessionParams) (*InvestigationSession, error)
	GetByID(ctx context.Context, id string) (*InvestigationSession, error)
	Close(ctx context.Context, id string) error
	Update(ctx context.Context, id string, params UpdateInvestigationSessionParams) error

	// Session lookup
	FindRecent(ctx context.Context, params FindRecentSessionParams) (*InvestigationSession, error)

	// Listing / analytics
	List(ctx context.Context, params ListInvestigationSessionParams) ([]InvestigationSession, error)
	Stats(ctx context.Context) (*InvestigationSessionStats, error)

	// Retention
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)

	// Step tracking
	RecordStep(ctx context.Context, sessionID string, toolName string, isError bool) error

	// Subsystem link lookups (return nil, nil when not found)
	FindByCreatedWatcher(ctx context.Context, watcherID string) (*InvestigationSession, error)
	FindByResolvedError(ctx context.Context, fingerprint string) (*InvestigationSession, error)
	FindByCreatedHealthcheck(ctx context.Context, healthcheckID string) (*InvestigationSession, error)

	// Similarity search for investigation context
	FindSimilar(ctx context.Context, params FindSimilarParams) ([]InvestigationSession, error)
}
