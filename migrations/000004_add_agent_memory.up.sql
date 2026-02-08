-- System memory (cross-conversation knowledge base)
CREATE TABLE agent_memory (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category   TEXT NOT NULL,
    content    TEXT NOT NULL,
    source     TEXT,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_agent_memory_category ON agent_memory (category);
CREATE INDEX idx_agent_memory_content_fts ON agent_memory USING gin(to_tsvector('english', content));
