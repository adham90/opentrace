-- Drop investigation-related tables (replaced by MCP + Claude Code)
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS chats;
DROP TABLE IF EXISTS traces;
DROP TABLE IF EXISTS investigations;
DROP TYPE IF EXISTS investigation_status;
DROP TYPE IF EXISTS trace_step;
