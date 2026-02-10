package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ListEnvironments returns distinct non-empty environment values across
// data_sources, watchers, alerts, and logs.
func ListEnvironments(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT environment FROM (
			SELECT environment FROM data_sources WHERE environment != ''
			UNION
			SELECT environment FROM watchers WHERE environment != ''
			UNION
			SELECT environment FROM alerts WHERE environment != ''
			UNION
			SELECT environment FROM logs WHERE environment != ''
		) ORDER BY environment`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing environments: %w", err)
	}
	defer rows.Close()

	var envs []string
	for rows.Next() {
		var env string
		if err := rows.Scan(&env); err != nil {
			return nil, fmt.Errorf("scanning environment: %w", err)
		}
		envs = append(envs, env)
	}
	return envs, rows.Err()
}

// ListServices returns distinct non-empty service values from the logs table.
func ListServices(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT service FROM logs WHERE service != '' ORDER BY service`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	defer rows.Close()

	var services []string
	for rows.Next() {
		var svc string
		if err := rows.Scan(&svc); err != nil {
			return nil, fmt.Errorf("scanning service: %w", err)
		}
		services = append(services, svc)
	}
	return services, rows.Err()
}
