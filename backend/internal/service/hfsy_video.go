package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

const DefaultHFsyAPIBaseURL = "https://www.hfsyapi.cn"

var hfsyTaskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

func HFsyVideoTaskKey(id string) string {
	id = strings.TrimPrefix(strings.TrimSpace(id), "hfsyapi:")
	if !hfsyTaskIDPattern.MatchString(id) {
		return ""
	}
	return "hfsyapi:" + id
}

func hfsyAPIBaseURL(account *Account) string {
	if base := strings.TrimSpace(account.GetCredential("base_url")); base != "" {
		return strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")
	}
	return DefaultHFsyAPIBaseURL
}

type hfsyVideoResponse struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	ResultURL        string `json:"result_url,omitempty"`
	VideoURL         string `json:"video_url,omitempty"`
	Progress         *int64 `json:"progress,omitempty"`
	Width            int64  `json:"width,omitempty"`
	Height           int64  `json:"height,omitempty"`
	FailReason       string `json:"fail_reason,omitempty"`
	StatusUpdateTime int64  `json:"status_update_time,omitempty"`
}

type HFsyVideoUpstreamError struct {
	StatusCode int
	RetryAfter time.Duration
	Message    string
}

func (e *HFsyVideoUpstreamError) Error() string {
	return fmt.Sprintf("HFsyAPI upstream status %d", e.StatusCode)
}

func parseHFsyVideoResponse(raw []byte, create bool, apiKey string) (hfsyVideoResponse, VideoTaskOutcome, error) {
	root := gjson.ParseBytes(raw)
	var response hfsyVideoResponse
	if !gjson.ValidBytes(raw) || !root.IsObject() || (root.Get("success").Exists() && !root.Get("success").Bool()) ||
		root.Get("error").IsObject() {
		return response, VideoTaskUnknown, fmt.Errorf("invalid HFsyAPI video response")
	}
	item := root
	if data := root.Get("data"); data.IsObject() {
		item = data
	}
	id := item.Get("task_id").String()
	if id == "" && item.Get("id").Type == gjson.String {
		id = item.Get("id").String()
	}
	response.ID = strings.TrimSpace(id)
	if HFsyVideoTaskKey(response.ID) == "" || strings.Contains(response.ID, ":") ||
		(apiKey != "" && strings.Contains(response.ID, apiKey)) {
		return response, VideoTaskUnknown, fmt.Errorf("HFsyAPI response missing a valid task ID")
	}
	response.StatusUpdateTime = item.Get("status_update_time").Int()
	if progress := item.Get("progress"); progress.Type == gjson.Number && progress.Num >= 0 && progress.Num <= 100 {
		value := progress.Int()
		response.Progress = &value
	}
	status := strings.ToLower(strings.TrimSpace(item.Get("status").String()))
	outcome := VideoTaskUnknown
	switch status {
	case "not_start", "submitted", "pending", "queued":
		response.Status = "queued"
	case "in_progress", "processing", "running":
		response.Status = "in_progress"
	case "success", "succeeded", "completed":
		response.Status = "completed"
		outcome = VideoTaskSucceeded
		if !create {
			for _, field := range []string{"result_url", "video_url"} {
				candidate := strings.TrimSpace(item.Get(field).String())
				if validHFsyMediaURL(candidate) && (apiKey == "" || !strings.Contains(candidate, apiKey)) {
					response.ResultURL, response.VideoURL = candidate, candidate
					break
				}
			}
			if response.ResultURL == "" {
				return response, VideoTaskUnknown, fmt.Errorf("completed HFsyAPI task is missing a valid video URL")
			}
			if width := item.Get("width").Int(); width > 0 {
				response.Width = width
			}
			if height := item.Get("height").Int(); height > 0 {
				response.Height = height
			}
		}
	case "failure", "failed", "canceled", "cancelled", "expired":
		response.Status = "failed"
		outcome = VideoTaskFailed
		if reason := strings.TrimSpace(item.Get("fail_reason").String()); reason != "" {
			if apiKey != "" {
				reason = strings.ReplaceAll(reason, apiKey, "[REDACTED]")
			}
			response.FailReason = sanitizeUpstreamErrorMessage(reason)
		}
	default:
		if !create {
			return response, VideoTaskUnknown, fmt.Errorf("invalid HFsyAPI task status")
		}
		// Do not lose an accepted job even if the create reply omits status.
		response.Status = "in_progress"
	}
	if create {
		switch response.Status {
		case "queued":
			response.Status = "QUEUED"
		case "completed":
			response.Status = "SUCCESS"
		case "failed":
			response.Status = "FAILURE"
		default:
			response.Status = "IN_PROGRESS"
		}
	}
	return response, outcome, nil
}

// ForwardHFsyVideo never retries a paid create. Polling always uses the owner
// account, and only whitelisted public task fields are returned to the client.
func (s *OpenAIGatewayService) ForwardHFsyVideo(ctx context.Context, c *gin.Context, account *Account, endpoint GrokMediaEndpoint, taskKey string, input *HFsyVideoRequest) (*OpenAIForwardResult, error) {
	if account == nil || account.Platform != PlatformHFsyAPI || account.Type != AccountTypeAPIKey || !endpoint.IsHFsyVideo() {
		return nil, fmt.Errorf("HFsyAPI video requires an HFsyAPI Key account")
	}
	base, err := s.validateUpstreamBaseURL(hfsyAPIBaseURL(account))
	if err != nil {
		return nil, err
	}
	base = strings.TrimRight(base, "/")
	create := endpoint == HFsyVideoEndpointCreate
	path, method := HFsyVideoCreatePath, http.MethodPost
	result := &OpenAIForwardResult{}
	var payload []byte
	if create {
		if input == nil {
			return nil, fmt.Errorf("HFsyAPI video request is required")
		}
		upstreamModel := account.GetMappedModel(input.Model)
		spec, err := input.upstreamSpec(upstreamModel)
		if err != nil {
			return nil, err
		}
		payload, err = input.upstreamBody(upstreamModel)
		if err != nil {
			return nil, err
		}
		result.Model, result.UpstreamModel = input.Model, upstreamModel
		result.VideoResolution, result.VideoDurationSeconds = spec.resolution, input.Duration
	} else {
		taskKey = HFsyVideoTaskKey(taskKey)
		if taskKey == "" {
			return nil, fmt.Errorf("invalid HFsyAPI task ID")
		}
		path = HFsyVideoQueryPath + "/" + strings.TrimPrefix(taskKey, "hfsyapi:")
		method = http.MethodGet
	}
	key := strings.TrimSpace(account.GetCredential("api_key"))
	if key == "" {
		return nil, fmt.Errorf("HFsyAPI account is missing api_key")
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	if create {
		req.Header.Set("Content-Type", "application/json")
	}
	account.ApplyHeaderOverrides(req.Context(), req.Header)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	started := time.Now()
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(started).Milliseconds())
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		upstreamErr := &HFsyVideoUpstreamError{StatusCode: resp.StatusCode, RetryAfter: bailianRetryAfter(resp.Header.Get("Retry-After"))}
		if create && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity) {
			if raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<10)); readErr == nil {
				message := strings.TrimSpace(extractUpstreamErrorMessage(raw))
				message = sanitizeUpstreamErrorMessage(strings.ReplaceAll(message, key, "[REDACTED]"))
				upstreamErr.Message = strings.Join(strings.Fields(truncateString(message, 512)), " ")
			}
		}
		return nil, upstreamErr
	}
	raw, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, nil)
	if err != nil {
		return nil, err
	}
	status, outcome, err := parseHFsyVideoResponse(raw, create, key)
	if err != nil {
		return nil, err
	}
	result.ResponseID = HFsyVideoTaskKey(status.ID)
	if !create && result.ResponseID != taskKey {
		return nil, fmt.Errorf("HFsyAPI response task ID mismatch")
	}
	result.Duration = time.Since(started)
	var body []byte
	if create {
		body, err = json.Marshal(status)
	} else {
		result.VideoTaskOutcome = outcome
		body, err = json.Marshal(struct {
			Data    hfsyVideoResponse `json:"data"`
			Success bool              `json:"success"`
		}{Data: status, Success: true})
	}
	if err != nil {
		return nil, err
	}
	result.DeferredMediaResponse = &BufferedMediaResponse{
		StatusCode: resp.StatusCode, Header: http.Header{"Content-Type": {"application/json"}}, Body: body,
	}
	return result, nil
}

func (s *OpenAIGatewayService) SnapshotHFsyVideoPricing(ctx context.Context, key *APIKey, account *Account, input *HFsyVideoRequest) (*VideoTaskPricingSnapshot, float64, error) {
	if input == nil {
		return nil, 0, fmt.Errorf("HFsyAPI video request is required")
	}
	snapshot, err := s.newVideoTaskPricingSnapshot(ctx, key, account)
	if err != nil {
		return nil, 0, err
	}
	upstream := account.GetMappedModel(input.Model)
	spec, err := input.upstreamSpec(upstream)
	if err != nil {
		return nil, 0, err
	}
	billingModel := input.Model
	price := LookupVideoModelPrice(key.Group.VideoModelPrices, billingModel, spec.resolution)
	if price == nil {
		billingModel = upstream
		price = LookupVideoModelPrice(key.Group.VideoModelPrices, billingModel, spec.resolution)
	}
	if price == nil {
		// The provider documents both names for the same 480p VIP tier.
		switch upstream {
		case "sd-2-vip":
			billingModel = "sd-2-vip-480"
		case "sd-2-vip-480":
			billingModel = "sd-2-vip"
		}
		price = LookupVideoModelPrice(key.Group.VideoModelPrices, billingModel, spec.resolution)
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple && !simpleModeKeyRateLimitBillingEnabled(s.cfg, key) {
		zero := 0.0
		price = &zero
	}
	if price == nil || math.IsNaN(*price) || math.IsInf(*price, 0) || *price < 0 {
		unit := "second"
		if spec.perRequest {
			unit = "request"
		}
		return nil, 0, fmt.Errorf("configure a model-specific HFsyAPI USD/%s price for %s %s", unit, upstream, spec.resolution)
	}
	snapshot.Prices[spec.resolution], snapshot.BillingModels[spec.resolution] = *price, billingModel
	snapshot.PerRequest = spec.perRequest
	cost, err := calculateHFsyVideoCost(snapshot, hfsyVideoUsage(spec.resolution, input.Duration))
	if err != nil {
		return nil, 0, err
	}
	return snapshot, cost.ActualCost, nil
}

func hfsyVideoUsage(resolution string, seconds int) *domain.VideoUsage {
	duration := json.Number(strconv.Itoa(seconds))
	return &domain.VideoUsage{Provider: PlatformHFsyAPI, InputVideoDuration: "0", OutputVideoDuration: duration, Duration: duration, Resolution: resolution}
}

func (p GrokVideoPendingBilling) HFsyCompletedUsage() (*domain.VideoUsage, error) {
	spec, ok := hfsyVideoModels[p.UpstreamModel]
	minSeconds := spec.minSeconds
	// Tasks accepted under the earlier four-second policy retain their tariff.
	switch p.UpstreamModel {
	case "sd-2-fast-480", "sd-2-fast-720", "sd-2-vip-480":
		minSeconds = 4
	}
	if p.Platform != PlatformHFsyAPI || p.HFsyPricing == nil || !ok || p.HFsyPricing.PerRequest != spec.perRequest ||
		p.VideoResolution != spec.resolution || p.VideoDurationSeconds < minSeconds || p.VideoDurationSeconds > spec.maxSeconds {
		return nil, fmt.Errorf("HFsyAPI task billing snapshot is missing or invalid")
	}
	return hfsyVideoUsage(spec.resolution, p.VideoDurationSeconds), nil
}

func calculateHFsyVideoCost(pricing *VideoTaskPricingSnapshot, usage *domain.VideoUsage) (*CostBreakdown, error) {
	if pricing == nil || usage == nil || usage.Provider != PlatformHFsyAPI {
		return nil, fmt.Errorf("HFsyAPI video billing requires a task price")
	}
	price, ok := pricing.Prices[usage.Resolution]
	if !ok || price < 0 || math.IsInf(price, 0) || math.IsNaN(price) ||
		pricing.RateMultiplier < 0 || math.IsInf(pricing.RateMultiplier, 0) || math.IsNaN(pricing.RateMultiplier) {
		return nil, fmt.Errorf("invalid HFsyAPI video tariff")
	}
	duration, err := usage.Duration.Int64()
	if err != nil || duration < 4 || duration > 30 || usage.Duration != usage.OutputVideoDuration || usage.InputVideoDuration != "0" {
		return nil, fmt.Errorf("invalid HFsyAPI output duration")
	}
	units := duration
	if pricing.PerRequest {
		units = 1
	}
	total := decimal.NewFromFloat(price).Mul(decimal.NewFromInt(units))
	base, _ := total.Round(UsageBillingMonetaryScale).Float64()
	actual, _ := total.Mul(decimal.NewFromFloat(pricing.RateMultiplier)).Round(UsageBillingMonetaryScale).Float64()
	return &CostBreakdown{BillingMode: string(BillingModeVideo), TotalCost: base, OutputCost: base, ActualCost: actual}, nil
}
