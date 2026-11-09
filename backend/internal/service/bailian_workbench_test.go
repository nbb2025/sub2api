//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBailianWorkbenchInlineMediaKeepsPublicParserURLOnly(t *testing.T) {
	ctx := WithBailianWorkbenchMedia(context.Background())
	for kind, mimes := range map[string][]string{
		"reference_video": {"video/mp4", "video/quicktime"},
		"reference_audio": {"audio/wav", "audio/x-wav", "audio/mpeg", "audio/mp3"},
	} {
		for _, mime := range mimes {
			body := []byte(fmt.Sprintf(`{"model":"wan-public","input":{"media":[{"type":%q,"url":%q}]},"parameters":{"duration":2}}`, kind, "data:"+mime+";base64,AAAA"))
			_, err := ParseBailianVideoRequest(body)
			require.Error(t, err, "public parser accepted %s", mime)
			_, err = ParseBailianVideoRequestForContext(context.Background(), body)
			require.Error(t, err, "ordinary context accepted %s", mime)
			_, err = ParseBailianVideoRequestForContext(ctx, body)
			require.NoError(t, err, mime)
		}
	}
	for _, raw := range []string{"data:video/mp4;base64,", "data:video/mp4;base64,A", "data:video/mp4;base64,AB==", "data:video/mp4;base64,AAAA\n", "data:audio/wav;base64,AAAA", "data:video/webm;base64,AAAA"} {
		require.False(t, validBailianWorkbenchMedia("reference_video", raw), raw)
	}
	require.False(t, validBailianWorkbenchMedia("file", "data:video/mp4;base64,AAAA"))
	require.False(t, validBailianWorkbenchMedia("reference_audio", "data:audio/wav;base64,"+strings.Repeat("AAAA", 5_000_001)))
}

func TestBailianWorkbenchForwardingPreservesInlineMediaAndStripsSession(t *testing.T) {
	body := []byte(`{"model":"wan-public","input":{"prompt":"test","media":[{"type":"reference_video","url":"data:video/mp4;base64,AAAA"},{"type":"reference_audio","url":"data:audio/wav;base64,AA=="}]},"parameters":{"duration":2,"resolution":"480P"}}`)
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"output":{"task_id":"fixture","task_status":"PENDING"}}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/wan/workbench/videos/generations", nil)
	c.Request.Header.Set("X-Iagent-Studio-Session", "login-secret")
	result, err := svc.ForwardBailianVideo(WithBailianWorkbenchMedia(c.Request.Context()), c, bailianTestAccount(), BailianEndpointCreate, "", body)
	require.NoError(t, err)
	require.Equal(t, "bailian:fixture", result.ResponseID)
	require.Empty(t, upstream.request.Header.Get("X-Iagent-Studio-Session"))
	require.Equal(t, "Bearer bailian-secret", upstream.request.Header.Get("Authorization"))
	require.Equal(t, BailianVideoCreatePath, upstream.request.URL.Path)
}
