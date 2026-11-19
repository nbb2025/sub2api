//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type hfsyMediaUsageRepo struct {
	service.UsageLogRepository
	logs []*service.UsageLog
}

func (r *hfsyMediaUsageRepo) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	r.logs = append(r.logs, log)
	return true, nil
}

type hfsyMediaTaskBindings struct {
	bailianReleasableCache
	owners map[string]int64
}

func (c hfsyMediaTaskBindings) GetSessionAccountID(_ context.Context, groupID int64, key string) (int64, error) {
	if groupID != 24 || c.owners[key] == 0 {
		return 0, service.ErrStickySessionNotFound
	}
	return c.owners[key], nil
}

func (c hfsyMediaTaskBindings) SetSessionAccountID(_ context.Context, _ int64, key string, accountID int64, _ time.Duration) error {
	c.owners[key] = accountID
	return nil
}

func newHFsyMediaTestHandler(t *testing.T) (*OpenAIGatewayHandler, *grokMediaSlotsCache, *grokMediaSlotUpstream, *hfsyMediaUsageRepo) {
	t.Helper()
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformHFsyAPI)
	account := service.Account{
		ID: 1, Platform: service.PlatformHFsyAPI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 50, GroupIDs: []int64{24},
		Credentials: map[string]any{"api_key": "hfsy-test-key"},
	}
	repo := grokMediaSlotRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}}
	usage := &hfsyMediaUsageRepo{}
	h.gatewayService = service.NewOpenAIGatewayService(repo, usage, nil, nil, nil, nil,
		hfsyMediaTaskBindings{bailianReleasableCache: bailianReleasableCache{bindings}, owners: map[string]int64{}}, h.cfg, nil, service.NewConcurrencyService(slots), service.NewBillingService(h.cfg, nil), nil,
		h.billingCacheService, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	return h, slots, upstream, usage
}

func hfsyMediaTestContext(ctx context.Context, method, taskID, jsonBody string) (*gin.Context, *httptest.ResponseRecorder) {
	c, w := grokMediaSlotContext(ctx, taskID == "")
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.Group.Platform = service.PlatformHFsyAPI
	key.Group.AllowImageGeneration = false
	path := service.HFsyVideoCreatePath
	if taskID != "" {
		path = service.HFsyVideoQueryPath + "/" + taskID
		c.Params = gin.Params{{Key: "task_id", Value: taskID}}
	} else {
		c.Params = nil
	}
	c.Request = httptest.NewRequest(method, path, strings.NewReader(jsonBody)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

func TestVideoReferenceUploadAuthorizationChecksGroupAndModel(t *testing.T) {
	h, _, _, _ := newHFsyMediaTestHandler(t)
	for _, tc := range []struct {
		platform, model string
		status          int
		videoAllowed    bool
	}{
		{service.PlatformHFsyAPI, "minimax-h3", http.StatusOK, false},
		{service.PlatformHFsyAPI, "sd-2.5-480", http.StatusOK, true},
		{service.PlatformHFsyAPI, "unknown", http.StatusForbidden, false},
		{service.PlatformOpenAI, "sd-2.5-480", http.StatusForbidden, false},
		{service.PlatformHFsyAPI, "", http.StatusOK, false},
	} {
		c, w := hfsyMediaTestContext(context.Background(), http.MethodGet, "", "")
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/media/upload-authorization?model="+tc.model, nil)
		key, _ := middleware.GetAPIKeyFromContext(c)
		key.Group.Platform = tc.platform
		h.AuthorizeVideoReferenceUpload(nil)(c)
		require.Equal(t, tc.status, w.Code, w.Body.String())
		if tc.status == http.StatusOK {
			require.Equal(t, key.ID, gjson.Get(w.Body.String(), "key_id").Int())
			require.Equal(t, tc.videoAllowed, gjson.Get(w.Body.String(), "video_allowed").Bool())
		}
	}
	c, w := hfsyMediaTestContext(context.Background(), http.MethodGet, "", "")
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/media/upload-authorization?model=sd-2.5-480", nil)
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.Group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"minimax-h3"}}
	h.AuthorizeVideoReferenceUpload(nil)(c)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestHFsyVideoTaskOwnershipAndCompletion(t *testing.T) {
	h, slots, upstream, usage := newHFsyMediaTestHandler(t)
	upstream.call = func(req *http.Request, accountID int64) (*http.Response, error) {
		require.Equal(t, int64(1), accountID)
		require.Equal(t, "www.hfsyapi.cn", req.URL.Host)
		require.Equal(t, "Bearer hfsy-test-key", req.Header.Get("Authorization"))
		raw := `{"id":"task_seedance","status":"IN_PROGRESS","key":"supplier-private"}`
		if req.Method == http.MethodPost {
			require.Equal(t, service.HFsyVideoCreatePath, req.URL.Path)
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, "sd-2.5-1080", gjson.GetBytes(body, "model").String())
			require.Equal(t, int64(30), gjson.GetBytes(body, "duration").Int())
		} else {
			require.Equal(t, "/v1/video/query/task_seedance", req.URL.Path)
			raw = `{"success":true,"data":{"id":"task_seedance","status":"completed","video_url":"https://cdn.example/video.mp4","width":640,"height":352,"progress":100,"key":"supplier-private","detail":{"user_id":52}}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(raw))}, nil
	}
	c, w := hfsyMediaTestContext(context.Background(), http.MethodPost, "", `{"model":"minimax-h3","prompt":"test","orientation":"portrait","duration":5}`)
	h.HFsyVideo(c)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Zero(t, upstream.calls)
	c, w = hfsyMediaTestContext(context.Background(), http.MethodPost, "", `{"model":"sd-2.5-1080","prompt":"leaf","orientation":"landscape","duration":30}`)
	h.HFsyVideo(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "task_seedance", gjson.GetBytes(w.Body.Bytes(), "id").String())
	require.NotContains(t, w.Body.String(), "supplier-private")
	require.Empty(t, usage.logs)

	c, w = hfsyMediaTestContext(context.Background(), http.MethodGet, "task_seedance", "")
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.ID++
	h.HFsyVideo(c)
	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	require.Equal(t, 1, upstream.calls)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		c, w = hfsyMediaTestContext(context.Background(), method, "task_seedance", "")
		h.HFsyVideo(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Equal(t, "https://cdn.example/video.mp4", gjson.GetBytes(w.Body.Bytes(), "data.result_url").String())
		require.Equal(t, "https://cdn.example/video.mp4", gjson.GetBytes(w.Body.Bytes(), "data.video_url").String())
		require.Equal(t, int64(640), gjson.GetBytes(w.Body.Bytes(), "data.width").Int())
		require.Equal(t, int64(352), gjson.GetBytes(w.Body.Bytes(), "data.height").Int())
		require.Equal(t, int64(100), gjson.GetBytes(w.Body.Bytes(), "data.progress").Int())
		require.NotContains(t, w.Body.String(), "supplier-private")
		require.NotContains(t, w.Body.String(), "user_id")
	}
	require.Len(t, usage.logs, 1)
	require.Equal(t, "30", usage.logs[0].VideoUsage.Duration.String())
	require.Equal(t, service.PlatformHFsyAPI, usage.logs[0].VideoUsage.Provider)
	slots.assertReleased(t)
}
