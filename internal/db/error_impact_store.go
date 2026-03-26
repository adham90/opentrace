package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

type errorImpactStore struct {
	db *sql.DB
}

// NewErrorImpactStore creates an ErrorImpactStore backed by SQLite.
func NewErrorImpactStore(db *sql.DB) store.ErrorImpactStore {
	return &errorImpactStore{db: db}
}

// TrackImpact records or updates the impact of an error on a specific user.
func (s *errorImpactStore) TrackImpact(ctx context.Context, fingerprint string, userID string, contextData map[string]any, logID int64, service string) error {
	if fingerprint == "" || userID == "" {
		return nil // silently skip if no fingerprint or user
	}

	ctxJSON, _ := json.Marshal(contextData)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO error_impacts (error_fingerprint, user_id, service, first_seen_at, last_seen_at, occurrence_count, last_context, last_log_id)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(error_fingerprint, user_id) DO UPDATE SET
			last_seen_at = excluded.last_seen_at,
			occurrence_count = error_impacts.occurrence_count + 1,
			last_context = excluded.last_context,
			last_log_id = excluded.last_log_id
	`, fingerprint, userID, service, now, now, string(ctxJSON), logID)
	if err != nil {
		return fmt.Errorf("tracking impact: %w", err)
	}

	return nil
}

// GetImpact returns a summary of the user impact for an error fingerprint.
func (s *errorImpactStore) GetImpact(ctx context.Context, fingerprint string) (*store.ErrorImpact, error) {
	var ei store.ErrorImpact
	ei.Fingerprint = fingerprint

	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT user_id) as unique_users,
		       SUM(occurrence_count) as total_occurrences
		FROM error_impacts
		WHERE error_fingerprint = ?
	`, fingerprint).Scan(&ei.UniqueUsers, &ei.TotalOccurrences)
	if err != nil {
		return nil, fmt.Errorf("getting impact: %w", err)
	}

	// Get the impact score from error_groups
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(impact_score, 0) FROM error_groups WHERE fingerprint = ?
	`, fingerprint).Scan(&ei.ImpactScore)

	// Get common traits
	traits, err := s.FindCommonTraits(ctx, fingerprint)
	if err == nil {
		ei.CommonTraits = traits
	}

	return &ei, nil
}

// GetAffectedUsers returns users affected by a specific error, ordered by occurrence count.
func (s *errorImpactStore) GetAffectedUsers(ctx context.Context, fingerprint string, limit int) ([]store.AffectedUser, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, occurrence_count, first_seen_at, last_seen_at, last_context
		FROM error_impacts
		WHERE error_fingerprint = ?
		ORDER BY occurrence_count DESC
		LIMIT ?
	`, fingerprint, limit)
	if err != nil {
		return nil, fmt.Errorf("getting affected users: %w", err)
	}
	defer rows.Close()

	var results []store.AffectedUser
	for rows.Next() {
		var au store.AffectedUser
		var firstStr, lastStr, ctxJSON string
		if err := rows.Scan(&au.UserID, &au.OccurrenceCount, &firstStr, &lastStr, &ctxJSON); err != nil {
			return nil, fmt.Errorf("scanning affected user: %w", err)
		}
		au.FirstSeenAt, _ = time.Parse(time.RFC3339, firstStr)
		au.LastSeenAt, _ = time.Parse(time.RFC3339, lastStr)
		if ctxJSON != "" && ctxJSON != "{}" {
			json.Unmarshal([]byte(ctxJSON), &au.LastContext)
		}
		results = append(results, au)
	}
	return results, rows.Err()
}

// GetUserErrors returns all errors affecting a specific user.
func (s *errorImpactStore) GetUserErrors(ctx context.Context, userID string, since time.Time) ([]store.ErrorSummary, error) {
	sinceStr := since.Format(time.RFC3339)

	rows, err := s.db.QueryContext(ctx, `
		SELECT ei.error_fingerprint,
		       COALESCE(eg.exception_class, '') as exception_class,
		       COALESCE(eg.message, '') as message,
		       ei.occurrence_count,
		       ei.first_seen_at,
		       ei.last_seen_at,
		       COALESCE(eg.status, 'unresolved') as status
		FROM error_impacts ei
		LEFT JOIN error_groups eg ON eg.fingerprint = ei.error_fingerprint
		WHERE ei.user_id = ? AND ei.last_seen_at >= ?
		ORDER BY ei.last_seen_at DESC
	`, userID, sinceStr)
	if err != nil {
		return nil, fmt.Errorf("getting user errors: %w", err)
	}
	defer rows.Close()

	var results []store.ErrorSummary
	for rows.Next() {
		var es store.ErrorSummary
		var firstStr, lastStr string
		if err := rows.Scan(&es.Fingerprint, &es.ExceptionClass, &es.Message,
			&es.OccurrenceCount, &firstStr, &lastStr, &es.Status); err != nil {
			return nil, fmt.Errorf("scanning user error: %w", err)
		}
		es.FirstSeenAt, _ = time.Parse(time.RFC3339, firstStr)
		es.LastSeenAt, _ = time.Parse(time.RFC3339, lastStr)
		results = append(results, es)
	}
	return results, rows.Err()
}

// ComputeImpactScores recalculates impact scores for all error groups.
// score = unique_users * log2(total_occurrences + 1) * recency_weight
func (s *errorImpactStore) ComputeImpactScores(ctx context.Context) error {
	now := time.Now().UTC()

	// Collect impact data per fingerprint
	rows, err := s.db.QueryContext(ctx, `
		SELECT error_fingerprint,
		       COUNT(DISTINCT user_id) as unique_users,
		       SUM(occurrence_count) as total_occurrences,
		       MAX(last_seen_at) as last_seen
		FROM error_impacts
		GROUP BY error_fingerprint
	`)
	if err != nil {
		return fmt.Errorf("querying impact data: %w", err)
	}

	type impactData struct {
		fingerprint      string
		uniqueUsers      int
		totalOccurrences int
		lastSeen         time.Time
	}
	var impacts []impactData
	for rows.Next() {
		var d impactData
		var lastSeenStr string
		if err := rows.Scan(&d.fingerprint, &d.uniqueUsers, &d.totalOccurrences, &lastSeenStr); err != nil {
			rows.Close()
			return fmt.Errorf("scanning impact data: %w", err)
		}
		d.lastSeen, _ = time.Parse(time.RFC3339, lastSeenStr)
		impacts = append(impacts, d)
	}
	rows.Close()

	// Update each error group
	for _, d := range impacts {
		recencyWeight := computeRecencyWeight(now, d.lastSeen)
		score := float64(d.uniqueUsers) * math.Log2(float64(d.totalOccurrences+1)) * recencyWeight

		// Find common traits
		traits, _ := s.FindCommonTraits(ctx, d.fingerprint)
		traitsJSON, _ := json.Marshal(traits)

		_, err := s.db.ExecContext(ctx, `
			UPDATE error_groups
			SET unique_users = ?, impact_score = ?, common_context = ?
			WHERE fingerprint = ?
		`, d.uniqueUsers, round2(score), string(traitsJSON), d.fingerprint)
		if err != nil {
			return fmt.Errorf("updating impact score for %s: %w", d.fingerprint, err)
		}
	}

	return nil
}

// TopByImpact returns error groups ranked by impact.
func (s *errorImpactStore) TopByImpact(ctx context.Context, params store.ImpactQueryParams) ([]store.ErrorGroupWithImpact, error) {
	var conditions []string
	var args []any

	if params.Status != "" {
		conditions = append(conditions, "eg.status = ?")
		args = append(args, string(params.Status))
	}
	if params.Service != "" {
		conditions = append(conditions, "eg.service = ?")
		args = append(args, params.Service)
	}
	if !params.Since.IsZero() {
		conditions = append(conditions, "eg.last_seen_at >= ?")
		args = append(args, params.Since.Format(time.RFC3339))
	}

	sortCol := "eg.impact_score"
	switch params.SortBy {
	case "unique_users":
		sortCol = "eg.unique_users"
	case "occurrence_count":
		sortCol = "eg.occurrence_count"
	case "last_seen":
		sortCol = "eg.last_seen_at"
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}

	query := `SELECT eg.fingerprint, eg.service, eg.environment,
		eg.exception_class, eg.message, eg.source_file, eg.source_line,
		eg.status, eg.first_seen_at, eg.last_seen_at,
		eg.occurrence_count, eg.last_log_id, eg.reopened_count,
		eg.unique_users, eg.impact_score, COALESCE(eg.common_context, '{}')
		FROM error_groups eg`

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY " + sortCol + " DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying top by impact: %w", err)
	}
	defer rows.Close()

	// Collect all results first to avoid deadlock with MaxOpenConns=1
	var results []store.ErrorGroupWithImpact
	for rows.Next() {
		var eg store.ErrorGroupWithImpact
		var firstStr, lastStr string
		var lastLogID sql.NullInt64
		var commonCtxJSON string
		if err := rows.Scan(&eg.Fingerprint, &eg.Service, &eg.Environment,
			&eg.ExceptionClass, &eg.Message, &eg.SourceFile, &eg.SourceLine,
			&eg.Status, &firstStr, &lastStr,
			&eg.OccurrenceCount, &lastLogID, &eg.ReopenedCount,
			&eg.UniqueUsers, &eg.ImpactScore, &commonCtxJSON); err != nil {
			return nil, fmt.Errorf("scanning error group: %w", err)
		}
		eg.FirstSeenAt, _ = time.Parse(time.RFC3339, firstStr)
		eg.LastSeenAt, _ = time.Parse(time.RFC3339, lastStr)
		if lastLogID.Valid {
			id := lastLogID.Int64
			eg.LastLogID = &id
		}
		if commonCtxJSON != "" && commonCtxJSON != "{}" {
			json.Unmarshal([]byte(commonCtxJSON), &eg.CommonContext)
		}
		results = append(results, eg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Now fetch top affected users (safe: rows are closed)
	for i := range results {
		users, _ := s.GetAffectedUsers(ctx, results[i].Fingerprint, 3)
		results[i].TopAffectedUsers = users
	}

	return results, nil
}

// FindCommonTraits analyzes the context data across all affected users to find
// common patterns (e.g., "87% Safari", "92% iOS").
func (s *errorImpactStore) FindCommonTraits(ctx context.Context, fingerprint string) (map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT last_context FROM error_impacts
		WHERE error_fingerprint = ? AND last_context != '{}'
	`, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("querying contexts: %w", err)
	}
	defer rows.Close()

	// Count occurrences of each key-value pair
	keyCounts := make(map[string]map[string]int) // key → value → count
	total := 0
	for rows.Next() {
		var ctxJSON string
		if err := rows.Scan(&ctxJSON); err != nil {
			continue
		}
		var ctxMap map[string]any
		if json.Unmarshal([]byte(ctxJSON), &ctxMap) != nil {
			continue
		}
		total++
		for k, v := range ctxMap {
			valStr := fmt.Sprintf("%v", v)
			if _, ok := keyCounts[k]; !ok {
				keyCounts[k] = make(map[string]int)
			}
			keyCounts[k][valStr]++
		}
	}

	if total == 0 {
		return nil, nil
	}

	// Build traits: only include keys where a single value dominates (>50%)
	traits := make(map[string]any)
	for key, valueCounts := range keyCounts {
		// Find the distribution
		dist := make(map[string]float64)
		for val, count := range valueCounts {
			pct := float64(count) / float64(total)
			if pct >= 0.1 { // only show values with >= 10% share
				dist[val] = round2(pct)
			}
		}
		if len(dist) > 0 {
			traits[key] = dist
		}
	}

	return traits, nil
}

// computeRecencyWeight returns a weight based on how recently the error was seen.
func computeRecencyWeight(now, lastSeen time.Time) float64 {
	age := now.Sub(lastSeen)
	switch {
	case age < time.Hour:
		return 1.0
	case age < 24*time.Hour:
		return 0.8
	case age < 7*24*time.Hour:
		return 0.5
	default:
		return 0.2
	}
}
