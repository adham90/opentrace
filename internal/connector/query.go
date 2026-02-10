package connector

import "context"

// QueryResult holds the structured output of an executed SQL query.
type QueryResult struct {
	Columns  []string
	Rows     [][]any
	RowCount int
}

// QueryExecutor is implemented by connectors that can execute read-only SQL queries
// and return structured results (as opposed to the formatted string from tools).
type QueryExecutor interface {
	ExecuteReadQuery(ctx context.Context, query string) (*QueryResult, error)
}

// PingResult holds the outcome of a data source connectivity check.
type PingResult struct {
	Reachable bool
	LatencyMS int64
	Error     string
}

// HealthChecker is implemented by connectors that support ping/latency checks.
type HealthChecker interface {
	Ping(ctx context.Context) PingResult
}
