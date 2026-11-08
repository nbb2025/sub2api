//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBailianHandlerTaskOwnershipAndBillingClaim(t *testing.T) {
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformBailian)
	upstream.call = func(req *http.Request, id int64) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"request_id":"trace","output":{"task_id":"wan-task","task_status":"PENDING"}}`))}, nil
	}
	newContext := func(method string) (*gin.Context, *httptest.ResponseRecorder) {
		c, w := grokMediaSlotContext(context.Background(), method == http.MethodPost)
		key, _ := middleware.GetAPIKeyFromContext(c)
		price := 0.1
		key.Group.Platform, key.Group.VideoPrice1080P = service.PlatformBailian, &price
		key.Group.AllowImageGeneration = false // video has independent platform admission
		body := ""
		if method == http.MethodPost {
			body = `{"model":"wan3.0-video-prime","input":{"prompt":"waves"},"parameters":{"duration":30}}`
		}
		c.Request = httptest.NewRequest(method, service.BailianVideoCreatePath, strings.NewReader(body))
		c.Params = gin.Params{{Key: "task_id", Value: "wan-task"}}
		return c, w
	}
	c, w := newContext(http.MethodPost)
	h.BailianVideo(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Len(t, bindings.pending, 1)
	slots.assertReleased(t)
	c, w = newContext(http.MethodGet)
	h.BailianVideo(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	c, w = newContext(http.MethodGet)
	c.Request.URL.Path = "/v1/videos/wan-task"
	c.Params = gin.Params{{Key: "request_id", Value: "wan-task"}}
	h.BailianVideo(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, EndpointBailianTasks, GetUpstreamEndpoint(c, service.PlatformBailian))
	for _, other := range []string{"user", "key", "group", "task", "provider"} {
		c, w = newContext(http.MethodGet)
		key, _ := middleware.GetAPIKeyFromContext(c)
		switch other {
		case "user":
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 11, Concurrency: 5})
		case "key":
			key.ID = 21
		case "group":
			id := int64(25)
			key.GroupID = &id
		case "task":
			c.Params = gin.Params{{Key: "task_id", Value: "other"}}
		case "provider":
			c.Params = gin.Params{{Key: "request_id", Value: "wan-task"}}
		}
		before := upstream.calls
		if other == "provider" {
			h.GrokVideoStatus(c)
		} else {
			h.BailianVideo(c)
		}
		require.Equal(t, 404, w.Code, other+": "+w.Body.String())
		require.Equal(t, before, upstream.calls)
		slots.assertReleased(t)
	}
	c, _ = newContext(http.MethodGet)
	key, _ := middleware.GetAPIKeyFromContext(c)
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	result := &service.OpenAIForwardResult{ResponseID: "bailian:wan-task", VideoCount: 1, VideoUsage: &domain.VideoUsage{Provider: service.PlatformBailian, InputVideoDuration: json.Number("5.125"), OutputVideoDuration: json.Number("6.375"), Duration: json.Number("11.5"), Resolution: "1080p"}}
	for i := range 20 {
		bill, err := prepareBailianCompletionBilling(context.Background(), h, key, subject, result.ResponseID, result)
		require.NoError(t, err)
		if i == 0 {
			require.NotNil(t, bill)
			require.Equal(t, "11.5", bill.VideoUsage.Duration.String())
			require.Equal(t, "wan3.0-video-prime", bill.BillingModel)
			// A lease alone is not evidence of a committed charge.
			inflight, err := prepareBailianCompletionBilling(context.Background(), h, key, subject, result.ResponseID, result)
			require.Error(t, err)
			require.Nil(t, inflight)
			require.NoError(t, h.gatewayService.MarkBailianVideoSettled(context.Background(), result.ResponseID, subject.UserID, key.ID))
		} else {
			require.Nil(t, bill)
		}
	}
	require.Len(t, bindings.billed, 1)
}

type bailianHandlerBillingRepo struct {
	service.UsageBillingRepository
	err    error
	calls  int
	before func()
}

func (r *bailianHandlerBillingRepo) Apply(context.Context, *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.calls++
	if r.before != nil {
		r.before()
	}
	return &service.UsageBillingApplyResult{Applied: r.err == nil}, r.err
}

type bailianHandlerUsageRepo struct{ service.UsageLogRepository }

func (*bailianHandlerUsageRepo) Create(context.Context, *service.UsageLog) (bool, error) {
	return true, nil
}

type bailianReleasableCache struct{ *grokMediaSlotBindings }

func (c bailianReleasableCache) ReleaseGrokVideoBilled(_ context.Context, key string) error {
	delete(c.billed, key)
	return nil
}

func TestBailianHandlerCommitsSettlementBeforeReleasingResult(t *testing.T) {
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformBailian)
	raw := `{"output":{"task_id":"wan-task","task_status":"PENDING"}}`
	upstream.call = func(*http.Request, int64) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(raw))}, nil
	}
	nativeContext := func(method string) (*gin.Context, *httptest.ResponseRecorder) {
		c, w := grokMediaSlotContext(context.Background(), method == http.MethodPost)
		key, _ := middleware.GetAPIKeyFromContext(c)
		key.Group.Platform = service.PlatformBailian
		body := ""
		if method == http.MethodPost {
			body = `{"model":"wan3.0-video","input":{"prompt":"waves"}}`
		}
		c.Request = httptest.NewRequest(method, service.BailianVideoCreatePath, strings.NewReader(body))
		c.Params = gin.Params{{Key: "task_id", Value: "wan-task"}}
		return c, w
	}
	c, w := nativeContext(http.MethodPost)
	h.BailianVideo(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	// Switch the fixture from simple mode to a real durable-billing boundary.
	// The accepted job's explicit free tariff remains frozen in its snapshot.
	h.cfg.RunMode = "standard"
	billing := &bailianHandlerBillingRepo{err: errors.New("temporary database failure")}
	accounts := []service.Account{{ID: 1, Platform: service.PlatformBailian, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 50, GroupIDs: []int64{24},
		Credentials: map[string]any{"api_key": "supplier-test-key", "base_url": "https://workspace.cn-beijing.maas.aliyuncs.com"}}}
	repo := grokMediaSlotRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: accounts}}
	h.gatewayService = service.NewOpenAIGatewayService(repo, &bailianHandlerUsageRepo{}, billing, nil, nil, nil,
		bailianReleasableCache{bindings}, h.cfg, nil, service.NewConcurrencyService(slots), service.NewBillingService(h.cfg, nil), nil,
		h.billingCacheService, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	raw = `{"output":{"task_id":"wan-task","task_status":"SUCCEEDED","video_url":"https://cdn.example/paid.mp4"},"usage":{"video_count":1,"input_video_duration":0,"output_video_duration":30,"duration":30,"SR":1080}}`
	c, w = nativeContext(http.MethodGet)
	billing.before = func() { require.Empty(t, w.Body.String(), "native video URL must wait for the commit") }
	h.BailianVideo(c)
	require.Equal(t, 503, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "paid.mp4")
	require.Empty(t, bindings.billed, "failed durable billing releases the lease")
	billing.err = nil
	c, w = nativeContext(http.MethodGet)
	h.BailianVideo(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "paid.mp4")
	require.Equal(t, 2, billing.calls)
	c, w = nativeContext(http.MethodGet)
	h.BailianVideo(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 2, billing.calls, "post-commit marker skips repeat settlement")
	slots.assertReleased(t)
}
