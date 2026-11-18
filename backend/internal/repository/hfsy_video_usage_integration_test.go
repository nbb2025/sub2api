//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestHFsyVideoBillingPrecisionAndDedup(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: "hfsy-" + uuid.NewString() + "@example.com", Balance: 100, PasswordHash: "hash"})
	group := mustCreateGroup(t, client, &service.Group{
		Name: "hfsy-" + uuid.NewString(), Platform: service.PlatformHFsyAPI, RateMultiplier: 1.25,
		VideoModelPrices: map[string]map[string]float64{"sd-2.5-1080": {"1080p": .025}, "minimax-h3": {"720p": .04}},
	})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-hfsy-" + uuid.NewString(), Name: "hfsy", Quota: 100, GroupID: &group.ID})
	account := mustCreateAccount(t, client, &service.Account{Name: "hfsy-" + uuid.NewString(), Platform: service.PlatformHFsyAPI, Type: service.AccountTypeAPIKey})
	taskKeys := []string{}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		for _, id := range taskKeys {
			_, err := integrationDB.ExecContext(cleanupCtx, "DELETE FROM bailian_video_tasks WHERE task_key=$1", id)
			require.NoError(t, err)
		}
		for _, row := range []struct {
			query string
			id    int64
		}{
			{"DELETE FROM usage_logs WHERE api_key_id=$1", key.ID},
			{"DELETE FROM usage_billing_dedup WHERE api_key_id=$1", key.ID},
			{"DELETE FROM api_keys WHERE id=$1", key.ID},
			{"DELETE FROM users WHERE id=$1", user.ID},
			{"DELETE FROM accounts WHERE id=$1", account.ID},
			{"DELETE FROM groups WHERE id=$1", group.ID},
		} {
			_, err := integrationDB.ExecContext(cleanupCtx, row.query, row.id)
			require.NoError(t, err)
		}
	})

	key.User, key.Group = user, group
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	userRepo := NewUserRepository(client, integrationDB)
	keySvc := service.NewAPIKeyService(NewAPIKeyRepository(client, integrationDB), userRepo, NewGroupRepository(client, integrationDB), nil, nil, nil, cfg)
	billingRepo := NewUsageBillingRepository(client, integrationDB)
	lanes := service.NewUsageBillingLaneCombiner(billingRepo)
	t.Cleanup(lanes.Stop)
	logs := newUsageLogRepositoryWithSQL(client, integrationDB)
	svc := service.NewOpenAIGatewayService(nil, logs, service.ProvideUsageBillingRepository(lanes), userRepo, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, &service.BillingCacheService{}, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)

	for _, model := range []struct {
		name, resolution string
		seconds          int
		want             float64
	}{
		{"sd-2.5-1080", "1080p", 30, .9375},
		{"minimax-h3", "720p", 5, .05},
	} {
		input := &service.HFsyVideoRequest{Model: model.name, Prompt: "@image1 moving leaves", Orientation: "landscape", Duration: model.seconds, Images: []string{"https://cdn.example/leaf.png"}}
		snapshot, estimate, err := svc.SnapshotHFsyVideoPricing(ctx, key, account, input)
		require.NoError(t, err)
		require.Equal(t, model.want, estimate)
		task := &service.BailianVideoTask{
			TaskID: service.HFsyVideoTaskKey(uuid.NewString()), UserID: user.ID, APIKeyID: key.ID, GroupID: group.ID, AccountID: account.ID,
			Pending: service.GrokVideoPendingBilling{Platform: service.PlatformHFsyAPI, Model: model.name, UpstreamModel: model.name,
				VideoResolution: model.resolution, VideoDurationSeconds: model.seconds, CreatedAt: service.GrokVideoPendingCreatedAtNow(), HFsyPricing: snapshot},
		}
		taskKeys = append(taskKeys, task.StorageKey())
		require.NoError(t, svc.StoreBailianVideoTask(ctx, task))
		usage, err := task.Pending.HFsyCompletedUsage()
		require.NoError(t, err)
		require.NoError(t, svc.PersistBailianVideoObservedUsage(ctx, task.TaskID, user.ID, key.ID, usage))
		loaded, err := billingRepo.(service.BailianVideoTaskStore).GetBailianVideoTask(ctx, task.StorageKey())
		require.NoError(t, err)
		require.Equal(t, usage, loaded.Usage)
		require.Equal(t, snapshot, loaded.Pending.HFsyPricing)
		id := service.StableGrokVideoBillingRequestID(task.TaskID)
		result := &service.OpenAIForwardResult{RequestID: id, ResponseID: task.TaskID, Model: model.name, BillingModel: model.name, UpstreamModel: model.name,
			VideoUsage: loaded.Usage, VideoCount: 1, VideoResolution: model.resolution, HFsyVideoPricing: loaded.Pending.HFsyPricing}
		// Change the live rate after acceptance and settle the same task twice.
		originalRate := group.RateMultiplier
		group.RateMultiplier = 999
		for range 2 {
			require.NoError(t, svc.RecordUsage(ctx, &service.OpenAIRecordUsageInput{Result: result,
				APIKey: key, User: user, Account: account, QuotaPlatform: service.PlatformHFsyAPI, APIKeyService: keySvc}))
		}
		group.RateMultiplier = originalRate
		var logCount int
		var cost float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*),sum(actual_cost) FROM usage_logs WHERE request_id=$1 AND api_key_id=$2", id, key.ID).Scan(&logCount, &cost))
		require.Equal(t, 1, logCount)
		require.Equal(t, model.want, cost)
	}

	var balance, quota string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id=$1", user.ID).Scan(&balance))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used::text FROM api_keys WHERE id=$1", key.ID).Scan(&quota))
	require.Regexp(t, `^99\.0125(0*)$`, balance)
	require.Regexp(t, `^0\.9875(0*)$`, quota)
}
