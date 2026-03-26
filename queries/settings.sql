-- Settings: key-value application configuration stored in app_config.

-- name: GetSetting :one
SELECT value FROM app_config
WHERE key = sqlc.arg(key);

-- name: UpsertSetting :exec
INSERT INTO app_config (key, value)
VALUES (sqlc.arg(key), sqlc.arg(value))
ON CONFLICT(key) DO UPDATE SET value = excluded.value;
