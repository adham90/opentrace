package store

import (
	"context"
	"time"
)

// DeployStore records and queries deploy markers.
//
// Rows are keyed (commit_hash, service, environment): the same commit deployed
// to staging and production is two deploys, each with its own first sighting.
// Every read takes an environment; passing "" means "across all envs", which is
// correct for unscoped internal callers and never what an env-scoped MCP token
// should get.
type DeployStore interface {
	// Record notes the first sighting of a commit. Repeat sightings are no-ops,
	// so it is safe to call on every ingest batch.
	Record(ctx context.Context, d Deploy) error

	// Latest returns the most recent deploy in scope, or ErrNotFound when the
	// scope has never seen one.
	Latest(ctx context.Context, service, environment string) (*Deploy, error)

	// List returns deploys in scope, newest first.
	List(ctx context.Context, params ListDeployParams) ([]Deploy, error)

	// Prune deletes deploys older than the given age.
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}
