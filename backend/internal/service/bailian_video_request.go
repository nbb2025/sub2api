package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/tidwall/gjson"
)

const (
	BailianEndpointCreate                GrokMediaEndpoint        = "bailian_video_create"
	BailianEndpointStatus                GrokMediaEndpoint        = "bailian_video_status"
	OpenAIEndpointCapabilityBailianVideo OpenAIEndpointCapability = "bailian_video"
	BailianVideoCreatePath                                        = "/api/v1/services/aigc/video-generation/video-synthesis"
)

func (e GrokMediaEndpoint) IsBailian() bool {
	return e == BailianEndpointCreate || e == BailianEndpointStatus
}

func BailianVideoModelIDs() []string { return []string{"wan3.0-video", "wan3.0-video-prime"} }

func BailianTaskKey(id string) string { return "bailian:" + strings.TrimSpace(id) }

type bailianWorkbenchMediaKey struct{}

// Only the authenticated workbench route sets this marker, after validating
// both a live login session and ownership of the billing API key.
func WithBailianWorkbenchMedia(ctx context.Context) context.Context {
	return context.WithValue(ctx, bailianWorkbenchMediaKey{}, true)
}

func ParseBailianVideoRequestForContext(ctx context.Context, body []byte) (GrokMediaRequestInfo, error) {
	allowInline, _ := ctx.Value(bailianWorkbenchMediaKey{}).(bool)
	return parseBailianVideoRequest(body, allowInline)
}

// ParseBailianVideoRequest validates the native Wan 3 contract without rewriting
// its body or defaults. All task modes share the same model and native endpoint.
// Remote media size, duration and input+output limits are validated by Bailian.
func ParseBailianVideoRequest(body []byte) (GrokMediaRequestInfo, error) {
	return parseBailianVideoRequest(body, false)
}

func parseBailianVideoRequest(body []byte, allowInline bool) (GrokMediaRequestInfo, error) {
	var info GrokMediaRequestInfo
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return info, fmt.Errorf("request body must be a JSON object")
	}
	root := gjson.ParseBytes(body)
	model := root.Get("model")
	if model.Type != gjson.String || strings.TrimSpace(model.String()) == "" {
		return info, fmt.Errorf("model is required")
	}
	info.Model = strings.TrimSpace(model.String())
	input := root.Get("input")
	if !input.IsObject() {
		return info, fmt.Errorf("input must be an object")
	}
	prompt := input.Get("prompt")
	if prompt.Exists() && prompt.Type != gjson.String {
		return info, fmt.Errorf("input.prompt must be a string")
	}
	info.Prompt = prompt.String()
	info.N, info.DurationSeconds, info.Resolution = 1, 5, VideoBillingResolution1080P
	params := root.Get("parameters")
	if params.Exists() && !params.IsObject() {
		return info, fmt.Errorf("parameters must be an object")
	}
	if value := params.Get("duration"); value.Exists() {
		var duration int
		if value.Type != gjson.Number || json.Unmarshal([]byte(value.Raw), &duration) != nil ||
			(duration != -1 && (duration < 1 || duration > 30)) {
			return info, fmt.Errorf("duration must be -1 or a positive integer up to 30")
		}
		info.DurationSeconds = duration
	}
	if value := params.Get("resolution"); value.Exists() {
		if value.Type != gjson.String || (value.String() != "480P" && value.String() != "720P" && value.String() != "1080P") {
			return info, fmt.Errorf("resolution must be 480P, 720P or 1080P")
		}
		info.Resolution = strings.ToLower(value.String())
	}
	if value := params.Get("ratio"); value.Exists() {
		if value.Type != gjson.String {
			return info, fmt.Errorf("ratio must be a string")
		}
		switch value.String() {
		case "adaptive", "21:9", "16:9", "4:3", "1:1", "3:4", "9:16":
		default:
			return info, fmt.Errorf("unsupported ratio")
		}
	}
	for _, field := range []string{"audio", "prompt_extend", "watermark"} {
		if value := params.Get(field); value.Exists() && value.Type != gjson.True && value.Type != gjson.False {
			return info, fmt.Errorf("%s must be a boolean", field)
		}
	}
	if value := params.Get("seed"); value.Exists() {
		var seed int64
		if value.Type != gjson.Number || json.Unmarshal([]byte(value.Raw), &seed) != nil || seed < -1 || seed > 2147483647 {
			return info, fmt.Errorf("seed must be -1 or an integer from 0 to 2147483647")
		}
	}
	media := input.Get("media")
	if media.Exists() && !media.IsArray() {
		return info, fmt.Errorf("input.media must be an array")
	}
	if len(media.Array()) > 20 {
		return info, fmt.Errorf("at most 20 media items are allowed")
	}
	limits := map[string]int{"first_frame": 1, "last_frame": 1, "reference_image": 10, "reference_video": 5, "reference_audio": 5, "file": 1, "link": 1}
	counts := map[string]int{}
	for _, item := range media.Array() {
		kind := item.Get("type").String()
		limit, supported := limits[kind]
		if !item.IsObject() || !supported {
			return info, fmt.Errorf("unsupported media type")
		}
		counts[kind]++
		if counts[kind] > limit {
			return info, fmt.Errorf("too many %s items", kind)
		}
		value := item.Get("url")
		validURL := validBailianMediaURL(kind, value.String())
		if allowInline && !validURL {
			validURL = validBailianWorkbenchMedia(kind, value.String())
		}
		if value.Type != gjson.String || !validURL {
			return info, fmt.Errorf("invalid %s URL", kind)
		}
		if kind == "first_frame" || kind == "last_frame" || kind == "reference_image" {
			info.InputImageURLs = append(info.InputImageURLs, value.String())
		}
	}
	if strings.TrimSpace(info.Prompt) == "" && len(counts) == 0 {
		return info, fmt.Errorf("input.prompt or input.media is required")
	}
	// The documented 2-second lower bound applies to requests without video.
	// For reference/edit/extension input, Bailian validates actual media duration
	// and the input+output limit; do not apply the text-to-video lower bound.
	if counts["reference_video"] == 0 && info.DurationSeconds != -1 && info.DurationSeconds < 2 {
		return info, fmt.Errorf("duration without video input must be -1 or an integer from 2 to 30")
	}
	if counts["last_frame"] > 0 && counts["first_frame"] == 0 {
		return info, fmt.Errorf("last_frame requires first_frame")
	}
	if counts["first_frame"]+counts["last_frame"] > 0 {
		for _, kind := range []string{"reference_image", "reference_video", "reference_audio", "file", "link"} {
			if counts[kind] > 0 {
				return info, fmt.Errorf("frame and reference media cannot be combined")
			}
		}
	}
	if counts["file"]+counts["link"] > 0 {
		if counts["file"] > 0 && counts["link"] > 0 {
			return info, fmt.Errorf("file and link cannot be combined")
		}
		if params.Get("prompt_extend").Type == gjson.False {
			return info, fmt.Errorf("file and link require prompt_extend=true")
		}
	}
	return info, nil
}

func validBailianWorkbenchMedia(kind, raw string) bool {
	header, payload, ok := strings.Cut(raw, ",")
	if !ok || payload == "" || strings.ContainsAny(payload, "\r\n") {
		return false
	}
	maxBytes := 0
	switch kind {
	case "reference_video":
		if header == "data:video/mp4;base64" || header == "data:video/quicktime;base64" {
			maxBytes = 100_000_000
		}
	case "reference_audio":
		if header == "data:audio/wav;base64" || header == "data:audio/x-wav;base64" || header == "data:audio/mpeg;base64" || header == "data:audio/mp3;base64" {
			maxBytes = 15_000_000
		}
	}
	if maxBytes == 0 || len(payload) > base64.StdEncoding.EncodedLen(maxBytes) {
		return false
	}
	decoded, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(payload)))
	return err == nil && decoded > 0 && decoded <= int64(maxBytes)
}

func validBailianMediaURL(kind, raw string) bool {
	if strings.HasPrefix(raw, "data:image/") && (kind == "first_frame" || kind == "last_frame" || kind == "reference_image") {
		header, payload, ok := strings.Cut(raw, ",")
		if !ok || !strings.HasSuffix(header, ";base64") || payload == "" {
			return false
		}
		_, err := base64.StdEncoding.DecodeString(payload)
		return err == nil
	}
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && u.User == nil &&
		(u.Scheme == "http" || u.Scheme == "https" || (kind != "link" && u.Scheme == "oss"))
}

func bailianUsesOSS(body []byte) bool { return bytes.Contains(body, []byte(`oss://`)) }
