package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type errorImpactStore struct {
	db *bun.DB
}

// NewErrorImpactStore creates an ErrorImpactStore backed by SQLite.
func NewErrorImpactStore(db *bun.DB) store.ErrorImpactStore {
	return &errorImpactStore{db: db}
}

// TrackImpact records or updates the impact of an error on a specific user,
// scoped to (fingerprint, environment). A user who hits the same fingerprint
// in both staging and production will have two distinct rows.
func (s *errorImpactStore) TrackImpact(ctx context.Context, fingerprint, environment, userID string, contextData map[string]any, logID int64, service string) error {
	if fingerprint == "" || userID == "" {
		return nil // silently skip if no fingerprint or user
	}

	ctxJSON, _ := json.Marshal(contextData)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.NewRaw(`
		INSERT INTO error_impacts (error_fingerprint, environment, user_id, service, first_seen_at, last_seen_at,
			last_context, last_log_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(error_fingerprint, environment, user_id) DO UPDATE SET
			occurrence_count = occurrence_count + 1,
			last_seen_at = excluded.last_seen_at,
			last_context = excluded.last_context,
			last_log_id = excluded.last_log_id`,
		fingerprint, environment, userID, service, now, now, string(ctxJSON), logID,
	).Exec(ctx)
	return err
}

// GetImpact returns a summary of the user impact for an error fingerprint.
func (s *errorImpactStore) GetImpact(ctx context.Context, fingerprint string) (*store.ErrorImpact, error) {
	var uniqueUsers int
	var totalOccurrences sql.NullFloat64

	err := s.db.NewRaw(`
		SELECT COUNT(*) AS unique_users, SUM(occurrence_count) AS total_occurrences
		FROM error_impacts WHERE error_fingerprint = ?`, fingerprint,
	).Scan(ctx, &uniqueUsers, &totalOccurrences)
	if err != nil {
		return nil, fmt.Errorf("getting impact: %w", err)
	}

	ei := &store.ErrorImpact{
		Fingerprint: fingerprint,
		UniqueUsers: uniqueUsers,
	}
	if totalOccurrences.Valid {
		ei.TotalOccurrences = int(totalOccurrences.Float64)
	}

	// Get the impact score from error_groups. With the composite PK,
	// a fingerprint can have multiple rows (one per env). Take the max
	// score — the aggregate GetImpact view mirrors the highest-impact env.
	var score float64
	err = s.db.NewRaw(
		`SELECT COALESCE(MAX(impact_score), 0) FROM error_groups WHERE fingerprint = ?`,
		fingerprint,
	).Scan(ctx, &score)
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

	type row struct {
		UserID          string `bun:"user_id"`
		OccurrenceCount int    `bun:"occurrence_count"`
		FirstSeenAt     string `bun:"first_seen_at"`
		LastSeenAt      string `bun:"last_seen_at"`
		LastContext      string `bun:"last_context"`
	}

	var rows []row
	err := s.db.NewRaw(`
		SELECT user_id, occurrence_count, first_seen_at, last_seen_at, last_context
		FROM error_impacts
		WHERE error_fingerprint = ?
		ORDER BY occurrence_count DESC
		LIMIT ?`, fingerprint, limit,
	).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("getting affected users: %w", err)
	}

	users := make([]store.AffectedUser, len(rows))
	for i, r := range rows {
		users[i] = store.AffectedUser{
			UserID:          r.UserID,
			OccurrenceCount: r.OccurrenceCount,
			FirstSeenAt:     parseTime(r.FirstSeenAt),
			LastSeenAt:      parseTime(r.LastSeenAt),
		}
		if r.LastContext != "" && r.LastContext != "{}" {
			json.Unmarshal([]byte(r.LastContext), &users[i].LastContext)
		}
	}
	return users, nil
}

// GetUserErrors returns all errors affecting a specific user.
func (s *errorImpactStore) GetUserErrors(ctx context.Context, userID string, since time.Time) ([]store.ErrorSummary, error) {
	sinceStr := since.Format(time.RFC3339)

	type row struct {
		Fingerprint     string `bun:"error_fingerprint"`
		ExceptionClass  string `bun:"exception_class"`
		Message         string `bun:"message"`
		OccurrenceCount int    `bun:"occurrence_count"`
		FirstSeenAt     string `bun:"first_seen_at"`
		LastSeenAt      string `bun:"last_seen_at"`
		Status          string `bun:"status"`
	}

	var rows []row
	err := s.db.NewRaw(`
		SELECT ei.error_fingerprint,
			COALESCE(eg.exception_class, '') AS exception_class,
			COALESCE(eg.message, '') AS message,
			ei.occurrence_count,
			ei.first_seen_at,
			ei.last_seen_at,
			COALESCE(eg.status, 'unresolved') AS status
		FROM error_impacts ei
		LEFT JOIN error_groups eg
			ON eg.fingerprint = ei.error_fingerprint
			AND eg.environment = ei.environment
		WHERE ei.user_id = ? AND ei.last_seen_at >= ?
		ORDER BY ei.last_seen_at DESC`, userID, sinceStr,
	).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("getting user errors: %w", err)
	}

	results := make([]store.ErrorSummary, len(rows))
	for i, r := range rows {
		results[i] = store.ErrorSummary{
			Fingerprint:     r.Fingerprint,
			ExceptionClass:  r.ExceptionClass,
			Message:         r.Message,
			OccurrenceCount: r.OccurrenceCount,
			FirstSeenAt:     parseTime(r.FirstSeenAt),
			LastSeenAt:      parseTime(r.LastSeenAt),
			Status:          store.ErrorGroupStatus(r.Status),
		}
	}
	return results, nil
}

// ComputeImpactScores recalculates impact scores for all error groups.
// score = unique_users * log2(total_occurrences + 1) * recency_weight
func (s *errorImpactStore) ComputeImpactScores(ctx context.Context) error {
	now := time.Now().UTC()

	// Collect impact data per fingerprint
	type impactRow struct {
		ErrorFingerprint string          `bun:"error_fingerprint"`
		UniqueUsers      int             `bun:"unique_users"`
		TotalOccurrences sql.NullFloat64 `bun:"total_occurrences"`
		LastSeen         string          `bun:"last_seen"`
	}

	var impactRows []impactRow
	err := s.db.NewRaw(`
		SELECT error_fingerprint,
			COUNT(*) AS unique_users,
			SUM(occurrence_count) AS total_occurrences,
			MAX(last_seen_at) AS last_seen
		FROM error_impacts
		GROUP BY error_fingerprint`,
	).Scan(ctx, &impactRows)
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
			uniqueUsers: row.UniqueUsers,
		}
		if row.TotalOccurrences.Valid {
			d.totalOccurrences = int(row.TotalOccurrences.Float64)
		}
		d.lastSeen, _ = time.Parse(time.RFC3339, row.LastSeen)
		impacts = append(impacts, d)
	}

	// First pass: compute scores for all error groups (cheap — no extra queries).
	type scored struct {
		impactData
		score float64
	}
	scoredImpacts := make([]scored, len(impacts))
	for i, d := range impacts {
		recencyWeight := computeRecencyWeight(now, d.lastSeen)
		scoredImpacts[i] = scored{
			impactData: d,
			score:      float64(d.uniqueUsers) * math.Log2(float64(d.totalOccurrences+1)) * recencyWeight,
		}
	}

	// Second pass: update all scores, but only compute expensive common traits
	// for the top 50 error groups by score (avoids N+1 for long-tail errors).
	// Sort by score descending.
	sort.Slice(scoredImpacts, func(i, j int) bool {
		return scoredImpacts[i].score > scoredImpacts[j].score
	})

	const maxTraitComputations = 50
	for i, d := range scoredImpacts {
		var traitsJSON []byte
		if i < maxTraitComputations {
			traits, _ := s.FindCommonTraits(ctx, d.fingerprint)
			traitsJSON, _ = json.Marshal(traits)
		}

		traitStr := "{}"
		if len(traitsJSON) > 0 {
			traitStr = string(traitsJSON)
		}

		_, err := s.db.NewRaw(`
			UPDATE error_groups SET unique_users = ?, impact_score = ?, common_context = ?
			WHERE fingerprint = ?`,
			d.uniqueUsers, round2(d.score), traitStr, d.fingerprint,
		).Exec(ctx)
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
		query += " WHERE " + joinConditions(conditions)
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
	var contexts []string
	err := s.db.NewRaw(`
		SELECT last_context FROM error_impacts
		WHERE error_fingerprint = ? AND last_context != '{}'
		ORDER BY last_seen_at DESC LIMIT 100`,
		fingerprint,
	).Scan(ctx, &contexts)
	if err != nil {
		return nil, fmt.Errorf("querying contexts: %w", err)
	}

	// Count occurrences of each key-value pair
	keyCounts := make(map[string]map[string]int) // key -> value -> count
	total := 0
	for _, ctxJSON := range contexts {
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

func joinConditions(conditions []string) string {
	return strings.Join(conditions, " AND ")
}

// round2 rounds a float64 to 2 decimal places.
func round2(f float64) float64 {
	return math.Round(f*100) / 100
}
