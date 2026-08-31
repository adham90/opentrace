package store

import (
	"time"

	"github.com/uptrace/bun"
)

// Deploy records the first time a commit hash was seen for a
// (service, environment) pair. The SDK stamps every log with the commit it is
// running, so a new hash appearing is the deploy — there is no deploy webhook
// to miss and nothing for the operator to wire up.
type Deploy struct {
	bun.BaseModel `bun:"table:deploys" json:"-"`
	ID            int64     `bun:"id,pk,autoincrement" json:"id"`
	CommitHash    string    `bun:"commit_hash" json:"commit_hash"`
	Service       string    `bun:"service" json:"service,omitempty"`
	Environment   string    `bun:"environment" json:"environment,omitempty"`
	FirstSeenAt   time.Time `bun:"first_seen_at" json:"first_seen_at"`
}

// ListDeployParams filters a deploy listing. Empty Service or Environment means
// "any" — callers holding an env-scoped token must pass their concrete env.
type ListDeployParams struct {
	Service     string     `json:"service,omitempty"`
	Environment string     `json:"environment,omitempty"`
	Since       *time.Time `json:"since,omitempty"`
	Limit       int        `json:"limit,omitempty"`
}
