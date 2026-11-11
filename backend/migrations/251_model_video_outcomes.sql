-- Observed terminal results for async videos whose upstream sends them as HTTP 200.
-- Deduplicate status polls with the same request key used by usage_logs.
CREATE TABLE IF NOT EXISTS model_video_outcomes (
    id BIGSERIAL PRIMARY KEY,
    api_key_id BIGINT NOT NULL,
    request_id TEXT NOT NULL,
    group_id BIGINT NOT NULL,
    model TEXT NOT NULL,
    succeeded BOOLEAN NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL,
    latency_ms BIGINT,
    UNIQUE (api_key_id, request_id)
);
CREATE INDEX IF NOT EXISTS idx_model_video_outcomes_completed_at
    ON model_video_outcomes (completed_at);
