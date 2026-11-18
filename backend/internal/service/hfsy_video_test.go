//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHFsyVideoRequestMediaConstraints(t *testing.T) {
	for _, body := range []string{
		`{"model":"sd-2.5-1080","prompt":"start and end","duration":30,"start_image_url":"https://cdn.example/a.png","end_image_url":"https://cdn.example/b.png"}`,
		`{"model":"sd-2.5-flash-480","prompt":"leaf","duration":4,"orientation":"landscape","ratio":"1:1"}`,
		`{"model":"sd-2.5-1080","prompt":"@video1","duration":4,"orientation":"landscape","videos":["https://cdn.example/a.mp4"]}`,
		`{"model":"sd-2-fast-480","prompt":"street","duration":5,"orientation":"landscape","ratio":"21:9"}`,
		`{"model":"sd-2-vip","prompt":"参考 @图片1 和 @音频1","duration":5,"orientation":"portrait","images":["https://cdn.example/a.png"],"audios":["https://cdn.example/a.mp3"]}`,
		`{"model":"sd-2-vip-720","prompt":"@1","duration":5,"orientation":"portrait","images":["https://cdn.example/a.png"]}`,
		`{"model":"minimax-h3","prompt":"@image1 rabbit","duration":5,"orientation":"portrait","images":["https://cdn.example/a.png"]}`,
	} {
		req, err := ParseHFsyVideoRequest([]byte(body))
		require.NoError(t, err, body)
		require.NoError(t, req.ValidateUpstreamModel(req.Model), body)
	}
	for _, body := range []string{
		`{"model":"sd-2.5-1080","prompt":"x","duration":31,"orientation":"landscape"}`,
		`{"model":"sd-2.5-720","prompt":"x","duration":4,"end_image_url":"https://cdn.example/a.png"}`,
		`{"model":"sd-2.5-480","prompt":"x","duration":4,"orientation":"portrait","start_image_url":"https://cdn.example/a.png"}`,
		`{"model":"sd-2.5-480","prompt":"x","duration":4,"images":[],"start_image_url":"https://cdn.example/a.png"}`,
		`{"model":"sd-2-fast-480","prompt":"street","duration":4,"orientation":"landscape","ratio":"21:9"}`,
		`{"model":"sd-2-fast-720","prompt":"street","duration":4,"orientation":"landscape"}`,
		`{"model":"sd-2-vip-480","prompt":"street","duration":4,"orientation":"landscape"}`,
		`{"model":"sd-2-mini-480","prompt":"@video1","duration":5,"orientation":"landscape","videos":["https://cdn.example/a.mp4"]}`,
		`{"model":"sd-2.5-480","prompt":"wide","duration":5,"orientation":"landscape","ratio":"21:9"}`,
		`{"model":"sd-2.5-1080","prompt":"@image2","duration":4,"orientation":"landscape","images":["https://cdn.example/a.png"]}`,
		`{"model":"minimax-h3","prompt":"x","duration":4,"orientation":"landscape","images":["https://cdn.example/a.png"]}`,
		`{"model":"minimax-h3","prompt":"x","duration":5,"orientation":"landscape","images":["https://cdn.example/a.png"],"videos":["https://cdn.example/a.mp4"]}`,
		`{"model":"minimax-h3","prompt":"@图片2","duration":5,"orientation":"landscape","images":["https://cdn.example/a.png"]}`,
		`{"model":"video-alias","prompt":"x","duration":5,"orientation":"landscape","audios":["data:audio/wav;base64,AA=="]}`,
		`{"model":"video-alias","prompt":"x","duration":5,"orientation":"landscape","images":["http://169.254.169.254/example"]}`,
		`{"model":"video-alias","model":"minimax-h3","prompt":"x","duration":5,"orientation":"landscape"}`,
	} {
		req, err := ParseHFsyVideoRequest([]byte(body))
		if err == nil {
			err = req.ValidateUpstreamModel(req.Model)
		}
		require.Error(t, err, body)
	}
	require.Empty(t, HFsyVideoTaskKey("../task"))
	require.Equal(t, "hfsyapi:task_1", HFsyVideoTaskKey("task_1"))
}

func TestHFsyVideoPreservesNativeOptionsWhenMappingModel(t *testing.T) {
	raw := []byte(`{ "model":"my-h3","prompt":"rabbit","duration":5,"orientation":"landscape","images":["https://cdn.example/ref.png"],"size":"large","watermark":false,"supplier_option":{"enabled":true}}`)
	input, err := ParseHFsyVideoRequest(raw)
	require.NoError(t, err)
	require.NoError(t, input.ValidateUpstreamModel("minimax-h3"))
	same, err := input.upstreamBody("my-h3")
	require.NoError(t, err)
	require.Equal(t, raw, same)
	mapped, err := input.upstreamBody("minimax-h3")
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"minimax-h3","prompt":"rabbit","duration":5,"orientation":"landscape","images":["https://cdn.example/ref.png"],"size":"large","watermark":false,"supplier_option":{"enabled":true}}`, string(mapped))
	_, err = ParseHFsyVideoRequest([]byte(`{"model":"my-h3","api_key":"credential"}`))
	require.ErrorContains(t, err, "Authorization")
}

func TestHFsyVideoModelBounds(t *testing.T) {
	for _, family := range []struct {
		ids                           []string
		min, max, image, video, audio int
	}{
		{[]string{"sd-2.5-480", "sd-2.5-720", "sd-2.5-1080"}, 4, 30, 30, 10, 10},
		{[]string{"sd-2.5-flash-480", "sd-2.5-flash-720"}, 4, 30, 10, 5, 5},
		{[]string{"sd-2-fast-480", "sd-2-fast-720", "sd-2-mini-480", "sd-2-mini-720", "sd-2-vip", "sd-2-vip-480", "sd-2-vip-720"}, 5, 15, 9, 3, 3},
		{[]string{"minimax-h3"}, 5, 15, 9, 0, 3},
	} {
		for _, id := range family.ids {
			t.Run(id, func(t *testing.T) {
				req := HFsyVideoRequest{Model: id, Prompt: "@image1", Orientation: "landscape", Duration: family.min}
				req.Images = make([]string, family.image)
				req.Videos = make([]string, min(family.video, 5))
				req.Audios = make([]string, min(family.audio, 5))
				require.NoError(t, req.ValidateUpstreamModel(id))
				req.Duration = family.max
				require.NoError(t, req.ValidateUpstreamModel(id))
				req.Duration = family.min - 1
				require.ErrorContains(t, req.ValidateUpstreamModel(id), "duration")
				req.Duration = family.max + 1
				require.ErrorContains(t, req.ValidateUpstreamModel(id), "duration")
				req.Duration = family.min
				for _, target := range []struct {
					name string
					urls *[]string
					max  int
				}{{"images", &req.Images, family.image}, {"videos", &req.Videos, family.video}, {"audios", &req.Audios, family.audio}} {
					original := *target.urls
					*target.urls = make([]string, target.max+1)
					require.ErrorContains(t, req.ValidateUpstreamModel(id), target.name+" supports at most")
					if target.name != "images" && target.max > 5 {
						*target.urls = make([]string, 6)
						require.ErrorContains(t, req.ValidateUpstreamModel(id), "15 seconds")
					}
					*target.urls = original
				}
			})
		}
	}
}

func TestHFsyPreviouslyAcceptedFourSecondTasksCanSettle(t *testing.T) {
	for _, tc := range []struct{ model, resolution string }{
		{"sd-2-fast-480", "480p"}, {"sd-2-fast-720", "720p"}, {"sd-2-vip-480", "480p"},
	} {
		pending := GrokVideoPendingBilling{Platform: PlatformHFsyAPI, UpstreamModel: tc.model,
			VideoDurationSeconds: 4, VideoResolution: tc.resolution, HFsyPricing: &VideoTaskPricingSnapshot{}}
		usage, err := pending.HFsyCompletedUsage()
		require.NoError(t, err)
		require.Equal(t, "4", usage.Duration.String())
	}
}

func TestHFsyVideoWorkerRetriesFrozenTariff(t *testing.T) {
	ctx := context.Background()
	group := &Group{ID: 25, Platform: PlatformHFsyAPI, RateMultiplier: 2, VideoModelPrices: map[string]map[string]float64{
		"minimax-h3": {"720p": .125},
	}}
	key := &APIKey{ID: 26, UserID: 27, User: &User{ID: 27}, GroupID: &group.ID, Group: group, Quota: 100}
	account := &Account{ID: 28, Platform: PlatformHFsyAPI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "hfsy-fixture"}}
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	store := &bailianWorkerStore{t: t, openAIRecordUsageBillingRepoStub: openAIRecordUsageBillingRepoStub{err: errors.New("temporary billing outage")}}
	lanes := NewUsageBillingLaneCombiner(store)
	t.Cleanup(lanes.Stop)
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, ProvideUsageBillingRepository(lanes), &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.userRepo = bailianWorkerUserRepo{}
	svc.accountRepo = &openAIRecordUsageAccountRepoStub{account: account}
	svc.cache = &bailianBillingStateCache{payload: map[string][]byte{}, claims: map[string]bool{}}
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(
		`{"success":true,"data":{"id":"h3-task","status":"completed","result_url":"https://cdn.example/h3.mp4"}}`,
	)}
	svc.httpUpstream = upstream
	input := &HFsyVideoRequest{Model: "minimax-h3", Prompt: "@image1 rabbit", Orientation: "portrait", Duration: 5, Images: []string{"https://cdn.example/a.png"}}
	price, estimate, err := svc.SnapshotHFsyVideoPricing(ctx, key, account, input)
	require.NoError(t, err)
	require.Equal(t, .25, estimate, "H3 estimate is per request")
	task := &BailianVideoTask{
		TaskID: "hfsyapi:h3-task", UserID: key.UserID, APIKeyID: key.ID, GroupID: group.ID, AccountID: account.ID,
		QuotaPlatform: PlatformHFsyAPI, LeaseToken: "lease-1",
		Pending: GrokVideoPendingBilling{Platform: PlatformHFsyAPI, Model: input.Model, UpstreamModel: input.Model,
			VideoResolution: "720p", VideoDurationSeconds: 5, CreatedAt: GrokVideoPendingCreatedAtNow(), HFsyPricing: price},
	}
	require.NoError(t, svc.StoreBailianVideoTask(ctx, task))
	keys := NewAPIKeyService(bailianWorkerKeyRepo{}, nil, nil, nil, nil, nil, svc.cfg)
	svc.settleBailianVideoTask(ctx, keys, task)
	require.Equal(t, "pending", store.state)
	require.Equal(t, "5", store.task.Usage.Duration.String())
	require.Zero(t, logs.calls)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/video/query/h3-task", upstream.request.URL.Path)
	fingerprint := store.lastCmd.RequestFingerprint
	group.VideoModelPrices[input.Model]["720p"], group.RateMultiplier = 25, 25
	store.err = nil
	svc.settleBailianVideoTask(ctx, keys, store.task)
	require.Equal(t, "done", store.state)
	require.Len(t, upstream.requests, 1, "persisted completion does not need another provider lookup")
	require.Equal(t, fingerprint, store.lastCmd.RequestFingerprint)
	require.Equal(t, .25, store.lastCmd.BalanceCost)
	require.Equal(t, .25, logs.lastLog.ActualCost)
}
