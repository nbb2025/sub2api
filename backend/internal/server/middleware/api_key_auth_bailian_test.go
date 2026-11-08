//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBailianTaskReadAfterQuotaExhaustionStillRequiresAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := &service.Group{ID: 8, Platform: service.PlatformBailian, Status: service.StatusActive, Hydrated: true}
	key := &service.APIKey{
		ID: 10, UserID: 11, Key: "sk-bailian-owned", Status: service.StatusAPIKeyQuotaExhausted,
		User:  &service.User{ID: 11, Role: service.RoleUser, Status: service.StatusActive, Balance: 0},
		Group: group, GroupID: &group.ID,
	}
	repo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) {
		copy := *key
		return &copy, nil
	}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	router := newAuthTestRouter(service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg), nil, cfg)
	router.GET("/api/v1/tasks/:task_id", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.POST(service.BailianVideoCreatePath, func(c *gin.Context) { c.Status(http.StatusOK) })
	for _, tc := range []struct {
		method, path, status string
		want                 int
	}{
		{http.MethodGet, "/api/v1/tasks/owned-task", service.StatusAPIKeyQuotaExhausted, http.StatusOK},
		{http.MethodPost, service.BailianVideoCreatePath, service.StatusAPIKeyQuotaExhausted, http.StatusTooManyRequests},
		{http.MethodGet, "/api/v1/tasks/owned-task", service.StatusDisabled, http.StatusUnauthorized},
	} {
		key.Status = tc.status
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+key.Key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, tc.want, w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/v1/tasks/", "/api/v1/tasks/id/extra", service.BailianVideoCreatePath} {
		require.False(t, isBailianVideoTaskRead(http.MethodGet, path))
	}
	require.False(t, isBailianVideoTaskRead(http.MethodPost, "/api/v1/tasks/id"))
}
