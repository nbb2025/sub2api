//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func bailianTestAccount() *Account {
	return &Account{ID: 8, Platform: PlatformBailian, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "bailian-secret", "base_url": "https://workspace.cn-beijing.maas.aliyuncs.com",
		"model_mapping": map[string]any{"wan-public": "wan3.0-video-prime"},
	}}
}

func TestBailianNativeRequestCapabilities(t *testing.T) {
	for _, body := range []string{
		`{"model":"wan3.0-video","input":{"prompt":"waves"},"parameters":{"duration":30,"resolution":"1080P","ratio":"21:9","audio":false,"seed":0,"prompt_extend":false,"watermark":true}}`,
		`{"model":"wan3.0-video","input":{"prompt":"frames","media":[{"type":"first_frame","url":"data:image/png;base64,YQ=="},{"type":"last_frame","url":"https://example.com/last.png"}]}}`,
		`{"model":"wan3.0-video-prime","input":{"prompt":"编辑视频1，参考音频1","media":[{"type":"reference_image","url":"https://example.com/a.png"},{"type":"reference_video","url":"https://example.com/a.mp4"},{"type":"reference_audio","url":"oss://dashscope-instant/a.mp3"}]},"parameters":{"duration":-1,"ratio":"adaptive"}}`,
		`{"model":"wan3.0-video","input":{"media":[{"type":"file","url":"https://example.com/demo.pdf"}]},"parameters":{"prompt_extend":true}}`,
		`{"model":"wan3.0-video","input":{"media":[{"type":"link","url":"https://example.com/page"}]}}`,
		`{"model":"wan3.0-video","input":{"media":[{"type":"reference_video","url":"https://example.com/one-second.mp4"}]},"parameters":{"duration":1}}`,
	} {
		_, err := ParseBailianVideoRequest([]byte(body))
		require.NoError(t, err, body)
	}
	info, err := ParseBailianVideoRequest([]byte(`{"model":"wan3.0-video","input":{"prompt":"waves"}}`))
	require.NoError(t, err)
	require.Equal(t, 5, info.DurationSeconds)
	require.Equal(t, "1080p", info.Resolution)
	for _, body := range []string{
		`[]`, `{} {}`, `{"model":"x","input":{}}`,
		`{"model":"x","input":{"prompt":1}}`,
		`{"model":"x","input":{"prompt":"x"},"parameters":{"duration":31}}`,
		`{"model":"x","input":{"prompt":"x"},"parameters":{"duration":2.5}}`,
		`{"model":"x","input":{"prompt":"x"},"parameters":{"duration":1}}`,
		`{"model":"x","input":{"prompt":"x"},"parameters":{"audio":"false"}}`,
		`{"model":"x","input":{"prompt":"x"},"parameters":{"resolution":"2K"}}`,
		`{"model":"x","input":{"media":[{"type":"last_frame","url":"https://example.com/a.png"}]}}`,
		`{"model":"x","input":{"media":[{"type":"first_frame","url":"https://example.com/a.png"},{"type":"reference_audio","url":"https://example.com/a.mp3"}]}}`,
		`{"model":"x","input":{"media":[{"type":"file","url":"https://example.com/a.pdf"}]},"parameters":{"prompt_extend":false}}`,
	} {
		_, err := ParseBailianVideoRequest([]byte(body))
		require.Error(t, err, body)
	}
}

func TestBailianNativeForwardingAndOSS(t *testing.T) {
	body := []byte(`{"model":"wan-public","input":{"prompt":"编辑视频1，参考音频1","media":[{"type":"reference_video","url":"https://example.com/a.mp4"},{"type":"reference_audio","url":"oss://dashscope-instant/a.mp3"}]},"parameters":{"duration":-1,"audio":false,"prompt_extend":true,"ratio":"adaptive"},"future_field":{"keep":true}}`)
	raw := `{"request_id":"http-trace","output":{"task_id":"wan-task","task_status":"PENDING"}}`
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(raw)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodPost, BailianVideoCreatePath, nil)
	result, err := svc.ForwardBailianVideo(context.Background(), c, bailianTestAccount(), BailianEndpointCreate, "", body)
	require.NoError(t, err)
	require.Empty(t, w.Body.String(), "task state must be saved before releasing native responses")
	svc.WriteBufferedMediaResponse(c, result)
	require.Equal(t, raw, w.Body.String())
	require.Equal(t, "bailian:wan-task", result.ResponseID)
	require.Equal(t, "wan-public", result.BillingModel)
	require.Equal(t, "wan3.0-video-prime", result.UpstreamModel)
	require.Equal(t, -1, result.VideoDurationSeconds)
	require.Zero(t, result.VideoCount, "creation never charges")
	require.Equal(t, "Bearer bailian-secret", upstream.request.Header.Get("Authorization"))
	require.Equal(t, "enable", upstream.request.Header.Get("X-DashScope-Async"))
	require.Equal(t, "enable", upstream.request.Header.Get("X-DashScope-OssResourceResolve"))
	require.Equal(t, BailianVideoCreatePath, upstream.request.URL.Path)
	forwarded, err := io.ReadAll(upstream.request.Body)
	require.NoError(t, err)
	for _, field := range []string{"input", "parameters", "future_field"} {
		require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(forwarded, field).Raw)
	}
}

func bailianCompletedBody(usage string) string {
	return `{"request_id":"trace","output":{"task_id":"wan-task","task_status":"SUCCEEDED","video_url":"https://cdn.example/video.mp4"},"usage":` + usage + `}`
}

func TestBailianCompletedUsagePreservesDecimalSeconds(t *testing.T) {
	rawUsage := `{"video_count":1,"duration":11.500000001,"input_video_duration":5.125000001,"output_video_duration":6.375,"SR":1080,"fps":30}`
	raw := bailianCompletedBody(rawUsage)
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(raw)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodGet, "/api/v1/tasks/wan-task", nil)
	result, err := svc.ForwardBailianVideo(context.Background(), c, bailianTestAccount(), BailianEndpointStatus, "bailian:wan-task", nil)
	require.NoError(t, err)
	require.Empty(t, w.Body.String(), "task state must be saved before releasing native responses")
	svc.WriteBufferedMediaResponse(c, result)
	require.Equal(t, raw, w.Body.String())
	require.Equal(t, "/api/v1/tasks/wan-task", upstream.request.URL.Path)
	require.Equal(t, 1, result.VideoCount)
	require.Zero(t, result.Usage.OutputTokens)
	require.Equal(t, "11.500000001", result.VideoUsage.Duration.String())
	require.Equal(t, "5.125000001", result.VideoUsage.InputVideoDuration.String())
	require.Equal(t, "1080p", result.VideoResolution)
	encoded, err := json.Marshal(result.VideoUsage)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"duration":11.500000001`)
	require.NotContains(t, string(encoded), "video.mp4")
}

func TestBailianCostUsesTotalSecondsAndWalletPrecision(t *testing.T) {
	price := 0.12345678
	group := &Group{VideoPrice1080P: &price}
	for _, tc := range []struct{ in, out, total string }{{"0", "30", "30"}, {"5.125000001", "6.375", "11.500000001"}, {"5", "10", "15"}} {
		usage := &domain.VideoUsage{Provider: PlatformBailian, InputVideoDuration: json.Number(tc.in), OutputVideoDuration: json.Number(tc.out), Duration: json.Number(tc.total), Resolution: "1080p"}
		cost, err := CalculateBailianVideoCost(group, "wan3.0-video", usage, 1.25)
		require.NoError(t, err)
		d, err := decimal.NewFromString(tc.total)
		require.NoError(t, err)
		want, _ := d.Mul(decimal.NewFromFloat(price)).Mul(decimal.NewFromFloat(1.25)).Round(8).Float64()
		require.Equal(t, want, cost.ActualCost)
		require.Equal(t, "video", cost.BillingMode)
		require.True(t, decimal.NewFromFloat(cost.InputCost).Add(decimal.NewFromFloat(cost.OutputCost)).Equal(decimal.NewFromFloat(cost.TotalCost)))
	}
	_, err := BailianVideoUnitPrice(&Group{}, "wan3.0-video", "1080p")
	require.Error(t, err, "must not fall back to Grok/token pricing or invent a CNY/USD rate")
	zero := 0.0
	_, err = BailianVideoUnitPrice(&Group{VideoPrice1080P: &zero}, "wan3.0-video", "1080p")
	require.NoError(t, err, "explicit free pricing is valid")
}

func TestBailianInvalidUsageNeverGuesses(t *testing.T) {
	noise, err := ParseBailianVideoUsage([]byte(`{"video_count":1,"duration":5.300000000000001,"input_video_duration":0.1,"output_video_duration":5.2,"SR":720}`))
	require.NoError(t, err)
	require.Equal(t, "5.300000000000001", noise.Duration.String())
	for _, usage := range []string{
		`{}`, `{"video_count":1,"duration":10,"SR":720}`,
		`{"video_count":1,"duration":10,"input_video_duration":5,"output_video_duration":10,"SR":720}`,
		`{"video_count":1,"duration":31,"input_video_duration":1,"output_video_duration":30,"SR":720}`,
		`{"video_count":1,"duration":10,"input_video_duration":0,"output_video_duration":10,"SR":2160}`,
		`{"video_count":1,"duration":"10","input_video_duration":0,"output_video_duration":10,"SR":720}`,
	} {
		_, err := ParseBailianVideoUsage([]byte(usage))
		require.Error(t, err, usage)
	}
	for _, status := range []string{"PENDING", "RUNNING", "FAILED", "CANCELED", "UNKNOWN"} {
		upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"output":{"task_id":"wan-task","task_status":"` + status + `"}}`)}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		c, _ := grokMediaContentTestContext(http.MethodGet, "/api/v1/tasks/wan-task", nil)
		result, err := svc.ForwardBailianVideo(context.Background(), c, bailianTestAccount(), BailianEndpointStatus, "bailian:wan-task", nil)
		require.NoError(t, err)
		require.Nil(t, result.VideoUsage)
		require.Zero(t, result.VideoCount)
	}
}

func TestBailianAccountCapabilityIsolation(t *testing.T) {
	a := bailianTestAccount()
	require.True(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityBailianVideo))
	for _, capability := range []OpenAIEndpointCapability{OpenAIEndpointCapabilitySeedance, OpenAIEndpointCapabilityGrokMediaGeneration, OpenAIEndpointCapabilityChatCompletions, OpenAIEndpointCapabilityResponses} {
		require.False(t, a.SupportsOpenAIEndpointCapability(capability))
	}
	a.Type = AccountTypeOAuth
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityBailianVideo))
	require.True(t, strings.HasPrefix(BailianTaskKey("wan-task"), "bailian:"))
}

func TestBailianMappedModelPricesAndRecordUsage(t *testing.T) {
	groupID := int64(950)
	group := &Group{ID: groupID, Platform: PlatformBailian, RateMultiplier: 1.25, VideoModelPrices: map[string]map[string]float64{
		"wan3.0-video": {"1080p": 0.02}, "wan3.0-video-prime": {"1080p": 0.12345678},
	}}
	model, err := ResolveBailianVideoBillingModel(group, "friendly-wan", "wan3.0-video-prime", "1080p")
	require.NoError(t, err)
	require.Equal(t, "wan3.0-video-prime", model)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	usage := &domain.VideoUsage{Provider: PlatformBailian, InputVideoDuration: json.Number("5.125000001"), OutputVideoDuration: json.Number("6.375"), Duration: json.Number("11.500000001"), Resolution: "1080p"}
	input := &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "grok-video:bailian:task-billing", ResponseID: "bailian:task-billing", Model: "friendly-wan", BillingModel: model, UpstreamModel: "wan3.0-video-prime", VideoCount: 1, VideoResolution: "1080p", VideoUsage: usage, Duration: time.Second},
		APIKey: &APIKey{ID: 951, GroupID: &groupID, Group: group}, User: &User{ID: 952}, Account: &Account{ID: 953, Platform: PlatformBailian},
	}
	require.NoError(t, svc.RecordUsage(context.Background(), input))
	want, err := CalculateBailianVideoCost(group, model, usage, 1.25)
	require.NoError(t, err)
	require.Equal(t, want.ActualCost, billingRepo.lastCmd.BalanceCost)
	require.Equal(t, want.ActualCost, usageRepo.lastLog.ActualCost)
	require.Equal(t, usage, usageRepo.lastLog.VideoUsage)
	require.Nil(t, usageRepo.lastLog.VideoDurationSeconds, "Bailian never uses the capped integer field")
	require.Zero(t, userRepo.deductCalls, "wallet changes are owned by the durable billing repository")
	require.Zero(t, usageRepo.lastLog.TotalTokens())
	firstFingerprint := billingRepo.lastCmd.RequestFingerprint
	billingRepo.result = &UsageBillingApplyResult{Applied: false}
	require.NoError(t, svc.RecordUsage(context.Background(), input))
	require.Equal(t, firstFingerprint, billingRepo.lastCmd.RequestFingerprint)
	require.Equal(t, 2, billingRepo.calls)
	require.Zero(t, userRepo.deductCalls, "deduplicated billing never falls back to a second wallet debit")
}
