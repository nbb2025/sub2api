package service

import (
	"context"
	"math"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"golang.org/x/sync/singleflight"
)

const modelMetricsWindow = 24 * time.Hour
const modelMetricsBucketSeconds = 3600
const modelMetricsBucketCount = int(modelMetricsWindow/time.Second) / modelMetricsBucketSeconds
const modelMetricsCacheTTL = time.Minute

type ModelRequestMetrics struct {
	WindowStart   time.Time                 `json:"window_start"`
	WindowEnd     time.Time                 `json:"window_end"`
	BucketSeconds int                       `json:"bucket_seconds"`
	Items         []ModelRequestDisplayItem `json:"items"`
	Groups        []ModelRequestGroupStatus `json:"groups"`
}

type ModelRequestDisplayItem struct {
	GroupID                     int64    `json:"group_id"`
	Model                       string   `json:"model"`
	AvgLatencyMs                *float64 `json:"avg_latency_ms"`
	StreamOutputTokensPerSecond *float64 `json:"stream_output_tokens_per_second"`
	ModelRequestStatus
}

type ModelRequestGroupStatus struct {
	GroupID int64 `json:"group_id"`
	ModelRequestStatus
}

type ModelRequestStatus struct {
	SuccessRate *float64                   `json:"success_rate"`
	Buckets     []ModelRequestStatusBucket `json:"buckets"`
}

type ModelRequestStatusBucket struct {
	Index int    `json:"index"`
	State string `json:"state"`
}

// ModelRequestMetric and its buckets are internal aggregates from the repository.
type ModelRequestMetric struct {
	GroupID                     int64
	Model                       string
	Succeeded                   int64 `json:"-"`
	Failed                      int64 `json:"-"`
	AvgLatencyMs                *float64
	TokensPerSecond             *float64
	StreamOutputTokensPerSecond *float64
	StreamOutputSamples         int64 `json:"-"`
	Buckets                     []ModelRequestMetricBucket
}

type ModelRequestMetricBucket struct {
	Index     int
	Succeeded int64 `json:"-"`
	Failed    int64 `json:"-"`
}

func modelRequestStatus(succeeded, failed int64, buckets []ModelRequestMetricBucket) ModelRequestStatus {
	status := ModelRequestStatus{Buckets: make([]ModelRequestStatusBucket, 0, len(buckets))}
	if total := succeeded + failed; total > 0 {
		rate := math.Round(float64(succeeded)/float64(total)*1000) / 1000
		if succeeded > 0 && failed > 0 {
			// Reserve 0% and 100% for exclusively failed or successful requests.
			rate = math.Min(math.Max(rate, 0.001), 0.999)
		}
		status.SuccessRate = &rate
	}
	for _, bucket := range buckets {
		var state string
		switch {
		case bucket.Succeeded > 0 && bucket.Failed > 0:
			state = "mixed"
		case bucket.Succeeded > 0:
			state = "success"
		case bucket.Failed > 0:
			state = "failure"
		default:
			continue
		}
		status.Buckets = append(status.Buckets, ModelRequestStatusBucket{Index: bucket.Index, State: state})
	}
	return status
}

func roundModelRequestMetric(value *float64, scale float64) *float64 {
	if value == nil {
		return nil
	}
	rounded := math.Round(*value*scale) / scale
	return &rounded
}

func newModelRequestMetrics(start, end time.Time, items []ModelRequestMetric) *ModelRequestMetrics {
	metrics := &ModelRequestMetrics{
		WindowStart: start, WindowEnd: end, BucketSeconds: modelMetricsBucketSeconds,
		Items:  make([]ModelRequestDisplayItem, 0, len(items)),
		Groups: make([]ModelRequestGroupStatus, 0),
	}
	type groupOutcomes struct {
		succeeded, failed int64
		buckets           [modelMetricsBucketCount]ModelRequestMetricBucket
	}
	byGroup := make(map[int64]*groupOutcomes)
	var groupIDs []int64
	for _, item := range items {
		latencyScale := 1.0
		if item.AvgLatencyMs != nil && *item.AvgLatencyMs >= 1000 {
			latencyScale = 0.1
		}
		metrics.Items = append(metrics.Items, ModelRequestDisplayItem{
			GroupID: item.GroupID, Model: item.Model,
			AvgLatencyMs:                roundModelRequestMetric(item.AvgLatencyMs, latencyScale),
			StreamOutputTokensPerSecond: roundModelRequestMetric(item.StreamOutputTokensPerSecond, 10),
			ModelRequestStatus:          modelRequestStatus(item.Succeeded, item.Failed, item.Buckets),
		})
		group := byGroup[item.GroupID]
		if group == nil {
			group = &groupOutcomes{}
			byGroup[item.GroupID] = group
			groupIDs = append(groupIDs, item.GroupID)
		}
		group.succeeded += item.Succeeded
		group.failed += item.Failed
		for _, bucket := range item.Buckets {
			if bucket.Index < 0 || bucket.Index >= len(group.buckets) {
				continue
			}
			aggregate := &group.buckets[bucket.Index]
			aggregate.Index = bucket.Index
			aggregate.Succeeded += bucket.Succeeded
			aggregate.Failed += bucket.Failed
		}
	}
	for _, id := range groupIDs {
		group := byGroup[id]
		metrics.Groups = append(metrics.Groups, ModelRequestGroupStatus{
			GroupID: id, ModelRequestStatus: modelRequestStatus(group.succeeded, group.failed, group.buckets[:]),
		})
	}
	return metrics
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

// Filter a shared display snapshot using the same group permissions as API key creation.
func (s *OpsService) GetModelRequestMetrics(ctx context.Context, availableGroups []Group) (*ModelRequestMetrics, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	end := time.Now().UTC().Truncate(time.Second)
	if len(availableGroups) == 0 {
		return newModelRequestMetrics(end.Add(-modelMetricsWindow), end, nil), nil
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
			current := newModelRequestMetrics(start, end, items)
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
	filtered.Items = make([]ModelRequestDisplayItem, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		if _, ok := allowed[item.GroupID]; ok {
			filtered.Items = append(filtered.Items, item)
		}
	}
	filtered.Groups = make([]ModelRequestGroupStatus, 0, len(allowed))
	for _, group := range snapshot.Groups {
		if _, ok := allowed[group.GroupID]; ok {
			filtered.Groups = append(filtered.Groups, group)
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
