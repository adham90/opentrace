package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/adham90/opentrace/pkg/store"
)

type errorImpactStore struct {
	db *sql.DB
	q  *Queries
}

// NewErrorImpactStore creates an ErrorImpactStore backed by SQLite.
func NewErrorImpactStore(db *sql.DB) store.ErrorImpactStore {
	return &errorImpactStore{db: db, q: New(db)}
}

// TrackImpact records or updates the impact of an error on a specific user.
func (s *errorImpactStore) TrackImpact(ctx context.Context, fingerprint string, userID string, contextData map[string]any, logID int64, service string) error {
	if fingerprint == "" || userID == "" {
		return nil // silently skip if no fingerprint or user
	}

	ctxJSON, _ := json.Marshal(contextData)
	now := time.Now().UTC().Format(time.RFC3339)

	return s.q.TrackErrorImpact(ctx, TrackErrorImpactParams{
		ErrorFingerprint: fingerprint,
		UserID:           userID,
		Service:          service,
		FirstSeenAt:      now,
		LastSeenAt:       now,
		LastContext:       string(ctxJSON),
		LastLogID:        logID,
	})
}

// GetImpact returns a summary of the user impact for an error fingerprint.
func (s *errorImpactStore) GetImpact(ctx context.Context, fingerprint string) (*store.ErrorImpact, error) {
	row, err := s.q.GetErrorImpact(ctx, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("getting impact: %w", err)
	}

	ei := &store.ErrorImpact{
		Fingerprint: fingerprint,
		UniqueUsers: int(row.UniqueUsers),
	}
	if row.TotalOccurrences.Valid {
		ei.TotalOccurrences = int(row.TotalOccurrences.Float64)
	}

	// Get the impact score from error_groups
	score, err := s.q.GetErrorImpactScore(ctx, fingerprint)
	if err == nil {
		ei.ImpactScore = score
	}

	// Get common traits
	traits, err := s.FindCommonTraits(ctx, fingerprint)
	if err == nil {
		ei.CommonTraits = traits
	}

	return ei, nil
}

// GetAffectedUsers returns users affected by a specific error, ordered by occurrence count.
func (s *errorImpactStore) GetAffectedUsers(ctx context.Context, fingerprint string, limit int) ([]store.AffectedUser, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.q.ListAffectedUsers(ctx, ListAffectedUsersParams{
		Fingerprint: fingerprint,
		RowLimit:    int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("getting affected users: %w", err)
	}
	return affectedUsersToStore(rows), nil
}

// GetUserErrors returns all errors affecting a specific user.
func (s *errorImpactStore) GetUserErrors(ctx context.Context, userID string, since time.Time) ([]store.ErrorSummary, error) {
	sinceStr := since.Format(time.RFC3339)

	qb := psql.Select(
		"ei.error_fingerprint",
		"COALESCE(eg.exception_class, '')",
		"COALESCE(eg.message, '')",
		"ei.occurrence_count",
		"ei.first_seen_at",
		"ei.last_seen_at",
		"COALESCE(eg.status, 'unresolved')").
		From("error_impacts ei").
		LeftJoin("error_groups eg ON eg.fingerprint = ei.error_fingerprint").
		Where(sq.Eq{"ei.user_id": userID}).
		Where("ei.last_seen_at >= ?", sinceStr).
		OrderBy("ei.last_seen_at DESC")

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building user errors query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
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

	// Collect impact data per fingerprint using sqlc
	impactRows, err := s.q.AggregateImpactData(ctx)
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
	for _, row := range impactRows {
		d := impactData{
			fingerprint: row.ErrorFingerprint,
			uniqueUsers: int(row.UniqueUsers),
		}
		if row.TotalOccurrences.Valid {
			d.totalOccurrences = int(row.TotalOccurrences.Float64)
		}
		if ls, ok := row.LastSeen.(string); ok {
			d.lastSeen, _ = time.Parse(time.RFC3339, ls)
		} else if ls, ok := row.LastSeen.([]byte); ok {
			d.lastSeen, _ = time.Parse(time.RFC3339, string(ls))
		}
		impacts = append(impacts, d)
	}

	// Update each error group using sqlc
	for _, d := range impacts {
		recencyWeight := computeRecencyWeight(now, d.lastSeen)
		score := float64(d.uniqueUsers) * math.Log2(float64(d.totalOccurrences+1)) * recencyWeight

		// Find common traits
		traits, _ := s.FindCommonTraits(ctx, d.fingerprint)
		traitsJSON, _ := json.Marshal(traits)

		err := s.q.UpdateErrorGroupImpact(ctx, UpdateErrorGroupImpactParams{
			UniqueUsers:   int64(d.uniqueUsers),
			ImpactScore:   round2(score),
			CommonContext: string(traitsJSON),
			Fingerprint:   d.fingerprint,
		})
		if err != nil {
			return fmt.Errorf("updating impact score for %s: %w", d.fingerprint, err)
		}
	}

	return nil
}

// TopByImpact returns error groups ranked by impact.
func (s *errorImpactStore) TopByImpact(ctx context.Context, params store.ImpactQueryParams) ([]store.ErrorGroupWithImpact, error) {
	qb := psql.Select(
		"eg.fingerprint", "eg.service", "eg.environment",
		"eg.exception_class", "eg.message", "eg.source_file", "eg.source_line",
		"eg.status", "eg.first_seen_at", "eg.last_seen_at",
		"eg.occurrence_count", "eg.last_log_id", "eg.reopened_count",
		"eg.unique_users", "eg.impact_score", "COALESCE(eg.common_context, '{}')").
		From("error_groups eg")

	if params.Status != "" {
		qb = qb.Where(sq.Eq{"eg.status": string(params.Status)})
	}
	if params.Service != "" {
		qb = qb.Where(sq.Eq{"eg.service": params.Service})
	}
	if !params.Since.IsZero() {
		qb = qb.Where("eg.last_seen_at >= ?", params.Since.Format(time.RFC3339))
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
	qb = qb.OrderBy(sortCol + " DESC")

	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	qb = qb.Limit(uint64(limit))

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building top by impact query: %w", err)
	}

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

