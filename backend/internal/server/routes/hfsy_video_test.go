package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestHFsyVideoNativeRoutesDispatchByGroup(t *testing.T) {
	hfsyRouter := newGatewayRoutesTestRouter(service.PlatformHFsyAPI)
	otherRouter := newGatewayRoutesTestRouter(service.PlatformBailian)
	for _, test := range []struct{ method, path string }{
		{http.MethodPost, "/v1/video/create"},
		{http.MethodGet, "/v1/video/query/task_sd"},
		{http.MethodGet, "/v1/video/query?id=task_sd"},
		{http.MethodPost, "/v1/video/query/task_sd"},
		{http.MethodPost, "/v1/video/query?id=task_sd"},
	} {
		w := httptest.NewRecorder()
		hfsyRouter.ServeHTTP(w, httptest.NewRequest(test.method, test.path, nil))
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "User context not found")
		w = httptest.NewRecorder()
		otherRouter.ServeHTTP(w, httptest.NewRequest(test.method, test.path, nil))
		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "HFsyAPI video requires an HFsyAPI group")
	}
	for _, test := range []struct{ method, path string }{
		{http.MethodPost, "/v1/videos/generations"},
		{http.MethodGet, "/v1/videos/task_sd"},
	} {
		w := httptest.NewRecorder()
		hfsyRouter.ServeHTTP(w, httptest.NewRequest(test.method, test.path, strings.NewReader(`{"model":"sd-2.5-1080"}`)))
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "User context not found")
	}
}
