package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// BufferedMediaResponse lets admission persist task ownership and completion
// billing state before a native response becomes visible to a polling client.
type BufferedMediaResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Freeze the accepted job's tariff. A later price/rate edit must not change a
// retry's durable billing fingerprint or reprice work already submitted.
type BailianVideoPricingSnapshot struct {
	Prices                map[string]float64 `json:"prices"`
	BillingModels         map[string]string  `json:"billing_models"`
	RateMultiplier        float64            `json:"rate_multiplier"`
	AccountRateMultiplier float64            `json:"account_rate_multiplier"`
	SubscriptionID        *int64             `json:"subscription_id,omitempty"`
	KeyQuota              float64            `json:"key_quota"`
	KeyRateLimit5h        float64            `json:"key_rate_limit_5h"`
	KeyRateLimit1d        float64            `json:"key_rate_limit_1d"`
	KeyRateLimit7d        float64            `json:"key_rate_limit_7d"`
	AccountQuotaLimit     float64            `json:"account_quota_limit"`
	AccountDailyLimit     float64            `json:"account_daily_limit"`
	AccountWeeklyLimit    float64            `json:"account_weekly_limit"`
}

func (s *OpenAIGatewayService) SnapshotBailianVideoPricing(ctx context.Context, key *APIKey, account *Account, model string) (*BailianVideoPricingSnapshot, error) {
	group := bailianVideoPricingGroup(key, s.cfg)
	if group == nil || account == nil || key.User == nil {
		return nil, fmt.Errorf("Bailian video pricing requires a group, user and account")
	}
	rate := s.ResolveUserGroupRateMultiplier(ctx, key.User.ID, group.ID, group.RateMultiplier)
	snapshot := &BailianVideoPricingSnapshot{
		Prices: map[string]float64{}, BillingModels: map[string]string{},
		RateMultiplier: resolveVideoRateMultiplier(key, rate), AccountRateMultiplier: account.BillingRateMultiplier(),
		KeyQuota: key.Quota, KeyRateLimit5h: key.RateLimit5h, KeyRateLimit1d: key.RateLimit1d, KeyRateLimit7d: key.RateLimit7d,
		AccountQuotaLimit: account.GetQuotaLimit(), AccountDailyLimit: account.GetQuotaDailyLimit(), AccountWeeklyLimit: account.GetQuotaWeeklyLimit(),
	}
	if math.IsNaN(snapshot.RateMultiplier) || math.IsInf(snapshot.RateMultiplier, 0) || snapshot.RateMultiplier < 0 ||
		math.IsNaN(snapshot.AccountRateMultiplier) || math.IsInf(snapshot.AccountRateMultiplier, 0) || snapshot.AccountRateMultiplier < 0 {
		return nil, fmt.Errorf("invalid Bailian video billing multiplier")
	}
	for _, resolution := range []string{VideoBillingResolution480P, VideoBillingResolution720P, VideoBillingResolution1080P} {
		billingModel, err := ResolveBailianVideoBillingModel(group, model, account.GetMappedModel(model), resolution)
		if err != nil {
			continue // An unpriced output resolution remains unavailable for billing.
		}
		price, _ := BailianVideoUnitPrice(group, billingModel, resolution)
		snapshot.Prices[resolution], snapshot.BillingModels[resolution] = *price, billingModel
	}
	return snapshot, nil
}

type BailianVideoUpstreamError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *BailianVideoUpstreamError) Error() string {
	return fmt.Sprintf("Bailian upstream status %d", e.StatusCode)
}

func bailianRetryAfter(raw string) time.Duration {
	if len(raw) > 128 {
		return 0
	}
	if seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil && !math.IsNaN(seconds) && !math.IsInf(seconds, 0) && seconds > 0 && seconds < 365*24*60*60 {
		return time.Duration(seconds * float64(time.Second))
	}
	if date, err := http.ParseTime(raw); err == nil && date.After(time.Now()) {
		return time.Until(date)
	}
	return 0
}

func (p *BailianVideoPricingSnapshot) billingGroup(resolution string) (*Group, error) {
	if p == nil {
		return nil, fmt.Errorf("Bailian task pricing snapshot is unavailable")
	}
	price, ok := p.Prices[resolution]
	if !ok || math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
		return nil, fmt.Errorf("Bailian task has no valid price for %s", resolution)
	}
	return &Group{VideoPrice480P: &price, VideoPrice720P: &price, VideoPrice1080P: &price}, nil
}

func bailianVideoSettledKey(taskID string, userID, keyID int64) string {
	return "bailian-settled:" + grokVideoPendingBillingKey(taskID, userID, keyID)
}

// Claims are short leases, not proof of payment. Only the marker written after
// the SQL commit is treated as settled; a crashed process can be retried after
// the lease expires and SQL deduplication still prevents a second debit.
func (s *OpenAIGatewayService) ClaimBailianVideoBilling(ctx context.Context, taskID string, userID, keyID int64) (claimed, settled bool, err error) {
	key := grokVideoPendingBillingKey(taskID, userID, keyID)
	if s == nil || s.cache == nil || key == "" {
		return false, false, fmt.Errorf("Bailian billing cache is unavailable")
	}
	marker, err := s.cache.GetGrokVideoPendingBilling(ctx, bailianVideoSettledKey(taskID, userID, keyID))
	if err != nil {
		return false, false, err
	}
	if string(marker) == "settled" {
		return false, true, nil
	}
	claimed, err = s.cache.ClaimGrokVideoBilled(ctx, key, 30*time.Second)
	return claimed, false, err
}

func (s *OpenAIGatewayService) MarkBailianVideoSettled(ctx context.Context, taskID string, userID, keyID int64) error {
	if s == nil || s.cache == nil || grokVideoPendingBillingKey(taskID, userID, keyID) == "" {
		return fmt.Errorf("Bailian billing cache is unavailable")
	}
	return s.cache.SetGrokVideoPendingBilling(ctx, bailianVideoSettledKey(taskID, userID, keyID), []byte("settled"), 24*time.Hour)
}

// IEEE-754 serialization can make 0.1 + 0.2 appear as
// 0.30000000000000004. Accept only this tiny consistency noise; preserve and
// bill the reported total itself, without rounding seconds or replacing it.
var bailianDurationFloatTolerance = decimal.New(1, -12)

func bailianDurationConsistent(input, output, total decimal.Decimal) bool {
	return total.Sub(input.Add(output)).Abs().LessThanOrEqual(bailianDurationFloatTolerance)
}

func (s *OpenAIGatewayService) WriteBufferedMediaResponse(c *gin.Context, result *OpenAIForwardResult) {
	if result == nil || result.DeferredMediaResponse == nil {
		return
	}
	r := result.DeferredMediaResponse
	writeGrokMediaResponse(c, &http.Response{StatusCode: r.StatusCode, Header: r.Header}, r.Body, s.responseHeaderFilter)
}

// ForwardBailianVideo speaks the supplier's native protocol. Async creates are
// deliberately never returned as failover errors: an ambiguous response may
// already represent a paid job. Polls stay bound to the creating account/key.
func (s *OpenAIGatewayService) ForwardBailianVideo(ctx context.Context, c *gin.Context, account *Account, endpoint GrokMediaEndpoint, taskKey string, body []byte) (*OpenAIForwardResult, error) {
	if account == nil || account.Platform != PlatformBailian || account.Type != AccountTypeAPIKey || !endpoint.IsBailian() {
		return nil, fmt.Errorf("Bailian video requires a Bailian API key account")
	}
	base, err := s.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return nil, err
	}
	base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/api/v1")
	path, method := BailianVideoCreatePath, http.MethodPost
	result := &OpenAIForwardResult{}
	if endpoint == BailianEndpointCreate {
		info, err := ParseBailianVideoRequest(body)
		if err != nil {
			return nil, err
		}
		result.Model, result.BillingModel = info.Model, info.Model
		result.UpstreamModel = account.GetMappedModel(info.Model)
		result.VideoResolution, result.VideoDurationSeconds = info.Resolution, info.DurationSeconds
		body, err = sjson.SetBytes(body, "model", result.UpstreamModel)
		if err != nil {
			return nil, err
		}
	} else {
		id := strings.TrimPrefix(taskKey, "bailian:")
		if id == "" {
			return nil, fmt.Errorf("task_id is required")
		}
		if err := validateUpstreamPathSegment("Bailian task ID", id); err != nil {
			return nil, err
		}
		path, method, body = "/api/v1/tasks/"+id, http.MethodGet, nil
		result.ResponseID = BailianTaskKey(id)
	}
	key := strings.TrimSpace(account.GetCredential("api_key"))
	if key == "" {
		return nil, fmt.Errorf("Bailian account missing api_key")
	}
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	account.ApplyHeaderOverrides(req.Header)
	if endpoint == BailianEndpointCreate {
		req.Header.Set("X-DashScope-Async", "enable")
		if bailianUsesOSS(body) {
			req.Header.Set("X-DashScope-OssResourceResolve", "enable")
		}
	}
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(started).Milliseconds())
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var tooLarge TooLargeWriter
	if c != nil {
		tooLarge = openAITooLargeError
	}
	raw, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, tooLarge)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if c != nil {
			writeGrokMediaResponse(c, resp, raw, s.responseHeaderFilter)
		}
		return nil, &BailianVideoUpstreamError{StatusCode: resp.StatusCode, RetryAfter: bailianRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject() {
		return nil, fmt.Errorf("invalid Bailian task response")
	}
	result.Duration, result.ResponseHeaders = time.Since(started), resp.Header.Clone()
	result.RequestID = strings.TrimSpace(gjson.GetBytes(raw, "request_id").String())
	if gjson.GetBytes(raw, "code").String() != "" {
		if c != nil {
			writeGrokMediaResponse(c, resp, raw, s.responseHeaderFilter)
		}
		return nil, fmt.Errorf("Bailian API rejected the request")
	}
	id := strings.TrimSpace(gjson.GetBytes(raw, "output.task_id").String())
	if id == "" || validateUpstreamPathSegment("Bailian task ID", id) != nil {
		return nil, fmt.Errorf("Bailian response missing a valid task ID")
	}
	if endpoint == BailianEndpointStatus && BailianTaskKey(id) != result.ResponseID {
		return nil, fmt.Errorf("Bailian response task ID mismatch")
	}
	result.ResponseID = BailianTaskKey(id)
	switch gjson.GetBytes(raw, "output.task_status").String() {
	case "PENDING", "RUNNING", "FAILED", "CANCELED", "UNKNOWN":
	case "SUCCEEDED":
		if strings.TrimSpace(gjson.GetBytes(raw, "output.video_url").String()) == "" {
			return nil, fmt.Errorf("completed Bailian task is missing video_url")
		}
		usage, err := ParseBailianVideoUsage([]byte(gjson.GetBytes(raw, "usage").Raw))
		if err != nil {
			return nil, err
		}
		result.VideoUsage, result.VideoCount, result.VideoResolution = usage, 1, usage.Resolution
	default:
		return nil, fmt.Errorf("invalid Bailian task status")
	}
	result.DeferredMediaResponse = &BufferedMediaResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: raw}
	return result, nil
}

// ParseBailianVideoUsage uses decimal arithmetic and the reported total. It
// never falls back to requested/smart duration or invents tokens for missing
// usage. The native response remains byte-for-byte unchanged for clients.
func ParseBailianVideoUsage(raw []byte) (*domain.VideoUsage, error) {
	if !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject() {
		return nil, fmt.Errorf("completed Bailian task is missing usage")
	}
	values := make([]json.Number, 3)
	seconds := make([]decimal.Decimal, 3)
	for i, field := range []string{"input_video_duration", "output_video_duration", "duration"} {
		v := gjson.GetBytes(raw, field)
		if v.Type != gjson.Number || len(v.Raw) > 64 {
			return nil, fmt.Errorf("invalid Bailian usage.%s", field)
		}
		d, err := decimal.NewFromString(v.Raw)
		if err != nil || d.Exponent() < -18 || d.Exponent() > 18 || d.IsNegative() || d.GreaterThan(decimal.NewFromInt(30)) {
			return nil, fmt.Errorf("invalid Bailian usage.%s", field)
		}
		values[i], seconds[i] = json.Number(v.Raw), d
	}
	if seconds[0].GreaterThan(decimal.NewFromInt(15)) || !seconds[1].IsPositive() || !bailianDurationConsistent(seconds[0], seconds[1], seconds[2]) {
		return nil, fmt.Errorf("inconsistent Bailian input/output/total video durations")
	}
	count, sr := gjson.GetBytes(raw, "video_count"), gjson.GetBytes(raw, "SR")
	if count.Type != gjson.Number || count.Raw != "1" || sr.Type != gjson.Number {
		return nil, fmt.Errorf("invalid Bailian video count or resolution")
	}
	resolution, known := LookupVideoBillingResolution(sr.Raw)
	if !known {
		return nil, fmt.Errorf("unsupported Bailian video resolution")
	}
	return &domain.VideoUsage{Provider: PlatformBailian, InputVideoDuration: values[0], OutputVideoDuration: values[1], Duration: values[2], Resolution: resolution}, nil
}

// BailianVideoUnitPrice requires an administrator's explicit USD/second price.
// Aliyun's CNY rate card is not a USD wallet tariff; no exchange rate is assumed.
func BailianVideoUnitPrice(group *Group, model, resolution string) (*float64, error) {
	if group == nil {
		return nil, fmt.Errorf("Bailian video pricing requires a group")
	}
	if _, ok := LookupVideoBillingResolution(resolution); !ok {
		return nil, fmt.Errorf("unsupported Bailian video resolution")
	}
	price := group.GetVideoPriceForModel(model, resolution)
	if price == nil || math.IsNaN(*price) || math.IsInf(*price, 0) || *price < 0 {
		return nil, fmt.Errorf("configure an explicit Bailian USD/second price for %s %s", model, resolution)
	}
	return price, nil
}

func bailianVideoPricingGroup(key *APIKey, cfg *config.Config) *Group {
	if key == nil {
		return nil
	}
	group := key.Group
	if group == nil || cfg == nil || cfg.RunMode != config.RunModeSimple || simpleModeKeyRateLimitBillingEnabled(cfg, key) {
		return group
	}
	// Simple mode deliberately has no wallet billing. Its explicit optional
	// key-cost limit mode still requires configured prices, as in other gateways.
	copy := *group
	zero := 0.0
	copy.VideoPrice480P, copy.VideoPrice720P, copy.VideoPrice1080P = &zero, &zero, &zero
	copy.VideoModelPrices = nil
	return &copy
}

func (s *OpenAIGatewayService) ResolveBailianVideoBillingModel(key *APIKey, requested, upstream, resolution string) (string, error) {
	return ResolveBailianVideoBillingModel(bailianVideoPricingGroup(key, s.cfg), requested, upstream, resolution)
}

// Explicit public-alias prices win, followed by the actual mapped Wan model,
// then the group's common resolution rate. This keeps prime/standard distinct
// even when an account exposes a friendly model alias.
func ResolveBailianVideoBillingModel(group *Group, requested, upstream, resolution string) (string, error) {
	if group == nil {
		return "", fmt.Errorf("Bailian video pricing requires a group")
	}
	for _, model := range []string{requested, upstream} {
		if LookupVideoModelPrice(group.VideoModelPrices, model, resolution) != nil {
			_, err := BailianVideoUnitPrice(group, model, resolution)
			return model, err
		}
	}
	_, err := BailianVideoUnitPrice(group, requested, resolution)
	return requested, err
}

func CalculateBailianVideoCost(group *Group, model string, usage *domain.VideoUsage, multiplier float64) (*CostBreakdown, error) {
	if usage == nil || usage.Provider != PlatformBailian {
		return nil, fmt.Errorf("authoritative Bailian usage is required")
	}
	price, err := BailianVideoUnitPrice(group, model, usage.Resolution)
	if err != nil {
		return nil, err
	}
	if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier < 0 {
		return nil, fmt.Errorf("invalid Bailian billing multiplier")
	}
	in, err := decimal.NewFromString(usage.InputVideoDuration.String())
	if err != nil {
		return nil, err
	}
	out, err := decimal.NewFromString(usage.OutputVideoDuration.String())
	if err != nil {
		return nil, err
	}
	total, err := decimal.NewFromString(usage.Duration.String())
	if err != nil || in.IsNegative() || !out.IsPositive() || !bailianDurationConsistent(in, out, total) || total.GreaterThan(decimal.NewFromInt(30)) {
		return nil, fmt.Errorf("invalid Bailian billable seconds")
	}
	unit := decimal.NewFromFloat(*price)
	base := unit.Mul(total).Round(UsageBillingMonetaryScale)
	input := unit.Mul(in).Round(UsageBillingMonetaryScale)
	cost := &CostBreakdown{BillingMode: string(BillingModeVideo)}
	cost.TotalCost, _ = base.Float64()
	cost.InputCost, _ = input.Float64()
	cost.OutputCost, _ = base.Sub(input).Float64()
	cost.ActualCost, _ = unit.Mul(total).Mul(decimal.NewFromFloat(multiplier)).Round(UsageBillingMonetaryScale).Float64()
	return cost, nil
}
