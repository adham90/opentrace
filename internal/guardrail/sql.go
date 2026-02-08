package guardrail

import (
	"fmt"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// ValidateReadOnly parses the SQL query and rejects anything that isn't a SELECT statement.
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
		if node.GetSelectStmt() == nil {
			return fmt.Errorf("only SELECT statements are allowed")
		}
	}

	return nil
}
