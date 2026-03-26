-- Add span tracking columns for distributed trace context propagation.
ALTER TABLE logs ADD COLUMN span_id TEXT;
ALTER TABLE logs ADD COLUMN parent_span_id TEXT;

CREATE INDEX idx_logs_span_id ON logs(span_id) WHERE span_id IS NOT NULL;
CREATE INDEX idx_logs_parent_span_id ON logs(parent_span_id) WHERE parent_span_id IS NOT NULL;
