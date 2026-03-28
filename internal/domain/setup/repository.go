// Package setup provides the domain service for first-run onboarding and
// system status detection. The repository interfaces are minimal — just
// enough to check if data is flowing.
package setup

import (
	"context"

	"github.com/adham90/opentrace/pkg/store"
)

// LogCounter checks if any logs have been ingested.
type LogCounter interface {
	CountByLevel(ctx context.Context, params store.LogCountParams) (map[string]int, error)
}

// UserCounter checks if any users have been created.
type UserCounter interface {
	Count(ctx context.Context) (int, error)
}

// DataSourceLister checks if any data sources are configured.
type DataSourceLister interface {
	List(ctx context.Context, params store.ListDataSourceParams) ([]store.DataSource, error)
}
