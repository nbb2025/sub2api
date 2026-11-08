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
}
