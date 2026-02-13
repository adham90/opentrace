package guardrail

import (
	"fmt"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// ValidateReadOnly parses the SQL query and rejects anything that isn't a
// SELECT or EXPLAIN SELECT statement.
func ValidateReadOnly(query string) error {
	result, err := pg_query.Parse(query)
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	stmts := result.GetStmts()
	if len(stmts) == 0 {
		return fmt.Errorf("empty query")
	}

	for _, stmt := range stmts {
		node := stmt.GetStmt()
		if node == nil {
			return fmt.Errorf("nil statement node")
		}
		if node.GetSelectStmt() != nil {
			continue
		}
		if ex := node.GetExplainStmt(); ex != nil {
			// Reject EXPLAIN ANALYZE — it actually executes the query.
			for _, opt := range ex.GetOptions() {
				if de := opt.GetDefElem(); de != nil {
					if de.GetDefname() == "analyze" {
						return fmt.Errorf("EXPLAIN ANALYZE is not allowed (it executes the query)")
					}
				}
			}
			// Only allow EXPLAIN wrapping a SELECT.
			inner := ex.GetQuery()
			if inner != nil && inner.GetSelectStmt() != nil {
				continue
			}
			return fmt.Errorf("only SELECT statements are allowed inside EXPLAIN")
		}
		return fmt.Errorf("only SELECT statements are allowed")
	}

	return nil
}

// HasLimit returns true if the query's top-level SELECT already contains a LIMIT clause.
func HasLimit(query string) bool {
	result, err := pg_query.Parse(query)
	if err != nil {
		return false
	}
	for _, stmt := range result.GetStmts() {
		sel := stmt.GetStmt().GetSelectStmt()
		if sel != nil && sel.LimitCount != nil {
			return true
		}
	}
	return false
}
