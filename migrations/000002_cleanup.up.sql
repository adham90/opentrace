-- ============================================================================
-- Cleanup: drop tables for removed features
--
-- Phase 1: Investigation session intelligence layer
-- Phase 2: Journey store
-- Phase 3: Deploys table (deploy_markers remains as single source of truth)
-- Phase 4: Events table (dead infrastructure — no MCP tool reads from it)
-- ============================================================================

-- Investigation intelligence layer
DROP TABLE IF EXISTS investigation_sessions;
DROP TABLE IF EXISTS tool_transitions;
DROP TABLE IF EXISTS workflow_templates;
DROP TABLE IF EXISTS query_memory;
DROP TABLE IF EXISTS runbook_effectiveness;
DROP TABLE IF EXISTS uncovered_error_paths;

-- Journey store
DROP TABLE IF EXISTS user_sessions;
DROP TABLE IF EXISTS funnels;

-- Deploys (replaced by deploy_markers auto-detection)
DROP TABLE IF EXISTS deploys;

-- Events (unused — no MCP tool consumed this data)
DROP TABLE IF EXISTS events;

-- ============================================================================
-- Drop redundant/unused indexes on logs table to improve write throughput.
-- Every query filters by timestamp, usually with service/level.
-- The compound indexes already cover all query patterns.
-- ============================================================================

-- Redundant: covered by idx_logs_service_timestamp, idx_logs_ts_service_env, idx_logs_ts_level
DROP INDEX IF EXISTS idx_logs_timestamp;

-- Redundant: covered by idx_logs_service_timestamp, idx_logs_service_level_timestamp
DROP INDEX IF EXISTS idx_logs_service;

-- Redundant: covered by idx_logs_level_timestamp, idx_logs_service_level_timestamp
DROP INDEX IF EXISTS idx_logs_level;

-- Redundant: idx_logs_level_timestamp covers (level, timestamp) queries
DROP INDEX IF EXISTS idx_logs_ts_level;

-- Redundant: idx_logs_service_timestamp covers (service, timestamp); environment rarely filtered alone
DROP INDEX IF EXISTS idx_logs_ts_service_env;

-- Unused: no MCP tool queries by session_id (journey store removed)
DROP INDEX IF EXISTS idx_logs_session_service_ts;
DROP INDEX IF EXISTS idx_logs_session_id;

-- Redundant: covered by idx_logs_event_type_timestamp
DROP INDEX IF EXISTS idx_logs_event_type;
