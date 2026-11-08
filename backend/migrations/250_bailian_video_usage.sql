-- Decimal video seconds are independent of Grok's historical integer column.
-- JSONB numeric values preserve supplier precision without storing prompts,
-- media URLs, credentials or fabricated tokens. Existing rows stay NULL.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS video_usage JSONB;
COMMENT ON COLUMN usage_logs.video_usage IS
    'Native video usage: provider, decimal input_video_duration/output_video_duration/duration, resolution';

-- No prompts, media URLs or credentials are stored. The durable job contains
-- ownership, the accepted tariff and (once observed) authoritative usage only.
CREATE TABLE IF NOT EXISTS bailian_video_tasks (
    task_key TEXT PRIMARY KEY,
    payload JSONB NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending',
    next_poll_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    lease_token TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_bailian_video_tasks_due
    ON bailian_video_tasks (next_poll_at) WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS idx_bailian_video_tasks_cleanup
    ON bailian_video_tasks (updated_at) WHERE state <> 'pending';
