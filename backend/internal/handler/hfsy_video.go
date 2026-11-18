package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) HFsyVideo(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.Group == nil || key.GroupID == nil ||
		(key.Group.Platform != service.PlatformHFsyAPI && key.Group.Platform != service.PlatformComposite) {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "HFsyAPI video requires an HFsyAPI group")
		return
	}
	if key.Group.Platform == service.PlatformComposite && !strings.HasPrefix(c.Request.URL.Path, "/v1/video/") {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Use /v1/video/create and /v1/video/query for HFsyAPI on a composite group")
		return
	}
	endpoint, taskID := service.HFsyVideoEndpointCreate, ""
	setActualUpstreamEndpoint(c, service.HFsyVideoCreatePath)
	if c.Request.Method == http.MethodGet || c.Request.URL.Path == service.HFsyVideoQueryPath ||
		strings.HasPrefix(c.Request.URL.Path, service.HFsyVideoQueryPath+"/") {
		endpoint = service.HFsyVideoEndpointStatus
		taskID = service.HFsyVideoTaskKey(firstNonEmptyString(c.Param("task_id"), c.Param("request_id"), c.Query("id")))
		setActualUpstreamEndpoint(c, service.HFsyVideoQueryPath)
	}
	h.handleGrokMedia(c, endpoint, taskID)
}
