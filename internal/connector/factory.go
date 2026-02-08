package connector

import (
	"context"
	"fmt"

	"github.com/opentrace/opentrace/internal/config"
	"github.com/opentrace/opentrace/internal/llm"
	"github.com/opentrace/opentrace/internal/store"
)

// CreateConnector builds the appropriate DataSource from a store.DataSource record.
func CreateConnector(ctx context.Context, ds store.DataSource, logStore store.LogStore, embStore store.EmbeddingStore, embedder llm.EmbeddingProvider, cfg *config.Config) (DataSource, error) {
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

	case store.ConnectorCodebase:
		repoPath, ok := ds.Config["repo_path"].(string)
		if !ok || repoPath == "" {
			return nil, fmt.Errorf("codebase connector requires repo_path in config")
		}
		if embedder == nil {
			return nil, fmt.Errorf("codebase connector requires an embedding provider")
		}
		return NewCodebaseConnector(ctx, repoPath, embStore, embedder)

	default:
		return nil, fmt.Errorf("unknown connector type: %q", ds.Type)
	}
}
