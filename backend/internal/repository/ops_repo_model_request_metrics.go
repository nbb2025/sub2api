package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// The window is half-open. The 24 buckets are anchored at the beginning of
// this exact rolling window rather than midnight or the server's time zone.
const modelRequestMetricsSQL = `
WITH failures AS MATERIALIZED (
  SELECT DISTINCT ON (group_id, api_key_id, COALESCE(NULLIF(request_id, ''), 'ops-error:' || id))
    group_id, api_key_id, request_id, client_request_id,
    LOWER(BTRIM(COALESCE(NULLIF(BTRIM(requested_model), ''), model, ''))) AS model,
    created_at
  FROM ops_error_logs
  WHERE created_at >= $1 AND created_at < $2 AND group_id > 0
    AND status_code >= 400 AND NOT is_business_limited AND NOT COALESCE(is_count_tokens, FALSE)
    AND COALESCE(NULLIF(error_owner, ''), CASE WHEN error_phase IN ('auth', 'request') THEN 'client' ELSE 'platform' END) <> 'client'
    AND COALESCE(inbound_endpoint, '') NOT IN ('/v1beta/cachedContents', '/v1/responses/input_tokens')
    AND COALESCE(request_path, '') !~ '/(contents/generations/tasks|api/v1/tasks|images/tasks)/[^/]+'
    AND (COALESCE(request_path, '') !~ '/videos/[^/]+'
         OR request_path ~ '/videos/(generations|edits|extensions)/?$')
  ORDER BY group_id, api_key_id, COALESCE(NULLIF(request_id, ''), 'ops-error:' || id), id DESC
), failure_usage_keys AS (
  SELECT group_id, api_key_id, model, 'local:' || request_id AS request_id FROM failures WHERE model <> '' AND request_id <> ''
  UNION ALL
  SELECT group_id, api_key_id, model, 'client:' || client_request_id FROM failures WHERE model <> '' AND client_request_id <> ''
), outcomes AS (
  SELECT u.group_id, LOWER(BTRIM(COALESCE(NULLIF(BTRIM(u.requested_model), ''), u.model))) AS model,
    u.created_at AS completed_at, TRUE AS succeeded, NULLIF(u.duration_ms, 0)::bigint AS latency_ms,
    CASE WHEN u.output_tokens > 0 AND u.duration_ms > 0
      AND u.image_count = 0 AND u.image_output_tokens = 0 AND COALESCE(u.billing_mode, '') <> 'image'
      AND COALESCE(u.video_count, 0) = 0 AND u.video_usage IS NULL
      AND u.request_id NOT LIKE 'grok-video:%'
      AND COALESCE(u.inbound_endpoint, '') NOT IN ('/v1/images/generations', '/v1/images/edits')
      THEN u.output_tokens END AS output_tokens
  FROM usage_logs u
  WHERE u.created_at >= $1 AND u.created_at < $2 AND u.group_id > 0
    AND COALESCE(u.inbound_endpoint, '') NOT IN ('/v1beta/cachedContents', '/v1/responses/input_tokens')
    AND NOT EXISTS (
      SELECT 1 FROM failure_usage_keys f
      WHERE f.group_id = u.group_id AND f.api_key_id = u.api_key_id AND f.request_id = u.request_id
        AND f.model = LOWER(BTRIM(COALESCE(NULLIF(BTRIM(u.requested_model), ''), u.model)))
    )
    AND NOT EXISTS (
      SELECT 1 FROM model_video_outcomes v
      WHERE u.request_id LIKE 'grok-video:%' AND v.api_key_id = u.api_key_id AND v.request_id = u.request_id
    )

  UNION ALL
  SELECT group_id, model, created_at, FALSE, NULL, NULL FROM failures

  UNION ALL
  SELECT group_id, LOWER(BTRIM(model)), completed_at, succeeded, latency_ms, NULL
  FROM model_video_outcomes WHERE completed_at >= $1 AND completed_at < $2

  UNION ALL
  SELECT (payload->>'group_id')::bigint,
    LOWER(BTRIM(COALESCE(NULLIF(BTRIM(payload#>>'{pending,original_model}'), ''), payload#>>'{pending,model}', ''))),
    updated_at, FALSE, NULL, NULL
  FROM bailian_video_tasks
  WHERE state = 'failed' AND updated_at >= $1 AND updated_at < $2
    AND jsonb_typeof(payload->'group_id') = 'number'
), hourly AS (
  SELECT group_id, model, FLOOR(EXTRACT(EPOCH FROM completed_at - $1::timestamptz) / $3::int)::int AS bucket,
    COUNT(*) FILTER (WHERE succeeded) AS succeeded,
    COUNT(*) FILTER (WHERE NOT succeeded) AS failed,
    COALESCE(SUM(latency_ms) FILTER (WHERE succeeded AND latency_ms > 0), 0)::double precision AS latency_total,
    COUNT(*) FILTER (WHERE succeeded AND latency_ms > 0) AS latency_samples,
    COALESCE(SUM(output_tokens), 0)::double precision AS tokens,
    COALESCE(SUM(latency_ms) FILTER (WHERE output_tokens > 0), 0)::double precision AS token_latency
  FROM outcomes WHERE model <> '' AND group_id > 0
  GROUP BY group_id, model, bucket
)
SELECT group_id, model, bucket, succeeded, failed, latency_total, latency_samples, tokens, token_latency
FROM hourly ORDER BY group_id, model, bucket
`

func (r *opsRepository) GetModelRequestMetrics(ctx context.Context, start, end time.Time, bucketSeconds int) ([]service.ModelRequestMetric, error) {
	rows, err := r.db.QueryContext(ctx, modelRequestMetricsSQL, start, end, bucketSeconds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.ModelRequestMetric, 0)
	var current *service.ModelRequestMetric
	var latencyTotal, tokenCount, tokenLatency float64
	var latencySamples int64
	finish := func() {
		if current == nil {
			return
		}
		if latencySamples > 0 {
			avg := latencyTotal / float64(latencySamples)
			current.AvgLatencyMs = &avg
		}
		if tokenLatency > 0 {
			rate := tokenCount * 1000 / tokenLatency
			current.TokensPerSecond = &rate
		}
		items = append(items, *current)
	}
	for rows.Next() {
		var groupID, samples int64
		var model string
		var bucket service.ModelRequestMetricBucket
		var latency, tokens, duration float64
		if err := rows.Scan(&groupID, &model, &bucket.Index, &bucket.Succeeded, &bucket.Failed, &latency, &samples, &tokens, &duration); err != nil {
			return nil, err
		}
		if current == nil || current.GroupID != groupID || current.Model != model {
			finish()
			current = &service.ModelRequestMetric{GroupID: groupID, Model: model}
			latencyTotal, latencySamples, tokenCount, tokenLatency = 0, 0, 0, 0
		}
		current.Succeeded += bucket.Succeeded
		current.Failed += bucket.Failed
		current.Buckets = append(current.Buckets, bucket)
		latencyTotal += latency
		latencySamples += samples
		tokenCount += tokens
		tokenLatency += duration
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	finish()
	return items, nil
}

func (r *opsRepository) SaveModelVideoOutcome(ctx context.Context, outcome service.ModelVideoOutcome) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO model_video_outcomes
		(api_key_id, request_id, group_id, model, succeeded, completed_at, latency_ms)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (api_key_id, request_id) DO NOTHING`, outcome.APIKeyID, outcome.RequestID,
		outcome.GroupID, outcome.Model, outcome.Succeeded, outcome.CompletedAt, outcome.LatencyMs)
	return err
}
