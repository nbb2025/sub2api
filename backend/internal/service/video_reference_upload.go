package service

import (
	"context"
	"slices"
	"strings"
)

type VideoReferenceUploadRules struct {
	Platform      string `json:"platform"`
	MaxImageBytes int64  `json:"max_image_bytes"`
	VideoAllowed  bool   `json:"video_allowed"`
}

// ReferenceUploadRules resolves the media capabilities of the accounts that
// could serve a public model, without taking a slot or making an upstream call.
func (s *OpenAIGatewayService) ReferenceUploadRules(ctx context.Context, groupID *int64, platform, model string) (VideoReferenceUploadRules, error) {
	accounts, err := s.listSchedulableAccounts(ctx, groupID, platform)
	if err != nil {
		return VideoReferenceUploadRules{}, err
	}
	return referenceUploadRules(accounts, platform, model)
}

func referenceUploadRules(accounts []Account, platform, model string) (VideoReferenceUploadRules, error) {
	rules := VideoReferenceUploadRules{Platform: platform, VideoAllowed: true}
	capability := OpenAIEndpointCapabilityBailianVideo
	if platform == PlatformHFsyAPI {
		capability = OpenAIEndpointCapabilityHFsyVideo
	}
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != platform || !account.SupportsOpenAIEndpointCapability(capability) ||
			strings.TrimSpace(account.GetCredential("api_key")) == "" || !account.IsModelSupported(model) {
			continue
		}
		mapped := account.GetMappedModel(model)
		var imageLimit int64
		switch platform {
		case PlatformBailian:
			if !slices.Contains(BailianVideoModelIDs(), mapped) {
				continue
			}
			imageLimit = 20_000_000
		case PlatformHFsyAPI:
			spec, ok := hfsyVideoModels[mapped]
			if !ok {
				continue
			}
			imageLimit = spec.maxImageBytes
			if imageLimit == 0 {
				imageLimit = 30_000_000
			}
			rules.VideoAllowed = rules.VideoAllowed && spec.maxVideos > 0
		default:
			continue
		}
		if rules.MaxImageBytes == 0 || imageLimit < rules.MaxImageBytes {
			rules.MaxImageBytes = imageLimit
		}
	}
	if rules.MaxImageBytes == 0 {
		return VideoReferenceUploadRules{}, ErrNoAvailableAccounts
	}
	return rules, nil
}
