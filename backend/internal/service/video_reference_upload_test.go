package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReferenceUploadRulesUseMappedModels(t *testing.T) {
	account := func(platform, mapped string) Account {
		return Account{Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{
			"api_key": "fixture", "base_url": "https://video.example",
			"model_mapping": map[string]any{"public-video": mapped},
		}}
	}
	seedance := account(PlatformHFsyAPI, "sd-2.5-flash-480")
	h3 := account(PlatformHFsyAPI, "minimax-h3")
	wan := account(PlatformBailian, "wan3.0-video-prime")
	for _, tc := range []struct {
		platform    string
		accounts    []Account
		imageBytes  int64
		allowsVideo bool
	}{
		{PlatformHFsyAPI, []Account{seedance}, 30_000_000, true},
		{PlatformHFsyAPI, []Account{h3}, 20_000_000, false},
		{PlatformHFsyAPI, []Account{seedance, h3}, 20_000_000, false},
		{PlatformBailian, []Account{wan, h3}, 20_000_000, true},
	} {
		rules, err := referenceUploadRules(tc.accounts, tc.platform, "public-video")
		require.NoError(t, err)
		require.Equal(t, VideoReferenceUploadRules{Platform: tc.platform, MaxImageBytes: tc.imageBytes, VideoAllowed: tc.allowsVideo}, rules)
	}
	_, err := referenceUploadRules([]Account{seedance}, PlatformHFsyAPI, "undeclared")
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	_, err = referenceUploadRules([]Account{wan}, PlatformHFsyAPI, "public-video")
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
}
