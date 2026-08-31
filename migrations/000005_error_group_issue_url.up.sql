-- ============================================================================
-- 000005 — link an error group to the GitHub issue filed for it.
--
-- The on-call agent files a diagnosis somewhere durable so the operator can act
-- on it when they wake up. Without this column it would file a fresh issue on
-- every recurrence, and a flapping error would produce a page of duplicates —
-- which is worse than silence, because it trains people to ignore the label.
--
-- error_groups is keyed (fingerprint, environment), so this is one issue per
-- distinct crash per environment. NULL means "not filed".
-- ============================================================================

ALTER TABLE error_groups ADD COLUMN issue_url TEXT;
