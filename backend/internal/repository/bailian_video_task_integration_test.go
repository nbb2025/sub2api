//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBailianDurableTasksLeaseFencingAndPrecision(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	store := NewUsageBillingRepository(client, integrationDB).(service.BailianVideoTaskStore)
	task := &service.BailianVideoTask{TaskID: "bailian:" + uuid.NewString(), UserID: 11, APIKeyID: 22, GroupID: 33, AccountID: 44,
		Pending: service.GrokVideoPendingBilling{Platform: service.PlatformBailian, Model: "wan3.0-video", CreatedAt: time.Now().Format(time.RFC3339Nano),
			BailianPricing: &service.BailianVideoPricingSnapshot{Prices: map[string]float64{"1080p": .01234567}, BillingModels: map[string]string{"1080p": "wan3.0-video"}, RateMultiplier: 1.25}}}
	require.NoError(t, store.SaveBailianVideoTask(ctx, task))
	require.NoError(t, store.SaveBailianVideoTask(ctx, task))
	_, err := integrationDB.ExecContext(ctx, "UPDATE bailian_video_tasks SET next_poll_at=NOW()-INTERVAL '1 second' WHERE task_key=$1", task.StorageKey())
	require.NoError(t, err)
	find := func(batch []*service.BailianVideoTask) *service.BailianVideoTask {
		for _, candidate := range batch {
			if candidate.StorageKey() == task.StorageKey() {
				return candidate
			}
		}
		return nil
	}
	batch, err := store.LeaseBailianVideoTasks(ctx, 64)
	require.NoError(t, err)
	first := find(batch)
	require.NotNil(t, first)
	batch, err = store.LeaseBailianVideoTasks(ctx, 64)
	require.NoError(t, err)
	require.Nil(t, find(batch), "another replica cannot lease work still owned by the first")
	_, err = integrationDB.ExecContext(ctx, "UPDATE bailian_video_tasks SET lease_until=NOW()-INTERVAL '1 second' WHERE task_key=$1", task.StorageKey())
	require.NoError(t, err)
	batch, err = store.LeaseBailianVideoTasks(ctx, 64)
	require.NoError(t, err)
	recovered := find(batch)
	require.NotNil(t, recovered, "process restart can reclaim an expired lease")
	require.NotEqual(t, first.LeaseToken, recovered.LeaseToken)
	require.Error(t, store.UpdateBailianVideoTask(ctx, first, "done", time.Now()), "a stale worker cannot overwrite its successor")
	recovered.Usage = &domain.VideoUsage{Provider: service.PlatformBailian, InputVideoDuration: json.Number("1.000000000000000001"), OutputVideoDuration: json.Number("2"), Duration: json.Number("3.000000000000000001"), Resolution: "1080p"}
	observer := store.(interface {
		ObserveBailianVideoUsage(context.Context, string, *domain.VideoUsage) error
	})
	require.NoError(t, observer.ObserveBailianVideoUsage(ctx, recovered.StorageKey(), recovered.Usage))
	stalePending := *recovered
	stalePending.Usage = nil
	require.NoError(t, store.UpdateBailianVideoTask(ctx, &stalePending, "pending", time.Now()), "a concurrent pending response can finish its lease")
	protected, err := store.GetBailianVideoTask(ctx, task.StorageKey())
	require.NoError(t, err)
	require.Equal(t, recovered.Usage, protected.Usage, "a pending response must not erase already observed successful usage")
	require.NoError(t, store.UpdateBailianVideoTask(ctx, recovered, "done", time.Now()))
	loaded, err := store.GetBailianVideoTask(ctx, task.StorageKey())
	require.NoError(t, err)
	require.Equal(t, "1.000000000000000001", loaded.Usage.InputVideoDuration.String())
	require.Equal(t, .01234567, loaded.Pending.BailianPricing.Prices["1080p"])
	batch, err = store.LeaseBailianVideoTasks(ctx, 64)
	require.NoError(t, err)
	require.Nil(t, find(batch), "terminal tasks stop background polling")
}
