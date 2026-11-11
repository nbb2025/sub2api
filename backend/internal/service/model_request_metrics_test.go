package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type modelMetricsTestRepo struct {
	OpsRepository
	queries int
}

func (r *modelMetricsTestRepo) GetModelRequestMetrics(_ context.Context, start, end time.Time, seconds int) ([]ModelRequestMetric, error) {
	r.queries++
	return []ModelRequestMetric{{GroupID: 1, Model: "shared", Succeeded: 5}, {GroupID: 2, Model: "shared", Failed: 3}}, nil
}

func (r *modelMetricsTestRepo) SaveModelVideoOutcome(context.Context, ModelVideoOutcome) error {
	return nil
}

func TestModelRequestMetricsScopesSharedCacheByGroup(t *testing.T) {
	repo := &modelMetricsTestRepo{}
	svc := &OpsService{opsRepo: repo}
	ctx := context.Background()
	first, err := svc.GetModelRequestMetrics(ctx, []Group{{ID: 1}})
	require.NoError(t, err)
	require.Equal(t, 24*time.Hour, first.WindowEnd.Sub(first.WindowStart))
	require.Equal(t, 3600, first.BucketSeconds)
	require.Len(t, first.Items, 1)
	require.EqualValues(t, 1, first.Items[0].GroupID)
	require.Equal(t, "shared", first.Items[0].Model)
	require.Len(t, first.Groups, 1)
	require.EqualValues(t, 1, first.Groups[0].GroupID)
	require.Equal(t, 1.0, *first.Groups[0].SuccessRate)
	second, err := svc.GetModelRequestMetrics(ctx, []Group{{ID: 2}})
	require.NoError(t, err)
	require.Equal(t, first.WindowEnd, second.WindowEnd)
	require.Len(t, second.Items, 1)
	require.EqualValues(t, 2, second.Items[0].GroupID)
	require.Len(t, second.Groups, 1)
	require.EqualValues(t, 2, second.Groups[0].GroupID)
	require.Equal(t, 0.0, *second.Groups[0].SuccessRate)
	empty, err := svc.GetModelRequestMetrics(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, empty.Items)
	require.Empty(t, empty.Items)
	require.NotNil(t, empty.Groups)
	require.Empty(t, empty.Groups)
	require.Equal(t, 1, repo.queries)
}

func TestModelRequestMetricsResponseExposesOnlyDisplayAggregates(t *testing.T) {
	start := time.Date(2026, time.November, 10, 0, 0, 0, 0, time.UTC)
	latency, overallRate, streamRate := 2356.8, 22.1234, 48.642
	metrics := newModelRequestMetrics(start, start.Add(modelMetricsWindow), []ModelRequestMetric{
		{
			GroupID: 1, Model: "first", Succeeded: 9, Failed: 1, AvgLatencyMs: &latency,
			TokensPerSecond: &overallRate, StreamOutputTokensPerSecond: &streamRate, StreamOutputSamples: 7,
			Buckets: []ModelRequestMetricBucket{{Index: 0, Succeeded: 9}, {Index: 1, Failed: 1}},
		},
		{GroupID: 1, Model: "second", Failed: 3, Buckets: []ModelRequestMetricBucket{{Index: 0, Failed: 3}}},
	})
	body, err := json.Marshal(metrics)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"window_start":"2026-11-10T00:00:00Z", "window_end":"2026-11-11T00:00:00Z", "bucket_seconds":3600,
		"items":[
			{"group_id":1,"model":"first","avg_latency_ms":2360,"stream_output_tokens_per_second":48.6,
			 "success_rate":0.9,"buckets":[{"index":0,"state":"success"},{"index":1,"state":"failure"}]},
			{"group_id":1,"model":"second","avg_latency_ms":null,"stream_output_tokens_per_second":null,
			 "success_rate":0,"buckets":[{"index":0,"state":"failure"}]}
		],
		"groups":[{"group_id":1,"success_rate":0.692,"buckets":[{"index":0,"state":"mixed"},{"index":1,"state":"failure"}]}]
	}`, string(body))
}
