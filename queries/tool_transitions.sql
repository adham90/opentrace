-- Tool transitions: pre-computed tool sequence statistics for ranking.
-- Table: tool_transitions (migration 000040)

-- name: IncrementToolTransition :exec
INSERT INTO tool_transitions (from_tool, to_tool, intent, total_count, last_seen_at)
VALUES (sqlc.arg(from_tool), sqlc.arg(to_tool), sqlc.arg(intent), 1, datetime('now'))
ON CONFLICT(from_tool, to_tool, intent) DO UPDATE SET
  total_count = total_count + 1,
  last_seen_at = datetime('now');

-- name: IncrementToolTransitionWithOutcome :exec
INSERT INTO tool_transitions (from_tool, to_tool, intent, total_count, resolved_count, abandoned_count, last_seen_at)
VALUES (sqlc.arg(from_tool), sqlc.arg(to_tool), sqlc.arg(intent), 1,
        sqlc.arg(resolved_inc), sqlc.arg(abandoned_inc), datetime('now'))
ON CONFLICT(from_tool, to_tool, intent) DO UPDATE SET
  total_count = total_count + 1,
  resolved_count = resolved_count + sqlc.arg(resolved_inc),
  abandoned_count = abandoned_count + sqlc.arg(abandoned_inc),
  last_seen_at = datetime('now');

-- name: PruneToolTransitions :execrows
DELETE FROM tool_transitions WHERE last_seen_at < sqlc.arg(cutoff);
