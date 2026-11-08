//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

type bailianBillingStateCache struct {
	GatewayCache
	mu      sync.Mutex
	payload map[string][]byte
	claims  map[string]bool
	lastTTL time.Duration
}

func (c *bailianBillingStateCache) GetGrokVideoPendingBilling(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.payload[key]...), nil
}

func (c *bailianBillingStateCache) SetGrokVideoPendingBilling(_ context.Context, key string, value []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.payload[key] = append([]byte(nil), value...)
	return nil
}

func (c *bailianBillingStateCache) ClaimGrokVideoBilled(_ context.Context, key string, ttl time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastTTL = ttl
	if c.claims[key] {
		return false, nil
	}
	c.claims[key] = true
	return true, nil
}

func (c *bailianBillingStateCache) ReleaseGrokVideoBilled(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.claims, key)
	return nil
}

func TestBailianBillingLeaseDoesNotPretendToBeSettled(t *testing.T) {
	cache := &bailianBillingStateCache{payload: map[string][]byte{}, claims: map[string]bool{}}
	svc := &OpenAIGatewayService{cache: cache}
	ctx := context.Background()
	var accepted int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			claimed, settled, err := svc.ClaimBailianVideoBilling(ctx, "bailian:task", 1, 2)
			require.NoError(t, err)
			require.False(t, settled)
			if claimed {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	require.Equal(t, 1, accepted)
	require.Equal(t, 30*time.Second, cache.lastTTL, "a crashed settlement must not block lookups for 48 hours")
	require.NoError(t, svc.ReleaseGrokVideoBilling(ctx, "bailian:task", 1, 2))
	claimed, settled, err := svc.ClaimBailianVideoBilling(ctx, "bailian:task", 1, 2)
	require.NoError(t, err)
	require.True(t, claimed, "failed settlement can retry")
	require.False(t, settled)
	require.NoError(t, svc.MarkBailianVideoSettled(ctx, "bailian:task", 1, 2))
	claimed, settled, err = svc.ClaimBailianVideoBilling(ctx, "bailian:task", 1, 2)
	require.NoError(t, err)
	require.False(t, claimed)
	require.True(t, settled, "only the post-commit marker means paid")
}

func TestBailianSimpleModeUsesExplicitOptionalKeyBillingPolicy(t *testing.T) {
	key := &APIKey{Group: &Group{Platform: PlatformBailian}, RateLimit5h: 1}
	for _, tc := range []struct {
		name string
		cfg  *config.Config
		free bool
	}{
		{"standard requires a tariff", &config.Config{}, false},
		{"simple without monetary limits", &config.Config{RunMode: config.RunModeSimple}, true},
		{"simple with optional key billing", &config.Config{RunMode: config.RunModeSimple, SimpleModeKeyRateLimitEnabled: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := bailianVideoPricingGroup(key, tc.cfg)
			price, err := BailianVideoUnitPrice(group, "wan3.0-video", "1080p")
			if tc.free {
				require.NoError(t, err)
				require.Zero(t, *price)
			} else {
				require.Error(t, err)
			}
			require.Nil(t, key.Group.VideoPrice1080P, "simple mode must not mutate saved group prices")
		})
	}
}

func TestBailianPricingSnapshotSurvivesPriceAndSubscriptionChanges(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		name := "balance"
		if subscription {
			name = "subscription"
		}
		t.Run(name, func(t *testing.T) {
			price, accountRate := 0.01234567, 1.1
			group := &Group{ID: 70, Platform: PlatformBailian, RateMultiplier: 1.25, VideoPrice1080P: &price}
			key := &APIKey{ID: 71, UserID: 72, User: &User{ID: 72}, GroupID: &group.ID, Group: group}
			account := bailianTestAccount()
			account.RateMultiplier = &accountRate
			logs, billing := &openAIRecordUsageLogRepoStub{inserted: true}, &openAIRecordUsageBillingRepoStub{}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			snapshot, err := svc.SnapshotBailianVideoPricing(context.Background(), key, account, "wan-public")
			require.NoError(t, err)
			if subscription {
				id := int64(73)
				snapshot.SubscriptionID = &id
			}
			usage := &domain.VideoUsage{Provider: PlatformBailian, InputVideoDuration: json.Number("5.125000001"), OutputVideoDuration: json.Number("24.874999999"), Duration: json.Number("30"), Resolution: "1080p"}
			want, err := CalculateBailianVideoCost(group, "wan-public", usage, 1.25)
			require.NoError(t, err)
			// Both a later config edit and a replacement subscription are ignored.
			price, group.RateMultiplier, accountRate = 99, 99, 99
			input := &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: "grok-video:bailian:snapshot", ResponseID: "bailian:snapshot", Model: "wan-public", BillingModel: "wan-public", VideoCount: 1, VideoResolution: "1080p", VideoUsage: usage, BailianVideoPricing: snapshot},
				APIKey: key, User: key.User, Account: account, Subscription: &UserSubscription{ID: 999},
			}
			require.NoError(t, svc.RecordUsage(context.Background(), input))
			require.Equal(t, want.ActualCost, logs.lastLog.ActualCost)
			require.Equal(t, 1.25, logs.lastLog.RateMultiplier)
			require.Equal(t, 1.1, *logs.lastLog.AccountRateMultiplier)
			if subscription {
				require.Equal(t, int64(73), *billing.lastCmd.SubscriptionID)
				require.Equal(t, want.ActualCost, billing.lastCmd.SubscriptionCost)
				require.Zero(t, billing.lastCmd.BalanceCost)
			} else {
				require.Nil(t, billing.lastCmd.SubscriptionID)
				require.Equal(t, want.ActualCost, billing.lastCmd.BalanceCost)
			}
		})
	}
}

func TestBailianSettlementFailureCannotWinTheUniqueUsageLogKey(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	billing := &openAIRecordUsageBillingRepoStub{err: errors.New("temporary database failure")}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	price := 0.01234567
	group := &Group{ID: 80, Platform: PlatformBailian, RateMultiplier: 1.25, VideoPrice1080P: &price}
	usage := &domain.VideoUsage{Provider: PlatformBailian, InputVideoDuration: json.Number("5"), OutputVideoDuration: json.Number("25"), Duration: json.Number("30"), Resolution: "1080p"}
	input := &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "grok-video:bailian:retry", ResponseID: "bailian:retry", Model: "wan3.0-video", VideoCount: 1, VideoResolution: "1080p", VideoUsage: usage},
		APIKey: &APIKey{ID: 81, GroupID: &group.ID, Group: group}, User: &User{ID: 82}, Account: bailianTestAccount(),
	}
	require.Error(t, svc.RecordUsage(context.Background(), input))
	require.Zero(t, logs.calls, "a failed attempt must not persist a zero-cost row")
	billing.err = nil
	logs.err = errors.New("usage log storage unavailable")
	require.Error(t, svc.RecordUsage(context.Background(), input), "a committed charge with missing metadata remains repairable")
	firstFingerprint := billing.lastCmd.RequestFingerprint
	billing.result = &UsageBillingApplyResult{Applied: false}
	logs.err = nil
	require.NoError(t, svc.RecordUsage(context.Background(), input))
	require.Equal(t, firstFingerprint, billing.lastCmd.RequestFingerprint)
	require.Equal(t, 0.46296263, logs.lastLog.ActualCost)
	require.Equal(t, usage, logs.lastLog.VideoUsage)
}
