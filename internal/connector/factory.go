package connector

import (
	"context"
	"fmt"
	"os"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/store"
)

// CreateConnector builds the appropriate DataSource from a store.DataSource record.
// Resolution order for query guardrails: env (from config) > DB (from settingsStore) > defaults (500, 5000).
func CreateConnector(ctx context.Context, ds store.DataSource, logStore store.LogStore, cfg *config.Config, ss store.SettingsStore) (DataSource, error) {
	switch ds.Type {
	case store.ConnectorLogs:
		return NewLogsConnector(logStore), nil

	case store.ConnectorDatabase:
		connStr, ok := ds.Config["connection_string"].(string)
		if !ok || connStr == "" {
			return nil, fmt.Errorf("database connector requires connection_string in config")
		}
		maxRows, stmtTimeout := resolveQueryGuardrails(ctx, cfg, ss)
		return NewDatabaseConnector(ctx, connStr, maxRows, stmtTimeout)

	case store.ConnectorMySQL:
		connStr, ok := ds.Config["connection_string"].(string)
		if !ok || connStr == "" {
			return nil, fmt.Errorf("MySQL connector requires connection_string in config")
		}
		maxRows, stmtTimeout := resolveQueryGuardrails(ctx, cfg, ss)
		return NewMySQLConnector(ctx, connStr, maxRows, stmtTimeout)

	case store.ConnectorRedis:
		connStr, ok := ds.Config["connection_string"].(string)
		if !ok || connStr == "" {
			return nil, fmt.Errorf("Redis connector requires connection_string in config")
		}
		return NewRedisConnector(ctx, connStr)

	case store.ConnectorTurso:
		connStr, ok := ds.Config["connection_string"].(string)
		if !ok || connStr == "" {
			return nil, fmt.Errorf("Turso connector requires connection_string in config")
		}
		authToken, _ := ds.Config["auth_token"].(string)
		maxRows, _ := resolveQueryGuardrails(ctx, cfg, ss)
		return NewTursoConnector(ctx, connStr, authToken, maxRows)

	default:
		return nil, fmt.Errorf("unknown connector type: %q", ds.Type)
	}
}

// resolveQueryGuardrails returns effective max rows and statement timeout.
// Priority: env var (via config) > DB setting > defaults.
func resolveQueryGuardrails(ctx context.Context, cfg *config.Config, ss store.SettingsStore) (maxRows, stmtTimeout int) {
	maxRows = 500
	stmtTimeout = 5000

	// Check env override first
	envMaxRows := os.Getenv("OPENTRACE_MAX_QUERY_ROWS") != ""
	envTimeout := os.Getenv("OPENTRACE_STATEMENT_TIMEOUT_MS") != ""

	if envMaxRows && cfg != nil {
		maxRows = cfg.MaxQueryRows
	} else if ss != nil {
		if v, err := ss.GetMaxQueryRows(ctx); err == nil && v > 0 {
			maxRows = v
		}
	}

	if envTimeout && cfg != nil {
		stmtTimeout = cfg.StatementTimeoutMS
	} else if ss != nil {
		if v, err := ss.GetStatementTimeout(ctx); err == nil && v > 0 {
			stmtTimeout = v
		}
	}

	return maxRows, stmtTimeout
}
