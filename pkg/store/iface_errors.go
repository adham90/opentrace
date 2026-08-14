package store

import (
	"context"
	"time"
)

// ErrorGroupStore manages error groups aggregated by fingerprint.
//
// Rows are keyed (fingerprint, environment): the same fingerprint legitimately
// exists once per env, each with its own status and counts. Every method that
// names a fingerprint therefore also takes an environment. Passing "" means
// "across all envs" — correct for unscoped internal callers (retention, the
// web UI), and never what an env-scoped MCP token should get. Callers holding
// a token scope must resolve it first and pass the concrete env, otherwise a
// production-scoped caller reads or mutates staging rows.
type ErrorGroupStore interface {
	Upsert(ctx context.Context, entry LogEntry) error
	Get(ctx context.Context, fingerprint, environment string) (*ErrorGroup, error)
	List(ctx context.Context, params ListErrorGroupParams) ([]ErrorGroup, error)
	Count(ctx context.Context, status ErrorGroupStatus, environment string) (int, error)
	Resolve(ctx context.Context, fingerprint, environment, reason string) error
	Ignore(ctx context.Context, fingerprint, environment, reason string) error
	Reopen(ctx context.Context, fingerprint, environment, reason string) error
	// ListEvents returns lifecycle events for a fingerprint. environment ""
	// means "across all envs" — env-scoped callers must pass a concrete env,
	// otherwise they see another environment's resolve/ignore history.
	ListEvents(ctx context.Context, fingerprint, environment string, limit int) ([]ErrorGroupEvent, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// ErrorImpactStore tracks which users are affected by each error and computes impact scores.
type ErrorImpactStore interface {
	// Called on each error occurrence (upsert). environment must match the
	// env of the error_groups row the impact belongs to.
	TrackImpact(ctx context.Context, fingerprint, environment, userID string, contextData map[string]any, logID int64, service string) error

	// Query. GetImpact reports the impact of a fingerprint within one
	// environment. Pass "" to aggregate across every env the fingerprint was
	// observed in; the returned ErrorImpact.Environment then names the
	// highest-impact env rather than the whole aggregate, so env-scoped callers
	// must pass their own env instead of gating on the result.
	GetImpact(ctx context.Context, fingerprint, environment string) (*ErrorImpact, error)
	GetAffectedUsers(ctx context.Context, fingerprint string, limit int) ([]AffectedUser, error)
	GetUserErrors(ctx context.Context, userID string, since time.Time) ([]ErrorSummary, error)

	// Scoring
	ComputeImpactScores(ctx context.Context) error
	TopByImpact(ctx context.Context, params ImpactQueryParams) ([]ErrorGroupWithImpact, error)

	// Pattern detection
	FindCommonTraits(ctx context.Context, fingerprint string) (map[string]any, error)
}
