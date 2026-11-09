package routes

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBailianWorkbenchRequiresRealSessionAndSameKeyOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jwtAuth := middleware.JWTAuthMiddleware(func(c *gin.Context) {
		subject := middleware.AuthSubject{UserID: 7}
		switch c.GetHeader("Authorization") {
		case "Bearer valid-login":
		case "Bearer other-user":
			subject.UserID = 8
		default:
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set(string(middleware.ContextKeyUser), subject)
		c.Next()
	})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer billing-key" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{UserID: 7, Group: &service.Group{Platform: service.PlatformBailian}})
		c.Next()
	})
	body := `{"model":"alias","input":{"media":[{"type":"reference_video","url":"data:video/mp4;base64,AAAA"}]},"parameters":{"duration":2}}`
	creates := 0
	handler := func(c *gin.Context) {
		require.Equal(t, "Bearer billing-key", c.GetHeader("Authorization"))
		if c.Request.URL.Path == "/v1/wan/workbench/videos/generations" {
			require.Empty(t, c.GetHeader("X-Iagent-Studio-Session"))
		}
		data, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		_, err = service.ParseBailianVideoRequestForContext(c.Request.Context(), data)
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		creates++
		c.Status(http.StatusOK)
	}
	router.POST("/v1/videos/generations", handler)
	router.POST("/v1/wan/workbench/videos/generations", append(bailianWorkbenchSession(jwtAuth), handler)...)
	for _, tc := range []struct {
		path, session string
		duplicate     bool
		status        int
	}{
		{"/v1/videos/generations", "valid-login", false, http.StatusBadRequest},
		{"/v1/wan/workbench/videos/generations", "", false, http.StatusUnauthorized},
		{"/v1/wan/workbench/videos/generations", "billing-key", false, http.StatusUnauthorized},
		{"/v1/wan/workbench/videos/generations", "fake-login", false, http.StatusUnauthorized},
		{"/v1/wan/workbench/videos/generations", "other-user", false, http.StatusForbidden},
		{"/v1/wan/workbench/videos/generations", "valid-login", true, http.StatusUnauthorized},
		{"/v1/wan/workbench/videos/generations", "valid-login", false, http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer billing-key")
		if tc.session != "" {
			req.Header.Set("X-Iagent-Studio-Session", tc.session)
		}
		if tc.duplicate {
			req.Header.Add("X-Iagent-Studio-Session", "another")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, tc.status, w.Code, "%s %s", tc.path, tc.session)
		require.Equal(t, "Bearer billing-key", req.Header.Get("Authorization"))
	}
	require.Equal(t, 1, creates)
}
