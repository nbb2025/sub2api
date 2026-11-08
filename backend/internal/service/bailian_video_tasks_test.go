//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type bailianWorkerStore struct {
	openAIRecordUsageBillingRepoStub
	task  *BailianVideoTask
	state string
	t     *testing.T
}

func (r *bailianWorkerStore) SaveBailianVideoTask(_ context.Context, task *BailianVideoTask) error {
	r.task = task
	return nil
}
func (r *bailianWorkerStore) GetBailianVideoTask(context.Context, string) (*BailianVideoTask, error) {
	return r.task, nil
}
func (r *bailianWorkerStore) LeaseBailianVideoTasks(context.Context, int) ([]*BailianVideoTask, error) {
	return []*BailianVideoTask{r.task}, nil
}
func (r *bailianWorkerStore) UpdateBailianVideoTask(_ context.Context, task *BailianVideoTask, state string, _ time.Time) error {
	raw, err := json.Marshal(task)
	if err != nil {
		return err
	}
	var copy BailianVideoTask
	if err := json.Unmarshal(raw, &copy); err != nil {
		return err
	}
	copy.LeaseToken = task.LeaseToken
	r.task, r.state = &copy, state
	return nil
}
func (r *bailianWorkerStore) Apply(ctx context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	require.NotNil(r.t, r.task.Usage, "authoritative usage must be saved before money is committed")
	return r.openAIRecordUsageBillingRepoStub.Apply(ctx, cmd)
}

type bailianWorkerUserRepo struct{ UserRepository }

func (bailianWorkerUserRepo) GetByID(_ context.Context, id int64) (*User, error) {
	return &User{ID: id, Balance: 100}, nil
}

type bailianWorkerKeyRepo struct{ APIKeyRepository }

func (bailianWorkerKeyRepo) GetByID(context.Context, int64) (*APIKey, error) {
	return nil, ErrAPIKeyNotFound
}

func TestBailianBackgroundSettlementPersistsUsageAndRetriesWithoutPollingAgain(t *testing.T) {
	ctx := context.Background()
	price := .01234567
	group := &Group{ID: 90, Platform: PlatformBailian, RateMultiplier: 1.25, VideoPrice1080P: &price}
	key := &APIKey{ID: 91, UserID: 92, User: &User{ID: 92}, GroupID: &group.ID, Group: group, Quota: 100}
	account := bailianTestAccount()
	account.Extra = map[string]any{"quota_limit": 100.0, "quota_daily_limit": 10.0}
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	store := &bailianWorkerStore{t: t, openAIRecordUsageBillingRepoStub: openAIRecordUsageBillingRepoStub{err: errors.New("temporary billing outage")}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, store, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.userRepo = bailianWorkerUserRepo{}
	svc.accountRepo = &openAIRecordUsageAccountRepoStub{account: account}
	svc.cache = &bailianBillingStateCache{payload: map[string][]byte{}, claims: map[string]bool{}}
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(bailianCompletedBody(`{"video_count":1,"duration":30,"input_video_duration":5.125000001,"output_video_duration":24.874999999,"SR":1080}`))}
	svc.httpUpstream = upstream
	snapshot, err := svc.SnapshotBailianVideoPricing(ctx, key, account, "wan-public")
	require.NoError(t, err)
	task := &BailianVideoTask{TaskID: "bailian:wan-task", UserID: 92, APIKeyID: 91, GroupID: 90, AccountID: account.ID,
		QuotaPlatform: PlatformBailian, LeaseToken: "lease-1", Pending: GrokVideoPendingBilling{Platform: PlatformBailian, Model: "wan-public", UpstreamModel: "wan3.0-video-prime", CreatedAt: time.Now().Add(-time.Minute).Format(time.RFC3339Nano), BailianPricing: snapshot}}
	require.NoError(t, store.SaveBailianVideoTask(ctx, task))
	keys := NewAPIKeyService(bailianWorkerKeyRepo{}, nil, nil, nil, nil, nil, svc.cfg)
	svc.settleBailianVideoTask(ctx, keys, task)
	require.Equal(t, "pending", store.state)
	require.Equal(t, "5.125000001", store.task.Usage.InputVideoDuration.String())
	require.Zero(t, logs.calls, "billing failure cannot create a permanent zero-cost row")
	require.Len(t, upstream.requests, 1)
	firstFingerprint := store.lastCmd.RequestFingerprint
	store.err = nil
	// Simulate a restart and a vendor task which can no longer be queried.
	store.task.Pending.CreatedAt = time.Now().Add(-25 * time.Hour).Format(time.RFC3339Nano)
	svc.settleBailianVideoTask(ctx, keys, store.task)
	require.Equal(t, "done", store.state)
	require.Len(t, upstream.requests, 1, "stored authoritative usage must survive vendor expiration")
	require.Equal(t, .46296263, store.lastCmd.BalanceCost)
	require.Equal(t, .46296263, store.lastCmd.APIKeyQuotaCost)
	require.Equal(t, .3703701, store.lastCmd.AccountQuotaCost)
	require.Equal(t, firstFingerprint, store.lastCmd.RequestFingerprint, "restart must keep the same monetary command")
	require.Equal(t, .46296263, logs.lastLog.ActualCost)
	require.True(t, svc.BailianVideoAlreadySettled(ctx, task.TaskID, task.UserID, task.APIKeyID))
	raw, err := json.Marshal(store.task)
	require.NoError(t, err)
	for _, secret := range []string{"bailian-secret", "video.mp4", "https://", "input.media", "input.prompt"} {
		require.NotContains(t, string(raw), secret)
	}
	// The lifecycle is reusable and joins its goroutine during shutdown.
	svc.StartBailianVideoSettlement(keys)
	svc.StopBailianVideoSettlement()
	svc.StopBailianVideoSettlement()
}

func TestBailianBackgroundLookupHonorsRetryAfterAndNeverCreates(t *testing.T) {
	require.Equal(t, 120*time.Second, bailianRetryAfter("120"))
	for _, raw := range []string{"NaN", "Inf", "-1", "invalid"} {
		require.Zero(t, bailianRetryAfter(raw))
	}
}
