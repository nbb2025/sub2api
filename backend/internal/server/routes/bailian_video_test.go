package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBailianNativeRoutesRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformBailian)
	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, tc := range []struct{ method, path string }{{http.MethodPost, service.BailianVideoCreatePath}, {http.MethodGet, "/api/v1/tasks/:task_id"}, {http.MethodGet, "/api/v1/models"}} {
		require.True(t, registered[tc.method+" "+tc.path], tc.method+" "+tc.path)
	}
}
