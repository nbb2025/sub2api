package routes

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type bailianWorkbenchAuthorizationKey struct{}

// The normal gateway chain authenticates the API key first. Authenticate the
// additional browser session using the existing JWT middleware, then bind its
// owner to that API key before enabling inline video/audio. No client header
// can enable this mode on the public video routes.
func bailianWorkbenchSession(auth middleware.JWTAuthMiddleware) []gin.HandlerFunc {
	prepare := func(c *gin.Context) {
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || key.Group == nil || key.Group.Platform != service.PlatformBailian {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Wan workbench requires a Bailian group"}})
			return
		}
		if len(c.Request.Header.Values("Authorization")) != 1 || len(c.Request.Header.Values("X-Iagent-Studio-Session")) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"type": "authentication_error", "message": "A workbench login session is required"}})
			return
		}
		session := strings.TrimSpace(c.GetHeader("X-Iagent-Studio-Session"))
		if session == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"type": "authentication_error", "message": "A workbench login session is required"}})
			return
		}
		authorization := c.GetHeader("Authorization")
		ctx := context.WithValue(c.Request.Context(), bailianWorkbenchAuthorizationKey{}, authorization)
		c.Request = c.Request.Clone(ctx)
		c.Request.Header.Del("X-Iagent-Studio-Session")
		c.Request.Header.Set("Authorization", "Bearer "+session)
		defer c.Request.Header.Set("Authorization", authorization)
		c.Next()
	}
	bindOwner := func(c *gin.Context) {
		key, ok := middleware.GetAPIKeyFromContext(c)
		subject, loggedIn := middleware.GetAuthSubjectFromContext(c)
		if !ok || !loggedIn || key.UserID != subject.UserID {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": "The API key must belong to the logged-in user"}})
			return
		}
		authorization, _ := c.Request.Context().Value(bailianWorkbenchAuthorizationKey{}).(string)
		c.Request.Header.Set("Authorization", authorization)
		c.Request = c.Request.WithContext(service.WithBailianWorkbenchMedia(c.Request.Context()))
		c.Next()
	}
	return []gin.HandlerFunc{prepare, gin.HandlerFunc(auth), bindOwner}
}
