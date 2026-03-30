package mcp

import (
	"testing"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/internal/testutil"
)

// setupTestDB opens an in-memory SQLite database with migrations applied.
// Used for real integration tests (no mocks).
func setupTestDB(t *testing.T) *bun.DB {
	t.Helper()
	return testutil.SetupTestBunDB(t)
}
