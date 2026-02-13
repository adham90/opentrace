ALTER TABLE request_summaries ADD COLUMN time_breakdown TEXT;
ALTER TABLE request_summaries ADD COLUMN duplicate_queries INTEGER DEFAULT 0;
ALTER TABLE request_summaries ADD COLUMN worst_duplicate_count INTEGER DEFAULT 0;
ALTER TABLE request_summaries ADD COLUMN top_duplicates TEXT;
