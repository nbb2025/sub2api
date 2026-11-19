package service

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	HFsyVideoCreatePath = "/v1/video/create"
	HFsyVideoQueryPath  = "/v1/video/query"

	HFsyVideoEndpointCreate GrokMediaEndpoint = "hfsy_video_create"
	HFsyVideoEndpointStatus GrokMediaEndpoint = "hfsy_video_status"

	OpenAIEndpointCapabilityHFsyVideo OpenAIEndpointCapability = "hfsy_video"
)

type hfsyVideoModelSpec struct {
	resolution                      string
	minSeconds, maxSeconds          int
	maxPrompt                       int
	maxImages, maxVideos, maxAudios int
	maxImageBytes                   int64
	frames, requireTags             bool
	requireImage, videoNeedsImage   bool
	ultrawide                       bool
	perRequest                      bool
}

var hfsyVIP480 = hfsyVideoModelSpec{resolution: "480p", minSeconds: 5, maxSeconds: 15, maxPrompt: 10000, maxImages: 9, maxVideos: 3, maxAudios: 3, videoNeedsImage: true, ultrawide: true, requireTags: true}

var hfsyVideoModels = map[string]hfsyVideoModelSpec{
	"sd-2.5-480":       {resolution: "480p", minSeconds: 4, maxSeconds: 30, maxPrompt: 10000, maxImages: 30, maxVideos: 10, maxAudios: 10, frames: true, requireTags: true},
	"sd-2.5-720":       {resolution: "720p", minSeconds: 4, maxSeconds: 30, maxPrompt: 10000, maxImages: 30, maxVideos: 10, maxAudios: 10, frames: true, requireTags: true},
	"sd-2.5-1080":      {resolution: "1080p", minSeconds: 4, maxSeconds: 30, maxPrompt: 10000, maxImages: 30, maxVideos: 10, maxAudios: 10, frames: true, requireTags: true},
	"sd-2.5-flash-480": {resolution: "480p", minSeconds: 4, maxSeconds: 30, maxPrompt: 10000, maxImages: 10, maxVideos: 5, maxAudios: 5, frames: true, requireTags: true},
	"sd-2.5-flash-720": {resolution: "720p", minSeconds: 4, maxSeconds: 30, maxPrompt: 10000, maxImages: 10, maxVideos: 5, maxAudios: 5, frames: true, requireTags: true},
	"sd-2-fast-480":    {resolution: "480p", minSeconds: 5, maxSeconds: 15, maxPrompt: 10000, maxImages: 9, maxVideos: 3, maxAudios: 3, videoNeedsImage: true, ultrawide: true},
	"sd-2-fast-720":    {resolution: "720p", minSeconds: 5, maxSeconds: 15, maxPrompt: 10000, maxImages: 9, maxVideos: 3, maxAudios: 3, videoNeedsImage: true, ultrawide: true},
	"sd-2-mini-480":    {resolution: "480p", minSeconds: 5, maxSeconds: 15, maxPrompt: 10000, maxImages: 9, maxVideos: 3, maxAudios: 3, videoNeedsImage: true, ultrawide: true},
	"sd-2-mini-720":    {resolution: "720p", minSeconds: 5, maxSeconds: 15, maxPrompt: 10000, maxImages: 9, maxVideos: 3, maxAudios: 3, videoNeedsImage: true, ultrawide: true},
	"sd-2-vip":         hfsyVIP480,
	"sd-2-vip-480":     hfsyVIP480,
	"sd-2-vip-720":     {resolution: "720p", minSeconds: 5, maxSeconds: 15, maxPrompt: 10000, maxImages: 9, maxVideos: 3, maxAudios: 3, videoNeedsImage: true, ultrawide: true, requireTags: true},
	"minimax-h3":       {resolution: "720p", minSeconds: 5, maxSeconds: 15, maxPrompt: 7000, maxImages: 9, maxAudios: 3, maxImageBytes: 20_000_000, requireImage: true, perRequest: true},
}

var hfsyReferenceTag = regexp.MustCompile(`(?i)@(image|video|audio|图片|视频|音频)?([0-9]+)\b`)

func HFsyVideoModelIDs() []string {
	ids := make([]string, 0, len(hfsyVideoModels))
	for id := range hfsyVideoModels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (e GrokMediaEndpoint) IsHFsyVideo() bool {
	return e == HFsyVideoEndpointCreate || e == HFsyVideoEndpointStatus
}

// HFsyVideoRequest reads fields needed for routing, validation and settlement.
// Supplier-specific options stay in the original JSON for forwarding.
type HFsyVideoRequest struct {
	Model                  string   `json:"model"`
	Prompt                 string   `json:"prompt"`
	Duration               int      `json:"duration"`
	Orientation            string   `json:"orientation,omitempty"`
	Ratio                  string   `json:"ratio,omitempty"`
	StartImageURL          string   `json:"start_image_url,omitempty"`
	EndImageURL            string   `json:"end_image_url,omitempty"`
	Images                 []string `json:"images,omitempty"`
	Videos                 []string `json:"videos,omitempty"`
	Audios                 []string `json:"audios,omitempty"`
	raw                    []byte
	referenceFieldsPresent bool
}

func ParseHFsyVideoRequest(body []byte) (*HFsyVideoRequest, error) {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, fmt.Errorf("HFsyAPI video requires a JSON object")
	}
	var fieldErr error
	var req HFsyVideoRequest
	seen := make(map[string]bool, 12)
	gjson.ParseBytes(body).ForEach(func(key, _ gjson.Result) bool {
		name := strings.ToLower(key.String())
		switch name {
		case "api_key", "apikey", "authorization", "access_token", "token":
			fieldErr = fmt.Errorf("use Authorization for video-lt credentials")
			return false
		case "orientation", "ratio", "images", "videos", "audios":
			req.referenceFieldsPresent = true
		}
		if seen[name] {
			fieldErr = fmt.Errorf("duplicate HFsyAPI video field %s", name)
			return false
		}
		seen[name] = true
		return true
	})
	if fieldErr != nil {
		return nil, fieldErr
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid HFsyAPI video field type")
	}
	req.Model = strings.TrimSpace(req.Model)
	if gjson.GetBytes(body, "model").Type != gjson.String || req.Model == "" || strings.TrimSpace(req.Prompt) == "" || req.Duration <= 0 {
		return nil, fmt.Errorf("model, prompt and duration are required")
	}
	if len(req.Images) > 30 || len(req.Videos) > 10 || len(req.Audios) > 10 {
		return nil, fmt.Errorf("HFsyAPI reference media count exceeds 30 images, 10 videos or 10 audios")
	}
	// Validate even public aliases before handing URLs to content moderation.
	if err := req.validateMediaURLs(); err != nil {
		return nil, err
	}
	req.raw = body
	return &req, nil
}

func (r *HFsyVideoRequest) upstreamBody(model string) ([]byte, error) {
	if len(r.raw) == 0 {
		wire := *r
		wire.Model = model
		return json.Marshal(wire)
	}
	if gjson.GetBytes(r.raw, "model").String() == model {
		return r.raw, nil
	}
	return sjson.SetBytes(r.raw, "model", model)
}

func (r *HFsyVideoRequest) MediaRequestInfo() GrokMediaRequestInfo {
	info := GrokMediaRequestInfo{Model: r.Model, Prompt: r.Prompt, DurationSeconds: r.Duration, N: 1}
	info.InputImageURLs = append(info.InputImageURLs, r.Images...)
	for _, frameURL := range []string{r.StartImageURL, r.EndImageURL} {
		if frameURL != "" {
			info.InputImageURLs = append(info.InputImageURLs, frameURL)
		}
	}
	return info
}

func (r *HFsyVideoRequest) upstreamSpec(model string) (hfsyVideoModelSpec, error) {
	spec, ok := hfsyVideoModels[model]
	if !ok {
		return spec, fmt.Errorf("unsupported HFsyAPI video model %s", model)
	}
	return spec, r.validate(spec)
}

func (r *HFsyVideoRequest) ValidateUpstreamModel(model string) error {
	_, err := r.upstreamSpec(model)
	return err
}

func (r *HFsyVideoRequest) validateMediaURLs() error {
	for _, media := range []struct {
		field string
		urls  []string
	}{{"images", r.Images}, {"videos", r.Videos}, {"audios", r.Audios}} {
		for _, raw := range media.urls {
			if !validHFsyMediaURL(raw) {
				return fmt.Errorf("%s requires public HTTP(S) URLs", media.field)
			}
		}
	}
	for _, frame := range []string{r.StartImageURL, r.EndImageURL} {
		if frame != "" && !validHFsyMediaURL(frame) {
			return fmt.Errorf("frame images require public HTTP(S) URLs")
		}
	}
	return nil
}

func (r *HFsyVideoRequest) validate(spec hfsyVideoModelSpec) error {
	if r.Duration < spec.minSeconds || r.Duration > spec.maxSeconds {
		return fmt.Errorf("duration must be an integer between %d and %d seconds", spec.minSeconds, spec.maxSeconds)
	}
	if utf8.RuneCountInString(r.Prompt) > spec.maxPrompt {
		return fmt.Errorf("prompt exceeds %d characters", spec.maxPrompt)
	}
	frames := r.StartImageURL != "" || r.EndImageURL != ""
	if frames {
		if !spec.frames || r.StartImageURL == "" {
			return fmt.Errorf("frame mode requires a supported model and start_image_url")
		}
		if r.referenceFieldsPresent || r.Orientation != "" || r.Ratio != "" || r.Images != nil || r.Videos != nil || r.Audios != nil {
			return fmt.Errorf("frame mode cannot be combined with orientation, ratio or reference media")
		}
		return nil
	}
	if r.Orientation != "landscape" && r.Orientation != "portrait" {
		return fmt.Errorf("orientation must be landscape or portrait")
	}
	if r.Ratio != "" {
		if spec.perRequest {
			return fmt.Errorf("use orientation to select the MiniMax H3 aspect ratio")
		}
		switch r.Ratio {
		case "9:16", "3:4", "1:1", "4:3", "16:9":
		case "21:9":
			if !spec.ultrawide {
				return fmt.Errorf("unsupported ratio")
			}
		default:
			return fmt.Errorf("unsupported ratio")
		}
	}
	if (spec.requireImage || (spec.videoNeedsImage && len(r.Videos) > 0)) && len(r.Images) == 0 {
		return fmt.Errorf("at least one reference image is required")
	}
	for _, medium := range []struct {
		name string
		urls []string
		max  int
	}{{"images", r.Images, spec.maxImages}, {"videos", r.Videos, spec.maxVideos}, {"audios", r.Audios, spec.maxAudios}} {
		if len(medium.urls) > medium.max {
			return fmt.Errorf("%s supports at most %d reference URLs for this model", medium.name, medium.max)
		}
	}
	// Each documented clip is at least 3 s, so six links cannot fit the
	// 15-second limit even when their actual durations are unknown here.
	if len(r.Videos)*3 > 15 || len(r.Audios)*3 > 15 {
		return fmt.Errorf("reference videos and reference audio must each total at most 15 seconds")
	}
	if total := len(r.Images) + len(r.Videos) + len(r.Audios); total > 0 {
		tags := hfsyReferenceTag.FindAllStringSubmatch(r.Prompt, -1)
		if spec.requireTags && len(tags) == 0 {
			return fmt.Errorf("reference media requires @image1, @video1 or @audio1 tags in the prompt")
		}
		counts := map[string]int{"": total, "image": len(r.Images), "video": len(r.Videos), "audio": len(r.Audios),
			"图片": len(r.Images), "视频": len(r.Videos), "音频": len(r.Audios)}
		for _, tag := range tags {
			index, err := strconv.Atoi(tag[2])
			if err != nil || index < 1 || index > counts[strings.ToLower(tag[1])] {
				return fmt.Errorf("prompt contains a reference tag without matching media")
			}
		}
	}
	return nil
}

func validHFsyMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		return addr.IsGlobalUnicast() && !addr.IsPrivate()
	}
	return strings.Contains(host, ".") && !strings.HasSuffix(host, ".localhost") &&
		!strings.HasSuffix(host, ".local") && !strings.HasSuffix(host, ".internal")
}
