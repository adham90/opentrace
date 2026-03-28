// Package database provides the domain service for external database
// introspection (schema, queries, activity). The repository interfaces define
// what the service needs from a query executor and query memory store.
package database

import (
	"context"

	"github.com/adham90/opentrace/pkg/store"
)

// QueryResult holds structured results from a read-only SQL query.
type QueryResult struct {
	Columns  []string
	Rows     [][]any
	RowCount int
}

// QueryExecutor runs read-only SQL against an external data source.
// Implemented by connector.QueryExecutor in production.
type QueryExecutor interface {
	ExecuteReadQuery(ctx context.Context, query string) (*QueryResult, error)
}

// QueryMemoryRepository provides access to past query investigation history.
// Implemented by internal/db.QueryMemoryStore (SQLite adapter).
type QueryMemoryRepository interface {
	Get(ctx context.Context, fingerprint string) (*store.QueryMemory, error)
	Upsert(ctx context.Context, params store.UpsertQueryMemoryParams) error
}
