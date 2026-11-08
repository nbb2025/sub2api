//go:build unit

package repository

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBailianUsageInsertPreservesExactDecimalJSON(t *testing.T) {
	u := &domain.VideoUsage{Provider: service.PlatformBailian, InputVideoDuration: json.Number("0.000000000000000001"), OutputVideoDuration: json.Number("1"), Duration: json.Number("1.000000000000000001"), Resolution: "720p"}
	p := prepareUsageLogInsert(&service.UsageLog{UserID: 1, APIKeyID: 2, AccountID: 3, RequestID: "bailian:precision", Model: "wan3.0-video", VideoCount: 1, VideoUsage: u})
	require.Equal(t, "jsonb", usageLogInsertArgTypes[len(usageLogInsertArgTypes)-1])
	raw, ok := p.args[len(p.args)-1].(string)
	require.True(t, ok)
	require.Contains(t, raw, `"duration":1.000000000000000001`)
	var loaded domain.VideoUsage
	require.NoError(t, json.Unmarshal([]byte(raw), &loaded))
	require.Equal(t, u, &loaded)
	require.Contains(t, usageLogSelectColumns, "video_usage")
	legacy := prepareUsageLogInsert(&service.UsageLog{UserID: 1, APIKeyID: 2, AccountID: 3, Model: "grok-imagine-video"})
	require.Nil(t, legacy.args[len(legacy.args)-1], "historical/Grok rows have no fabricated native usage")
}
