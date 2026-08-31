-- ============================================================================
-- 000003 — per-user catch-up cursor.
--
-- overview.catchup answers "what happened while I was gone" per caller, so the
-- cursor is per user rather than global: on a two- or three-person team, one
-- person draining the queue must not hide the same incident from everyone else.
--
-- NULL means "never caught up" — the handler falls back to a bounded default
-- window rather than replaying all of history on the first call.
-- ============================================================================

ALTER TABLE users ADD COLUMN last_catchup_at TEXT;
