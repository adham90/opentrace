ALTER TABLE watchers ADD COLUMN time_range TEXT NOT NULL DEFAULT '15m';

-- Backfill from filters JSON if available, else derive from interval_seconds
UPDATE watchers SET time_range = COALESCE(
    NULLIF(filters->>'time_range', ''),
    CASE
        WHEN interval_seconds <= 300 THEN '5m'
        WHEN interval_seconds <= 900 THEN '15m'
        WHEN interval_seconds <= 1800 THEN '30m'
        WHEN interval_seconds <= 3600 THEN '1h'
        WHEN interval_seconds <= 21600 THEN '6h'
        ELSE '24h'
    END
);

ALTER TABLE watchers DROP COLUMN interval_seconds;
