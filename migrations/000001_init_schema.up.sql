-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Connector type enum
CREATE TYPE connector_type AS ENUM ('logs', 'database', 'codebase', 'monitoring');

-- Connector status enum
CREATE TYPE connector_status AS ENUM ('connected', 'disconnected', 'error');

-- Investigation status enum
CREATE TYPE investigation_status AS ENUM ('active', 'completed', 'error');

-- Trace step types enum
CREATE TYPE trace_step AS ENUM ('thinking', 'tool_call', 'observation', 'final', 'error');

-- Data Sources (user-configured connections)
CREATE TABLE data_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type connector_type NOT NULL,
    name TEXT NOT NULL,
    config JSONB NOT NULL DEFAULT '{}',
    status connector_status NOT NULL DEFAULT 'disconnected',
    status_message TEXT,
    last_tested_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- One active connector per type (MVP constraint)
CREATE UNIQUE INDEX idx_data_sources_type ON data_sources (type) WHERE status = 'connected';

-- Investigation Metadata
CREATE TABLE investigations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    query TEXT NOT NULL,
    status investigation_status NOT NULL DEFAULT 'active',
    created_at TIMESTAMP DEFAULT NOW(),
    completed_at TIMESTAMP
);

-- Investigation Trace (Decision Log)
CREATE TABLE traces (
    id SERIAL PRIMARY KEY,
    investigation_id UUID REFERENCES investigations(id),
    step_type trace_step NOT NULL,
    tool_name TEXT,
    content TEXT NOT NULL,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Ingested Logs
CREATE TABLE logs (
    id SERIAL PRIMARY KEY,
    timestamp TIMESTAMP NOT NULL,
    level TEXT NOT NULL,
    service TEXT,
    trace_id TEXT,
    message TEXT NOT NULL,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_logs_timestamp ON logs (timestamp);
CREATE INDEX idx_logs_level ON logs (level);
CREATE INDEX idx_logs_service ON logs (service);
CREATE INDEX idx_logs_trace_id ON logs (trace_id);
CREATE INDEX idx_logs_message_fts ON logs USING gin(to_tsvector('english', message));

-- Code Embeddings
CREATE TABLE code_embeddings (
    id SERIAL PRIMARY KEY,
    file_path TEXT NOT NULL,
    chunk_index INTEGER NOT NULL DEFAULT 0,
    content TEXT NOT NULL,
    embedding vector(768)
);

CREATE INDEX idx_code_embeddings_vector ON code_embeddings USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- App Config (key-value store)
CREATE TABLE app_config (
    key TEXT PRIMARY KEY,
    value JSONB NOT NULL,
    updated_at TIMESTAMP DEFAULT NOW()
);
