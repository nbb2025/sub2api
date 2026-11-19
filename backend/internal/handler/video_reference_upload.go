package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// AuthorizeVideoReferenceUpload supplies the upload service with checked key
// ownership and media rules. It neither accepts files nor creates a video.
func (h *OpenAIGatewayHandler) AuthorizeVideoReferenceUpload(resolver *service.CompositeRouteResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		key, ok := middleware.GetAPIKeyFromContext(c)
		subject, hasSubject := middleware.GetAuthSubjectFromContext(c)
		if !ok || !hasSubject || key == nil || key.Group == nil || key.GroupID == nil {
			h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "A grouped API key is required")
			return
		}
		platform := key.Group.Platform
		if !service.IsDurableVideoTaskPlatform(platform) && platform != service.PlatformComposite {
			h.errorResponse(c, http.StatusForbidden, "permission_error", "The key does not have access to reference uploads")
			return
		}
		result := struct {
			UserID int64 `json:"user_id"`
			KeyID  int64 `json:"key_id"`
			service.VideoReferenceUploadRules
		}{UserID: subject.UserID, KeyID: key.ID}
		model := strings.TrimSpace(c.Query("model"))
		if model != "" {
			if !key.Group.ModelAllowlist.Allows(model) {
				h.errorResponse(c, http.StatusForbidden, "permission_error", "The model does not support reference uploads with this key")
				return
			}
			if platform == service.PlatformComposite {
				route, err := resolver.Resolve(c.Request.Context(), key.Group.ID, model, service.CompositeRouteEndpointAny)
				if err != nil {
					h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Video model routing is temporarily unavailable")
					return
				}
				platform, model = route.TargetPlatform, route.UpstreamModel
			}
			if !service.IsDurableVideoTaskPlatform(platform) {
				h.errorResponse(c, http.StatusForbidden, "permission_error", "The model does not support reference uploads with this key")
				return
			}
			rules, err := h.gatewayService.ReferenceUploadRules(c.Request.Context(), key.GroupID, platform, model)
			if errors.Is(err, service.ErrNoAvailableAccounts) {
				h.errorResponse(c, http.StatusForbidden, "permission_error", "The model does not support reference uploads with this key")
				return
			}
			if err != nil {
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Video model routing is temporarily unavailable")
				return
			}
			result.VideoReferenceUploadRules = rules
		}
		c.JSON(http.StatusOK, result)
	}
}
