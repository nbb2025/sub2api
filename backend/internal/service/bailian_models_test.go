//go:build unit

package service

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBailianModelSyncUsesNativePaginationWithoutGeneration(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{responses: []*http.Response{
		grokMediaContentStatusResponse(`{"success":true,"output":{"total":3,"models":[{"model":"wan3.0-video"},{"model":"wan2.7-t2v"}]}}`),
		grokMediaContentStatusResponse(`{"success":true,"output":{"total":3,"models":[{"model":"wan3.0-video-prime"}]}}`),
	}}
	svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
	models, err := svc.FetchUpstreamSupportedModels(context.Background(), bailianTestAccount())
	require.NoError(t, err)
	require.ElementsMatch(t, BailianVideoModelIDs(), models)
	require.Len(t, upstream.requests, 2)
	for i, req := range upstream.requests {
		require.Equal(t, "GET", req.Method)
		require.Equal(t, "/api/v1/models", req.URL.Path)
		require.Equal(t, "wan", req.URL.Query().Get("providers"))
		require.Equal(t, "VG", req.URL.Query().Get("capabilities"))
		require.Equal(t, strconv.Itoa(i+1), req.URL.Query().Get("page_no"))
		require.Equal(t, "Bearer bailian-secret", req.Header.Get("Authorization"))
	}
}
