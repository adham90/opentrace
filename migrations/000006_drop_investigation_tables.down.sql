-- Re-create investigation types and tables
CREATE TYPE investigation_status AS ENUM ('active', 'completed', 'error');
CREATE TYPE trace_step AS ENUM ('thinking', 'tool_call', 'observation', 'final', 'error');

CREATE TABLE investigations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    query TEXT NOT NULL,
    status investigation_status NOT NULL DEFAULT 'active',
    created_at TIMESTAMP DEFAULT NOW(),
    completed_at TIMESTAMP
);

CREATE TABLE traces (
    id SERIAL PRIMARY KEY,
    investigation_id UUID REFERENCES investigations(id),
    step_type trace_step NOT NULL,
    tool_name TEXT,
    content TEXT NOT NULL,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE chats (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title      TEXT,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE messages (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id    UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    role       TEXT NOT NULL,
    content    TEXT NOT NULL,
    tool_name  TEXT,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_messages_chat_id ON messages (chat_id, created_at);
