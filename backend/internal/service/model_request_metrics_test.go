package service

import (
	"context"
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
	require.Equal(t, []ModelRequestMetric{{GroupID: 1, Model: "shared", Succeeded: 5}}, first.Items)
	second, err := svc.GetModelRequestMetrics(ctx, []Group{{ID: 2}})
	require.NoError(t, err)
	require.Equal(t, first.WindowEnd, second.WindowEnd)
	require.Equal(t, []ModelRequestMetric{{GroupID: 2, Model: "shared", Failed: 3}}, second.Items)
	empty, err := svc.GetModelRequestMetrics(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, empty.Items)
	require.Empty(t, empty.Items)
	require.Equal(t, 1, repo.queries)
}
