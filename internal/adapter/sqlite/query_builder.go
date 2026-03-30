package sqlite

import "strings"

// queryBuilder accumulates WHERE conditions for dynamic SQL construction.
type queryBuilder struct {
	conds []string
	args  []any
}

// where adds a condition with its arguments.
func (qb *queryBuilder) where(cond string, args ...any) {
	qb.conds = append(qb.conds, cond)
	qb.args = append(qb.args, args...)
}

// build appends the accumulated WHERE clause to baseQuery.
// Returns the final query and all arguments.
func (qb *queryBuilder) build(baseQuery string) (string, []any) {
	if len(qb.conds) == 0 {
		return baseQuery, qb.args
	}
	return baseQuery + " WHERE " + strings.Join(qb.conds, " AND "), qb.args
}
