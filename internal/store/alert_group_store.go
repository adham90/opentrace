package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type alertGroupStore struct {
	db *sql.DB
}

// NewAlertGroupStore creates a new AlertGroupStore backed by SQLite.
func NewAlertGroupStore(db *sql.DB) AlertGroupStore {
	return &alertGroupStore{db: db}
}

func (s *alertGroupStore) Create(ctx context.Context, params CreateAlertGroupParams) (*AlertGroup, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	severity := params.Severity
	if severity == "" {
		severity = "warning"
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO alert_groups (id, title, severity, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, params.Title, severity, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("creating alert group: %w", err)
	}

	// Add alerts to the group
	for _, alertID := range params.AlertIDs {
		_, err := s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO alert_group_members (group_id, alert_id) VALUES (?, ?)`,
			id, alertID,
		)
		if err != nil {
			return nil, fmt.Errorf("adding alert to group: %w", err)
		}
	}

	return s.GetByID(ctx, id)
}

func (s *alertGroupStore) GetByID(ctx context.Context, id string) (*AlertGroup, error) {
	g := &AlertGroup{}
	var createdAt, updatedAt string
	var resolvedAt sql.NullString
	var rootCause, resolution sql.NullString

	err := s.db.QueryRowContext(ctx,
		`SELECT g.id, g.title, g.status, g.severity,
		        g.root_cause, g.resolution, g.created_at, g.updated_at, g.resolved_at,
		        (SELECT COUNT(*) FROM alert_group_members WHERE group_id = g.id)
		 FROM alert_groups g WHERE g.id = ?`, id,
	).Scan(&g.ID, &g.Title, &g.Status, &g.Severity,
		&rootCause, &resolution, &createdAt, &updatedAt, &resolvedAt,
		&g.AlertCount,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying alert group: %w", err)
	}

	g.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	g.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if resolvedAt.Valid {
		t, _ := time.Parse(time.RFC3339, resolvedAt.String)
		g.ResolvedAt = &t
	}
	if rootCause.Valid {
		g.RootCause = &rootCause.String
	}
	if resolution.Valid {
		g.Resolution = &resolution.String
	}

	// Load member alerts
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.watcher_id, a.run_id, COALESCE(a.watcher_title, ''), a.title, a.summary,
		        a.severity, a.details, a.read, a.dismissed, a.created_at
		 FROM alerts a
		 JOIN alert_group_members m ON m.alert_id = a.id
		 WHERE m.group_id = ?
		 ORDER BY a.created_at DESC`, id,
	)
	if err != nil {
		return nil, fmt.Errorf("loading group alerts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var a Alert
		var watcherID, runID sql.NullString
		var detailsStr sql.NullString
		var readInt, dismissedInt int
		var alertCreated string

		if err := rows.Scan(
			&a.ID, &watcherID, &runID, &a.WatcherTitle, &a.Title, &a.Summary,
			&a.Severity, &detailsStr, &readInt, &dismissedInt, &alertCreated,
		); err != nil {
			return nil, fmt.Errorf("scanning group alert: %w", err)
		}

		if watcherID.Valid {
			uid, _ := uuid.Parse(watcherID.String)
			a.WatcherID = &uid
		}
		if runID.Valid {
			uid, _ := uuid.Parse(runID.String)
			a.RunID = &uid
		}
		a.Read = readInt != 0
		a.Dismissed = dismissedInt != 0
		a.CreatedAt, _ = time.Parse(time.RFC3339, alertCreated)

		g.Alerts = append(g.Alerts, a)
	}

	return g, rows.Err()
}

func (s *alertGroupStore) List(ctx context.Context, status string) ([]AlertGroup, error) {
	query := `SELECT g.id, g.title, g.status, g.severity,
	                  g.root_cause, g.resolution, g.created_at, g.updated_at, g.resolved_at,
	                  (SELECT COUNT(*) FROM alert_group_members WHERE group_id = g.id)
	           FROM alert_groups g WHERE 1=1`
	var args []any

	if status != "" {
		query += ` AND g.status = ?`
		args = append(args, status)
	}

	query += ` ORDER BY g.created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing alert groups: %w", err)
	}
	defer rows.Close()

	var result []AlertGroup
	for rows.Next() {
		var g AlertGroup
		var createdAt, updatedAt string
		var resolvedAt, rootCause, resolution sql.NullString

		if err := rows.Scan(
			&g.ID, &g.Title, &g.Status, &g.Severity,
			&rootCause, &resolution, &createdAt, &updatedAt, &resolvedAt,
			&g.AlertCount,
		); err != nil {
			return nil, fmt.Errorf("scanning alert group: %w", err)
		}

		g.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		g.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		if resolvedAt.Valid {
			t, _ := time.Parse(time.RFC3339, resolvedAt.String)
			g.ResolvedAt = &t
		}
		if rootCause.Valid {
			g.RootCause = &rootCause.String
		}
		if resolution.Valid {
			g.Resolution = &resolution.String
		}

		result = append(result, g)
	}

	return result, rows.Err()
}

func (s *alertGroupStore) Update(ctx context.Context, id string, params UpdateAlertGroupParams) error {
	now := time.Now().UTC().Format(time.RFC3339)

	setClauses := []string{"updated_at = ?"}
	args := []any{now}

	if params.Title != nil {
		setClauses = append(setClauses, "title = ?")
		args = append(args, *params.Title)
	}
	if params.Status != nil {
		setClauses = append(setClauses, "status = ?")
		args = append(args, *params.Status)
		if *params.Status == "resolved" {
			setClauses = append(setClauses, "resolved_at = ?")
			args = append(args, now)
		}
	}
	if params.Severity != nil {
		setClauses = append(setClauses, "severity = ?")
		args = append(args, *params.Severity)
	}
	if params.RootCause != nil {
		setClauses = append(setClauses, "root_cause = ?")
		args = append(args, *params.RootCause)
	}
	if params.Resolution != nil {
		setClauses = append(setClauses, "resolution = ?")
		args = append(args, *params.Resolution)
	}

	args = append(args, id)
	query := fmt.Sprintf("UPDATE alert_groups SET %s WHERE id = ?", joinStrings(setClauses, ", "))

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating alert group: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *alertGroupStore) AddAlerts(ctx context.Context, groupID string, alertIDs []string) error {
	for _, alertID := range alertIDs {
		_, err := s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO alert_group_members (group_id, alert_id) VALUES (?, ?)`,
			groupID, alertID,
		)
		if err != nil {
			return fmt.Errorf("adding alert to group: %w", err)
		}
	}
	// Touch updated_at
	now := time.Now().UTC().Format(time.RFC3339)
	s.db.ExecContext(ctx, `UPDATE alert_groups SET updated_at = ? WHERE id = ?`, now, groupID)
	return nil
}

func (s *alertGroupStore) RemoveAlert(ctx context.Context, groupID string, alertID string) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM alert_group_members WHERE group_id = ? AND alert_id = ?`,
		groupID, alertID,
	)
	if err != nil {
		return fmt.Errorf("removing alert from group: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *alertGroupStore) GetGroupForAlert(ctx context.Context, alertID string) (*AlertGroup, error) {
	var groupID string
	err := s.db.QueryRowContext(ctx,
		`SELECT group_id FROM alert_group_members WHERE alert_id = ?`, alertID,
	).Scan(&groupID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("finding group for alert: %w", err)
	}
	return s.GetByID(ctx, groupID)
}

func (s *alertGroupStore) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM alert_groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting alert group: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *alertGroupStore) AutoCorrelate(ctx context.Context, windowMinutes int) ([]AlertGroup, error) {
	if windowMinutes <= 0 {
		windowMinutes = 10
	}

	// Find pairs of alerts from different watchers within the time window, not already grouped
	rows, err := s.db.QueryContext(ctx, `
		SELECT a1.id, a2.id
		FROM alerts a1
		JOIN alerts a2 ON a1.id < a2.id
		  AND COALESCE(a1.watcher_id, '') != COALESCE(a2.watcher_id, '')
		  AND ABS(julianday(a1.created_at) - julianday(a2.created_at)) * 1440 < ?
		  AND a1.dismissed = 0 AND a2.dismissed = 0
		WHERE a1.id NOT IN (SELECT alert_id FROM alert_group_members)
		  AND a2.id NOT IN (SELECT alert_id FROM alert_group_members)
		ORDER BY a1.created_at DESC
		LIMIT 100`,
		windowMinutes,
	)
	if err != nil {
		return nil, fmt.Errorf("auto-correlating alerts: %w", err)
	}
	defer rows.Close()

	// Build connected components using union-find
	parent := make(map[string]string)

	find := func(x string) string {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}

	union := func(x, y string) {
		px, py := find(x), find(y)
		if px != py {
			parent[px] = py
		}
	}

	for rows.Next() {
		var id1, id2 string
		if err := rows.Scan(&id1, &id2); err != nil {
			return nil, fmt.Errorf("scanning correlation pair: %w", err)
		}
		if _, ok := parent[id1]; !ok {
			parent[id1] = id1
		}
		if _, ok := parent[id2]; !ok {
			parent[id2] = id2
		}
		union(id1, id2)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Build groups from components
	groups := make(map[string][]string)
	for id := range parent {
		root := find(id)
		groups[root] = append(groups[root], id)
	}

	// Only create groups with 2+ alerts
	var result []AlertGroup
	for _, alertIDs := range groups {
		if len(alertIDs) < 2 {
			continue
		}

		g, err := s.Create(ctx, CreateAlertGroupParams{
			Title:    fmt.Sprintf("Correlated alerts (%d)", len(alertIDs)),
			Severity: "warning",
			AlertIDs: alertIDs,
		})
		if err != nil {
			return nil, fmt.Errorf("creating correlated group: %w", err)
		}
		result = append(result, *g)
	}

	return result, nil
}

// joinStrings is a simple helper to join string slices.
func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}
