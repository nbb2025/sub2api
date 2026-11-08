package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBailianEndpointNormalization(t *testing.T) {
	for _, test := range []struct{ raw, expected string }{
		{service.BailianVideoCreatePath, EndpointBailianVideoCreate},
		{"/api/v1/tasks/task-a", EndpointBailianTasks},
		{"/api/v1/tasks/task-b", EndpointBailianTasks},
	} {
		inbound := NormalizeInboundEndpoint(test.raw)
		require.Equal(t, test.expected, inbound)
		require.Equal(t, test.expected, DeriveUpstreamEndpoint(inbound, test.raw, service.PlatformBailian))
	}
	// Similar administrator resources retain their existing identity.
	require.Equal(t, "/api/v1/admin/tasks/example", NormalizeInboundEndpoint("/api/v1/admin/tasks/example"))
	for _, test := range []struct{ raw, inbound, upstream string }{
		{"/v1/videos/generations", EndpointVideosGenerations, EndpointBailianVideoCreate},
		{"/v1/videos/wan-task", EndpointVideos, EndpointBailianTasks},
		{"/v1/videos/generations/wan-task", EndpointVideosGenerations, EndpointBailianTasks},
	} {
		require.Equal(t, test.inbound, NormalizeInboundEndpoint(test.raw))
		require.Equal(t, test.upstream, DeriveUpstreamEndpoint(test.inbound, test.raw, service.PlatformBailian))
	}
}
