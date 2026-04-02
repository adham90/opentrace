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
