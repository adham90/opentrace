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

// EnvironmentScoped is implemented by connectors pinned to a specific
// environment. The database tools use it to reject cross-env queries at query
// time. An empty or "*" environment means the connector is shared / any-env.
type EnvironmentScoped interface {
	Environment() string
}

// ControlExecutor is implemented by connectors that can run privileged control
// statements (e.g. cancelling/terminating a backend) that intentionally bypass
// the read-only side-effecting-function guardrail. Callers MUST enforce
// authorization (admin) before using it.
type ControlExecutor interface {
	ExecuteControlQuery(ctx context.Context, query string) (*QueryResult, error)
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
