package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBailianNativeRoutesRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformBailian)
	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, tc := range []struct{ method, path string }{{http.MethodPost, "/v1/videos/generations"}, {http.MethodGet, "/v1/videos/:request_id"}, {http.MethodGet, "/v1/models"}, {http.MethodPost, service.BailianVideoCreatePath}, {http.MethodGet, "/api/v1/tasks/:task_id"}, {http.MethodGet, "/api/v1/models"}} {
		require.True(t, registered[tc.method+" "+tc.path], tc.method+" "+tc.path)
	}
}

func TestBailianV1VideoRoutesDispatchToGateway(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformBailian)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/videos/generations"},
		{http.MethodGet, "/v1/videos/wan-task"},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"model":"wan3.0-video","input":{"prompt":"waves"}}`)))
		// The test router deliberately supplies no user context. Reaching the
		// gateway's context check proves Bailian passed the platform gate.
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "User context not found")
	}
}
