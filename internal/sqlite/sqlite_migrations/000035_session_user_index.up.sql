-- Index for user journey session queries (user_id + service + started_at)
CREATE INDEX IF NOT EXISTS idx_user_sessions_user_service_started
    ON user_sessions(user_id, service, started_at DESC);
