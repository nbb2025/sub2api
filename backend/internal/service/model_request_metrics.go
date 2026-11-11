package service

import (
	"context"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"golang.org/x/sync/singleflight"
)

const modelMetricsWindow = 24 * time.Hour
const modelMetricsBucketSeconds = 3600
const modelMetricsCacheTTL = time.Minute

type ModelRequestMetrics struct {
	WindowStart   time.Time            `json:"window_start"`
	WindowEnd     time.Time            `json:"window_end"`
	BucketSeconds int                  `json:"bucket_seconds"`
	Items         []ModelRequestMetric `json:"items"`
}

type ModelRequestMetric struct {
	GroupID                     int64                      `json:"group_id"`
	Model                       string                     `json:"model"`
	Succeeded                   int64                      `json:"succeeded"`
	Failed                      int64                      `json:"failed"`
	AvgLatencyMs                *float64                   `json:"avg_latency_ms"`
	TokensPerSecond             *float64                   `json:"tokens_per_second"` // End-to-end rate kept for existing consumers.
	StreamOutputTokensPerSecond *float64                   `json:"stream_output_tokens_per_second"`
	StreamOutputSamples         int64                      `json:"stream_output_samples"`
	Buckets                     []ModelRequestMetricBucket `json:"buckets"`
}

type ModelRequestMetricBucket struct {
	Index     int   `json:"index"`
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
}

type VideoTaskOutcome uint8

const (
	VideoTaskUnknown VideoTaskOutcome = iota
	VideoTaskSucceeded
	VideoTaskFailed
)

type ModelVideoOutcome struct {
	GroupID, APIKeyID int64
	RequestID, Model  string
	Succeeded         bool
	CompletedAt       time.Time
	LatencyMs         *int64
}

type modelRequestMetricsRepository interface {
	GetModelRequestMetrics(context.Context, time.Time, time.Time, int) ([]ModelRequestMetric, error)
	SaveModelVideoOutcome(context.Context, ModelVideoOutcome) error
}

type modelRequestMetricsCache struct {
	mu        sync.Mutex
	flight    singleflight.Group
	snapshot  *ModelRequestMetrics
	err       error
	expiresAt time.Time
}

func (c *modelRequestMetricsCache) read() (*ModelRequestMetrics, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot, c.err, time.Now().Before(c.expiresAt)
}

// Filter a shared aggregate using the same group permissions as API key creation.
// Caller identities, request IDs and upstream accounts never enter the response.
func (s *OpsService) GetModelRequestMetrics(ctx context.Context, availableGroups []Group) (*ModelRequestMetrics, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	end := time.Now().UTC().Truncate(time.Second)
	if len(availableGroups) == 0 {
		return &ModelRequestMetrics{WindowStart: end.Add(-modelMetricsWindow), WindowEnd: end, BucketSeconds: modelMetricsBucketSeconds, Items: []ModelRequestMetric{}}, nil
	}
	repo, ok := s.opsRepo.(modelRequestMetricsRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("MODEL_METRICS_UNAVAILABLE", "Model metrics repository not available")
	}
	cache := &s.modelRequestMetrics
	snapshot, err, cached := cache.read()
	if !cached {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-cache.flight.DoChan("24h", func() (any, error) {
			if current, err, found := cache.read(); found {
				return current, err
			}
			queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
			defer cancel()
			start := end.Add(-modelMetricsWindow)
			items, err := repo.GetModelRequestMetrics(queryCtx, start, end, modelMetricsBucketSeconds)
			current := &ModelRequestMetrics{WindowStart: start, WindowEnd: end, BucketSeconds: modelMetricsBucketSeconds, Items: items}
			ttl := modelMetricsCacheTTL
			if err != nil {
				ttl = 10 * time.Second
			}
			cache.mu.Lock()
			cache.snapshot, cache.err, cache.expiresAt = current, err, time.Now().Add(ttl)
			cache.mu.Unlock()
			return current, err
		}):
			err = result.Err
			snapshot, _ = result.Val.(*ModelRequestMetrics)
		}
	}
	if err != nil {
		return nil, err
	}

	allowed := make(map[int64]struct{}, len(availableGroups))
	for _, group := range availableGroups {
		allowed[group.ID] = struct{}{}
	}
	filtered := *snapshot
	filtered.Items = make([]ModelRequestMetric, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		if _, ok := allowed[item.GroupID]; ok {
			filtered.Items = append(filtered.Items, item)
		}
	}
	return &filtered, nil
}

func (s *OpsService) SaveModelVideoOutcome(ctx context.Context, outcome ModelVideoOutcome) error {
	if s == nil || !s.IsMonitoringEnabled(ctx) {
		return nil
	}
	if repo, ok := s.opsRepo.(modelRequestMetricsRepository); ok {
		return repo.SaveModelVideoOutcome(ctx, outcome)
	}
	return nil
}
