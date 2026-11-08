package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

func (s *AccountTestService) fetchBailianVideoModels(ctx context.Context, account *Account) ([]string, []byte, *http.Request, error) {
	if s == nil || s.httpUpstream == nil || account == nil || account.Type != AccountTypeAPIKey {
		return nil, nil, nil, newUpstreamModelSyncConfigError("Bailian model sync requires an API key account", nil)
	}
	base, err := s.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return nil, nil, nil, err
	}
	base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/api/v1")
	key := strings.TrimSpace(account.GetCredential("api_key"))
	if key == "" {
		return nil, nil, nil, newUpstreamModelSyncConfigError("Bailian API key is required", nil)
	}
	var models []string
	var lastBody []byte
	var lastReq *http.Request
	seen := map[string]bool{}
	loaded := 0
	for page := 1; page <= 20; page++ {
		q := url.Values{"providers": {"wan"}, "capabilities": {"VG"}, "page_no": {strconv.Itoa(page)}, "page_size": {"100"}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/models?"+q.Encode(), nil)
		if err != nil {
			return nil, nil, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		account.ApplyHeaderOverrides(req.Header)
		body, err := s.readUpstreamModelsResponse(req, account)
		if err != nil {
			return nil, nil, req, err
		}
		if !gjson.ValidBytes(body) || gjson.GetBytes(body, "code").String() != "" || gjson.GetBytes(body, "success").Type == gjson.False {
			return nil, nil, req, newUpstreamModelSyncUpstreamError("Invalid Bailian model-list response", nil)
		}
		entries := gjson.GetBytes(body, "output.models")
		if !entries.IsArray() {
			return nil, nil, req, newUpstreamModelSyncUpstreamError("Bailian model-list response is missing output.models", nil)
		}
		for _, item := range entries.Array() {
			id := strings.TrimSpace(item.Get("model").String())
			if (id == "wan3.0-video" || id == "wan3.0-video-prime") && !seen[id] {
				models = append(models, id)
				seen[id] = true
			}
		}
		loaded += len(entries.Array())
		lastBody, lastReq = body, req
		total := gjson.GetBytes(body, "output.total")
		if (total.Type == gjson.Number && loaded >= int(total.Int())) || (total.Type != gjson.Number && len(entries.Array()) < 100) {
			if len(models) == 0 {
				return nil, nil, req, newUpstreamModelSyncUpstreamError("No Wan 3 video models are available in this Bailian workspace", nil)
			}
			return models, lastBody, lastReq, nil
		}
		if len(entries.Array()) == 0 {
			break
		}
	}
	return nil, nil, lastReq, newUpstreamModelSyncUpstreamError("Bailian model-list pagination did not finish", fmt.Errorf("maximum 20 pages"))
}
