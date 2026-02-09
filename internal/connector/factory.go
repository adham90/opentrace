package connector

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/store"
)

// CreateConnector builds the appropriate DataSource from a store.DataSource record.
func CreateConnector(ctx context.Context, ds store.DataSource, logStore store.LogStore, cfg *config.Config) (DataSource, error) {
	switch ds.Type {
	case store.ConnectorLogs:
		return NewLogsConnector(logStore), nil

	case store.ConnectorDatabase:
		connStr, ok := ds.Config["connection_string"].(string)
		if !ok || connStr == "" {
			return nil, fmt.Errorf("database connector requires connection_string in config")
		}
		maxRows := 500
		stmtTimeout := 5000
		if cfg != nil {
			maxRows = cfg.MaxQueryRows
			stmtTimeout = cfg.StatementTimeoutMS
		}
		return NewDatabaseConnector(ctx, connStr, maxRows, stmtTimeout)

	default:
		return nil, fmt.Errorf("unknown connector type: %q", ds.Type)
	}
}
