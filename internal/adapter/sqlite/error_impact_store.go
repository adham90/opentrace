package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	return trackErrorImpact(ctx, s.db, fingerprint, environment, userID, contextData, logID, service)
}

func (s *errorImpactStore) TrackImpactBatch(ctx context.Context, entries []store.LogEntry) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for i := range entries {
			e := &entries[i]
			if err := trackErrorImpact(ctx, tx, e.ErrorFingerprint, e.Environment, e.UserID, e.Metadata, e.ID, e.Service); err != nil {
				return err
			}
		}
		return nil
	})
}

func trackErrorImpact(ctx context.Context, db bun.IDB, fingerprint, environment, userID string, contextData map[string]any, logID int64, service string) error {
	if fingerprint == "" || userID == "" {
		return nil // silently skip if no fingerprint or user
	}

	ctxJSON, _ := json.Marshal(contextData)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := db.NewRaw(`
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
//
// A non-empty environment scopes every number to that env, which is what
// env-pinned callers need: the cross-env aggregate names only its
// highest-impact env, so gating on that env would deny a caller whose own env
// simply scores lower. Passing "" keeps the all-env aggregate.
func (s *errorImpactStore) GetImpact(ctx context.Context, fingerprint, environment string) (*store.ErrorImpact, error) {
	var uniqueUsers int
	var totalOccurrences sql.NullFloat64

	impactQuery := `
		SELECT COUNT(*) AS unique_users, SUM(occurrence_count) AS total_occurrences
		FROM error_impacts WHERE error_fingerprint = ?`
	impactArgs := []any{fingerprint}
	if environment != "" {
		impactQuery += ` AND environment = ?`
		impactArgs = append(impactArgs, environment)
	}

	err := s.db.NewRaw(impactQuery, impactArgs...).Scan(ctx, &uniqueUsers, &totalOccurrences)
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

	// Score and environment come from the same error_groups row. With the
	// composite PK a fingerprint has one row per env; the aggregate GetImpact
	// view mirrors the highest-impact env, so report that env alongside its
	// score. Environment must be populated: env-scoped callers gate on it, and
	// an empty value fails every scope check.
	var env string
	var score float64
	groupQuery := `
		SELECT environment, impact_score FROM error_groups
		WHERE fingerprint = ?`
	groupArgs := []any{fingerprint}
	if environment != "" {
		groupQuery += ` AND environment = ?`
		groupArgs = append(groupArgs, environment)
	}
	groupQuery += `
		ORDER BY impact_score DESC, last_seen_at DESC
		LIMIT 1`
	err = s.db.NewRaw(groupQuery, groupArgs...).Scan(ctx, &env, &score)
	if err == nil {
		ei.Environment = env
		ei.ImpactScore = score
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("getting impact score: %w", err)
	}

	// Get common traits, scoped to the same environment as the numbers above.
	traits, err := s.findCommonTraits(ctx, fingerprint, environment)
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
		LastContext     string `bun:"last_context"`
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

// maxTraitComputations caps how many groups get the expensive common-trait
// analysis per scoring run (avoids an N+1 over long-tail errors).
const maxTraitComputations = 50

// scoredImpact is one (fingerprint, environment) group with its computed score.
type scoredImpact struct {
	fingerprint      string
	environment      string
	uniqueUsers      int
	totalOccurrences int
	lastSeen         time.Time
	score            float64
}

// ComputeImpactScores recalculates impact scores for all error groups.
// score = unique_users * log2(total_occurrences + 1) * recency_weight
//
// error_groups is keyed (fingerprint, environment) and error_impacts rows are
// env-scoped, so the aggregation is per environment: a fingerprint hit by 40
// staging testers and 2 production users must not stamp 42 unique users and a
// staging-derived score onto its production row.
func (s *errorImpactStore) ComputeImpactScores(ctx context.Context) error {
	impacts, err := s.collectScoredImpacts(ctx)
	if err != nil {
		return err
	}

	// Highest-scoring groups first, so the trait budget goes to the ones that
	// matter.
	sort.Slice(impacts, func(i, j int) bool {
		return impacts[i].score > impacts[j].score
	})

	for i, d := range impacts {
		if i < maxTraitComputations {
			traits, _ := s.findCommonTraits(ctx, d.fingerprint, d.environment)
			traitsJSON, mErr := json.Marshal(traits)
			if mErr == nil && len(traitsJSON) > 0 {
				_, err = s.db.NewRaw(`
					UPDATE error_groups
					SET unique_users = ?, impact_score = ?, common_context = ?
					WHERE fingerprint = ? AND environment = ?`,
					d.uniqueUsers, round2(d.score), string(traitsJSON),
					d.fingerprint, d.environment,
				).Exec(ctx)
			}
		} else {
			// Outside the trait budget: leave common_context alone rather than
			// overwriting previously computed traits with "{}".
			_, err = s.db.NewRaw(`
				UPDATE error_groups SET unique_users = ?, impact_score = ?
				WHERE fingerprint = ? AND environment = ?`,
				d.uniqueUsers, round2(d.score), d.fingerprint, d.environment,
			).Exec(ctx)
		}
		if err != nil {
			return fmt.Errorf("updating impact score for %s (%s): %w",
				d.fingerprint, d.environment, err)
		}
	}

	return nil
}

// collectScoredImpacts aggregates error_impacts per (fingerprint, environment)
// and computes each group's impact score.
func (s *errorImpactStore) collectScoredImpacts(ctx context.Context) ([]scoredImpact, error) {
	type impactRow struct {
		ErrorFingerprint string          `bun:"error_fingerprint"`
		Environment      string          `bun:"environment"`
		UniqueUsers      int             `bun:"unique_users"`
		TotalOccurrences sql.NullFloat64 `bun:"total_occurrences"`
		LastSeen         string          `bun:"last_seen"`
	}

	var rows []impactRow
	err := s.db.NewRaw(`
		SELECT error_fingerprint,
			environment,
			COUNT(*) AS unique_users,
			SUM(occurrence_count) AS total_occurrences,
			MAX(last_seen_at) AS last_seen
		FROM error_impacts
		GROUP BY error_fingerprint, environment`,
	).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("querying impact data: %w", err)
	}

	now := time.Now().UTC()
	impacts := make([]scoredImpact, len(rows))
	for i, row := range rows {
		d := scoredImpact{
			fingerprint: row.ErrorFingerprint,
			environment: row.Environment,
			uniqueUsers: row.UniqueUsers,
			lastSeen:    parseTime(row.LastSeen),
		}
		if row.TotalOccurrences.Valid {
			d.totalOccurrences = int(row.TotalOccurrences.Float64)
		}
		d.score = float64(d.uniqueUsers) *
			math.Log2(float64(d.totalOccurrences+1)) *
			computeRecencyWeight(now, d.lastSeen)
		impacts[i] = d
	}
	return impacts, nil
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
	return s.findCommonTraits(ctx, fingerprint, "")
}

// maxTraitContexts caps how many context blobs one trait analysis reads.
const maxTraitContexts = 100

// findCommonTraits is FindCommonTraits scoped to one environment. environment
// "" analyses every env, which is what the exported aggregate view wants; the
// per-env scoring path passes a concrete env so a production group's traits
// are not dominated by staging context.
func (s *errorImpactStore) findCommonTraits(ctx context.Context, fingerprint, environment string) (map[string]any, error) {
	query := `
		SELECT last_context FROM error_impacts
		WHERE error_fingerprint = ? AND last_context != '{}'
		ORDER BY last_seen_at DESC LIMIT ?`
	args := []any{fingerprint, maxTraitContexts}
	if environment != "" {
		query = `
		SELECT last_context FROM error_impacts
		WHERE error_fingerprint = ? AND environment = ? AND last_context != '{}'
		ORDER BY last_seen_at DESC LIMIT ?`
		args = []any{fingerprint, environment, maxTraitContexts}
	}

	var contexts []string
	err := s.db.NewRaw(query, args...).Scan(ctx, &contexts)
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
