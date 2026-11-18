package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// BailianVideo exposes the native DashScope task contract on a distinct platform.
// It shares gateway admission, scheduling, ownership and durable usage dedup;
// no Grok response conversion or integer-duration billing applies.
func (h *OpenAIGatewayHandler) BailianVideo(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.Group == nil || key.GroupID == nil || (key.Group.Platform != service.PlatformBailian && key.Group.Platform != service.PlatformComposite) {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Bailian video requires a Bailian group")
		return
	}
	endpoint, taskID := service.BailianEndpointCreate, ""
	setActualUpstreamEndpoint(c, EndpointBailianVideoCreate)
	if c.Request.Method == http.MethodGet {
		endpoint, taskID = service.BailianEndpointStatus, service.BailianTaskKey(firstNonEmptyString(c.Param("task_id"), c.Param("request_id")))
		setActualUpstreamEndpoint(c, EndpointBailianTasks)
	}
	h.handleGrokMedia(c, endpoint, taskID)
}

func prepareDurableVideoCompletionBilling(ctx context.Context, h *OpenAIGatewayHandler, key *service.APIKey, subject middleware.AuthSubject, taskID, platform string, result *service.OpenAIForwardResult) (*service.OpenAIForwardResult, error) {
	if result == nil {
		return nil, nil
	}
	if platform == service.PlatformHFsyAPI {
		if result.VideoTaskOutcome != service.VideoTaskSucceeded {
			return nil, nil
		}
	} else if result.VideoUsage == nil || result.VideoCount != 1 {
		return nil, nil
	}
	pending, err := h.gatewayService.LoadGrokVideoPendingBilling(ctx, taskID, subject.UserID, key.ID)
	if err != nil || pending == nil {
		if restored, restoreErr := h.gatewayService.RestoreBailianVideoTaskBinding(ctx, taskID, key, subject.UserID); restoreErr == nil && restored {
			pending, err = h.gatewayService.LoadGrokVideoPendingBilling(ctx, taskID, subject.UserID, key.ID)
		}
	}
	if err != nil || pending == nil || pending.Platform != platform {
		return nil, fmt.Errorf("video task billing snapshot is unavailable")
	}
	usage := result.VideoUsage
	if platform == service.PlatformHFsyAPI {
		usage, err = pending.HFsyCompletedUsage()
		if err != nil {
			return nil, err
		}
	}
	pricing := pending.DurableVideoPricing()
	if pricing == nil || pricing.BillingModels[usage.Resolution] == "" {
		return nil, fmt.Errorf("video task price snapshot is unavailable for the output resolution")
	}
	if err := h.gatewayService.PersistBailianVideoObservedUsage(ctx, taskID, subject.UserID, key.ID, usage); err != nil {
		return nil, err
	}
	claimed, settled, err := h.gatewayService.ClaimBailianVideoBilling(ctx, taskID, subject.UserID, key.ID)
	if err != nil {
		return nil, fmt.Errorf("video task billing claim is unavailable")
	}
	if settled {
		return nil, nil
	}
	if !claimed {
		return nil, fmt.Errorf("video task settlement is in progress; retry lookup")
	}
	merged := *result
	merged.Model, merged.BillingModel = pending.Model, pricing.BillingModels[usage.Resolution]
	merged.BailianVideoPricing, merged.HFsyVideoPricing = pending.BailianPricing, pending.HFsyPricing
	merged.VideoUsage, merged.VideoCount, merged.VideoResolution = usage, 1, usage.Resolution
	merged.UpstreamModel = firstNonEmptyString(pending.UpstreamModel, result.UpstreamModel)
	merged.RequestID = service.StableGrokVideoBillingRequestID(taskID)
	merged.ResponseID = taskID
	merged.Duration = service.GrokVideoE2EDuration(pending.CreatedAt, time.Now())
	return &merged, nil
}
