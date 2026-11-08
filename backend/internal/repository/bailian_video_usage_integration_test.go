//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// Exercise migrations, the real cost/RecordUsage path, the SQL monetary
// transaction, durable task dedup and JSONB/management readback together.
func TestBailianVideoBillingPrecisionAndDedup(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: "bailian-" + uuid.NewString() + "@example.com", Balance: 100, PasswordHash: "hash"})
	price := 0.01234567
	group := mustCreateGroup(t, client, &service.Group{Name: "bailian-" + uuid.NewString(), Platform: service.PlatformBailian, RateMultiplier: 1.25, VideoPrice1080P: &price})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-bailian-" + uuid.NewString(), Name: "wan", Quota: 100, GroupID: &group.ID})
	account := mustCreateAccount(t, client, &service.Account{Name: "bailian-" + uuid.NewString(), Platform: service.PlatformBailian, Type: service.AccountTypeAPIKey})
	key.Group = group
	key.User = user
	keyService := service.NewAPIKeyService(NewAPIKeyRepository(client, integrationDB), NewUserRepository(client, integrationDB), NewGroupRepository(client, integrationDB), nil, nil, nil, &config.Config{})
	logs := newUsageLogRepositoryWithSQL(client, integrationDB)
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	svc := service.NewOpenAIGatewayService(nil, logs, NewUsageBillingRepository(client, integrationDB), NewUserRepository(client, integrationDB), nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, &service.BillingCacheService{}, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	usage := &domain.VideoUsage{Provider: service.PlatformBailian, InputVideoDuration: json.Number("5.125000001"), OutputVideoDuration: json.Number("24.874999999"), Duration: json.Number("30"), Resolution: "1080p"}
	task := "grok-video:bailian:" + uuid.NewString()
	input := &service.OpenAIRecordUsageInput{Result: &service.OpenAIForwardResult{RequestID: task, ResponseID: task, Model: "wan3.0-video-prime", BillingModel: "wan3.0-video-prime", UpstreamModel: "wan3.0-video-prime", VideoCount: 1, VideoResolution: "1080p", VideoUsage: usage, Duration: time.Second}, APIKey: key, User: user, Account: account, QuotaPlatform: service.PlatformBailian, APIKeyService: keyService}
	snapshot, err := svc.SnapshotBailianVideoPricing(ctx, key, account, "wan3.0-video-prime")
	require.NoError(t, err)
	input.Result.BailianVideoPricing = snapshot
	want, err := service.CalculateBailianVideoCost(group, "wan3.0-video-prime", usage, 1.25)
	require.NoError(t, err)
	price, group.RateMultiplier = 99, 99 // A live config edit must not reprice the accepted job.
	for range 3 {
		require.NoError(t, svc.RecordUsage(ctx, input))
	}
	var balance, quota string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id=$1", user.ID).Scan(&balance))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used::text FROM api_keys WHERE id=$1", key.ID).Scan(&quota))
	charged := decimal.NewFromFloat(want.ActualCost)
	gotBalance, err := decimal.NewFromString(balance)
	require.NoError(t, err)
	gotQuota, err := decimal.NewFromString(quota)
	require.NoError(t, err)
	require.True(t, decimal.NewFromInt(100).Sub(gotBalance).Equal(charged), "balance=%s cost=%s", balance, charged)
	require.True(t, gotQuota.Equal(charged), "quota=%s cost=%s", quota, charged)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM usage_billing_dedup WHERE request_id=$1 AND api_key_id=$2", task, key.ID).Scan(&count))
	require.Equal(t, 1, count)
	var id int64
	require.Eventually(t, func() bool {
		return integrationDB.QueryRowContext(ctx, "SELECT id FROM usage_logs WHERE request_id=$1 AND api_key_id=$2", task, key.ID).Scan(&id) == nil
	}, time.Second, 10*time.Millisecond)
	loaded, err := logs.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, usage, loaded.VideoUsage)
	require.Nil(t, loaded.VideoDurationSeconds)
	require.Equal(t, want.ActualCost, loaded.ActualCost)
}
